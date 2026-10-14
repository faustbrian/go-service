//go:build integration

package referencedurability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	golibpostgres "github.com/faustbrian/go-postgres/v2"
	"github.com/faustbrian/go-queue"
	"github.com/faustbrian/go-queue/core"
	"github.com/faustbrian/go-queue/valkeystream"
	scheduler "github.com/faustbrian/go-scheduler/v2"
	schedulerqueue "github.com/faustbrian/go-scheduler/v2/adapters/queue"
	schedulerpostgres "github.com/faustbrian/go-scheduler/v2/postgres"
	outboxqueue "github.com/faustbrian/go-transactional-outbox/adapters/queue/v2"
	outbox "github.com/faustbrian/go-transactional-outbox/v2"
	outboxpostgres "github.com/faustbrian/go-transactional-outbox/v2/postgres"
	"github.com/faustbrian/go-transactional-outbox/v2/relay"
	webhook "github.com/faustbrian/go-webhook/v3"
	webhookoutbox "github.com/faustbrian/go-webhook/v3/adapters/outbox"
	webhookqueue "github.com/faustbrian/go-webhook/v3/adapters/queue"
	"github.com/jackc/pgx/v5"
)

// The lease is Scheduler's published PostgreSQL port, not external Lease v2.
// Two real replicas must agree on one physical occurrence before public queue
// dispatch serializes and persists it in the existing Valkey supplier.
func TestSchedulerPostgresLeaseDispatchesToDurableQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, schema := causalPool(t, ctx)
	migration, err := schedulerpostgres.SchemaMigrationFor(schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, migration.Up); err != nil {
		t.Fatal(err)
	}
	leases, err := schedulerpostgres.NewWithSchema(pool.Raw(), schema)
	if err != nil {
		t.Fatal(err)
	}
	stream := "reference-scheduler-" + schema
	publisher := causalPublisher(t, stream)
	dispatcher, err := schedulerqueue.New(publisher)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := scheduler.NewSchedule("durable-edge", "reference.execute", scheduler.EveryMinute(),
		scheduler.WithOneServer(time.Minute),
		scheduler.WithParameters(map[string]any{"value": "preserved"}))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := scheduler.Compile(schedule)
	if err != nil {
		t.Fatal(err)
	}
	runners := make([]*scheduler.Runner, 0, 2)
	for _, owner := range []string{"replica-a", "replica-b"} {
		runner, err := scheduler.NewRunner(registry, leases, dispatcher, scheduler.WithOwner(owner))
		if err != nil {
			t.Fatal(err)
		}
		runners = append(runners, runner)
		t.Cleanup(func() {
			drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer drainCancel()
			if err := runner.Drain(drainCtx); err != nil {
				t.Errorf("drain scheduler: %v", err)
			}
		})
	}
	through := time.Now().UTC().Truncate(time.Minute)
	ticks := make(chan error, len(runners))
	for _, runner := range runners {
		go func() { ticks <- runner.Tick(ctx, through.Add(-time.Minute), through) }()
	}
	for range runners {
		select {
		case err := <-ticks:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("scheduler ticks did not join before the deadline")
		}
	}
	worker := causalWorker(t, stream, "scheduled", nil)
	delivery, err := worker.Request()
	if err != nil {
		t.Fatal(err)
	}
	var occurrence schedulerqueue.Envelope
	if err := json.Unmarshal(delivery.Payload(), &occurrence); err != nil {
		t.Fatal(err)
	}
	if occurrence.ScheduleID != schedule.Identity || occurrence.CoordinationID != schedule.CoordinationID ||
		occurrence.Task != "reference.execute" || !occurrence.Occurrence.Equal(through) ||
		occurrence.IdempotencyKey == "" || occurrence.FencingToken != 1 ||
		occurrence.Parameters["value"] != "preserved" {
		t.Fatalf("durable occurrence changed: %#v", occurrence)
	}
	var owners int
	var owner string
	var fence int64
	if err := pool.Raw().QueryRow(ctx,
		"SELECT count(*), min(owner), min(fencing_token) FROM "+pgx.Identifier{schema, "scheduler_leases"}.Sanitize()+" WHERE lease_key = $1",
		"occurrence:"+occurrence.IdempotencyKey).Scan(&owners, &owner, &fence); err != nil {
		t.Fatal(err)
	}
	if owners != 1 || owner != occurrence.Owner || fence != int64(occurrence.FencingToken) ||
		(owner != "replica-a" && owner != "replica-b") {
		t.Fatalf("persisted ownership differs: rows=%d owner=%q fence=%d", owners, owner, fence)
	}
	acknowledgeCausalTask(t, delivery)
	assertCausalSettlement(t, ctx, worker, 0)
	for _, runner := range runners {
		if err := runner.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		if err := runner.Tick(ctx, through, through.Add(time.Minute)); !errors.Is(err, scheduler.ErrDraining) {
			t.Fatalf("post-drain tick = %v, want ErrDraining", err)
		}
	}
}

// The first real HTTP response is retryable. Queue.Handle must perform just
// one attempt, leaving the durable task unacknowledged. A replacement consumer
// reclaims identical bytes and acknowledges only after real HTTP success.
func TestDurableOutboxQueueReclaimDeliversWebhookHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, schema := causalPool(t, ctx)
	// Migrations v2 owns a public-schema ledger. The original scenario tests
	// that runner; this isolated delivery edge needs only its same canonical
	// Outbox SQL, without pretending a second schema has an independent ledger.
	sequence, err := composedMigrations(ctx)
	if err != nil || len(sequence) != 3 {
		t.Fatalf("canonical migration sequence = %d, %v", len(sequence), err)
	}
	if _, err := pool.Raw().Exec(ctx, sequence[0].UpSQL()); err != nil {
		t.Fatal(err)
	}
	type received struct {
		body []byte
		key  string
	}
	requests := make(chan received, 4)
	var calls atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(body) > 4096 {
			http.Error(w, "invalid bounded fixture body", http.StatusBadRequest)
			return
		}
		select {
		case requests <- received{body: body, key: r.Header.Get(webhook.IdempotencyHeader)}:
		default:
			http.Error(w, "unexpected extra attempt", http.StatusBadRequest)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL + "/hook")
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Timeout = 2 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	signingKey, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := webhook.NewSigner(webhook.SignerConfig{
		Algorithm: webhook.SHA256,
		Keys:      []webhook.SigningKey{{ID: "fixture", Secret: []byte(signingKey)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deliverer, err := webhook.NewDeliverer(webhook.DeliveryConfig{
		Client: client, Signer: signer,
		EndpointPolicy: webhook.EndpointPolicyFunc(func(_ context.Context, candidate *url.URL) error {
			if candidate.String() != endpoint.String() {
				return webhook.ErrEndpointRejected
			}
			return nil
		}),
		Retry:           webhook.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Second},
		IDGenerator:     func() (string, error) { return "reference-http-delivery", nil },
		MaxRequestBytes: 4096, MaxResponseBytes: 4096, MaxFanOut: 1,
		HeaderLimits: webhook.HeaderLimits{MaxSignatures: 1, MaxBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"event":"durable-webhook"}`)
	builder, err := outbox.NewEnvelopeBuilder()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := webhookoutbox.Build(builder, "reference.webhook", webhook.DeliveryRequest{
		Endpoint: endpoint, Body: body, EventID: "event-1", IdempotencyKey: "webhook-command-1",
	}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := outboxpostgres.NewWriter(outboxpostgres.WriterConfig{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if err := writer.Insert(ctx, tx, envelope); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	stream := "reference-webhook-" + schema
	publisher := causalPublisher(t, stream)
	queuePublisher, err := outboxqueue.New(publisher)
	if err != nil {
		t.Fatal(err)
	}
	store, err := outboxpostgres.NewStore(pool.Raw(), outboxpostgres.StoreConfig{Schema: schema, MaxClaimBatch: 1})
	if err != nil {
		t.Fatal(err)
	}
	workerRelay, err := relay.New(store, queuePublisher, relay.Config{
		Owner: "webhook-relay", BatchSize: 1, Workers: 1, LeaseDuration: time.Minute,
		ClassifyError: outboxqueue.ClassifyError,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := workerRelay.RunOnce(ctx)
	if err != nil || result.Delivered != 1 {
		t.Fatalf("relay = %#v, %v", result, err)
	}
	var handled webhook.DeliveryResult
	handle := func(ctx context.Context, delivery core.TaskMessage) error {
		var task outboxqueue.Task
		if err := json.Unmarshal(delivery.Payload(), &task); err != nil {
			return err
		}
		if task.TaskID != envelope.ID || task.IdempotencyKey != envelope.IdempotencyKey ||
			task.OrderingKey != "event-1" || !bytes.Equal(task.Content, envelope.Payload) {
			return errors.New("durable webhook task identity or content changed")
		}
		var err error
		handled, err = webhookqueue.Handle(ctx, deliverer, task.Content, 4096)
		return err
	}
	first := causalWorker(t, stream, "failed-http", handle)
	delivery, err := first.Request()
	if err != nil {
		t.Fatal(err)
	}
	firstBytes := append([]byte(nil), delivery.Payload()...)
	if err := first.Run(ctx, delivery); !errors.Is(err, webhook.ErrDeliveryFailed) {
		t.Fatalf("first HTTP handling = %v, want ErrDeliveryFailed", err)
	}
	if len(handled.Attempts) != 1 || handled.Attempts[0].StatusCode != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("queue handler multiplied attempts: %#v, calls=%d", handled, calls.Load())
	}
	stats, err := first.Stats(ctx)
	if err != nil || stats.Acknowledged != 0 || stats.Pending != 1 {
		t.Fatalf("failed delivery was settled: %#v, %v", stats, err)
	}
	if err := first.Shutdown(); err != nil {
		t.Fatal(err)
	}
	reclaimer := causalWorker(t, stream, "successful-http", handle)
	// Queue v1.1.2's persistent reclaimLoop retries Claim every 100ms. Request
	// waits up to five seconds on that loop's delivery channel; it is not a
	// one-shot claim made before the pending record becomes eligible.
	recovered, err := reclaimer.Request()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, recovered.Payload()) {
		t.Fatal("reclaimed webhook task bytes changed")
	}
	if err := reclaimer.Run(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	if handled.ID != "reference-http-delivery" || handled.EventID != "event-1" ||
		len(handled.Attempts) != 1 || handled.Attempts[0].StatusCode != http.StatusNoContent || calls.Load() != 2 {
		t.Fatalf("successful HTTP handling changed: %#v, calls=%d", handled, calls.Load())
	}
	stats, err = reclaimer.Stats(ctx)
	if err != nil || stats.Acknowledged != 0 || stats.Pending != 1 {
		t.Fatalf("HTTP success was acknowledged before explicit settlement: %#v, %v", stats, err)
	}
	for range 2 {
		select {
		case request := <-requests:
			if !bytes.Equal(request.body, body) || request.key != envelope.IdempotencyKey {
				t.Fatalf("real HTTP body/key changed: %#v", request)
			}
		case <-ctx.Done():
			t.Fatal("real HTTP requests were not observed")
		}
	}
	acknowledgeCausalTask(t, recovered)
	assertCausalSettlement(t, ctx, reclaimer, 1)
	var state string
	if err := pool.Raw().QueryRow(ctx, "SELECT state FROM "+pgx.Identifier{schema, "outbox_messages"}.Sanitize()+" WHERE id = $1", envelope.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" {
		t.Fatalf("durable relay state = %q, want delivered", state)
	}
}

func causalPool(t *testing.T, ctx context.Context) (*golibpostgres.Pool, string) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("VALKEY_ADDRESS") == "" {
		t.Fatal("DATABASE_URL and VALKEY_ADDRESS are required")
	}
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	schema := "reference_edge_" + token
	admin, err := golibpostgres.Connect(ctx, golibpostgres.Config{
		DSN: dsn, ResolveDSN: resolvePostgresDSN, StartupPolicy: golibpostgres.StartupPing,
		MaxConns: 2, AcquireTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, dropErr := admin.Raw().Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if err := errors.Join(dropErr, admin.Shutdown(cleanupCtx)); err != nil {
			t.Errorf("clean isolated schema: %v", err)
		}
	})
	if _, err := admin.Raw().Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	pool, err := golibpostgres.Connect(ctx, golibpostgres.Config{
		DSN: dsn, StartupPolicy: golibpostgres.StartupPing, MaxConns: 4,
		AcquireTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		ResolveDSN: func(ctx context.Context, dsn string) (*golibpostgres.PoolConfig, error) {
			config, err := resolvePostgresDSN(ctx, dsn)
			if err == nil {
				config.ConnConfig.RuntimeParams["search_path"] = schema
			}
			return config, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := pool.Shutdown(cleanupCtx); err != nil {
			t.Errorf("close isolated pool: %v", err)
		}
	})
	return pool, schema
}

func causalPublisher(t *testing.T, stream string) *valkeystream.Publisher {
	t.Helper()
	publisher, err := valkeystream.NewPublisherE(valkeystream.WithAddress(os.Getenv("VALKEY_ADDRESS")),
		valkeystream.WithStreamName(stream), valkeystream.WithMaxLength(128))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := publisher.Shutdown(); err != nil {
			t.Errorf("close causal publisher: %v", err)
		}
	})
	return publisher
}

func causalWorker(t *testing.T, stream, consumer string, handler func(context.Context, core.TaskMessage) error) *valkeystream.Worker {
	t.Helper()
	options := []valkeystream.Option{
		valkeystream.WithAddress(os.Getenv("VALKEY_ADDRESS")), valkeystream.WithStreamName(stream),
		valkeystream.WithGroup("causal-edge"), valkeystream.WithConsumer(consumer), valkeystream.WithMaxLength(128),
		valkeystream.WithRequestTimeout(5 * time.Second), valkeystream.WithReclaim(100*time.Millisecond, 100*time.Millisecond, 16),
	}
	if handler != nil {
		options = append(options, valkeystream.WithRunFunc(handler))
	}
	worker, err := valkeystream.NewWorkerE(options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := worker.Shutdown(); err != nil && !errors.Is(err, queue.ErrQueueShutdown) {
			t.Errorf("close causal worker: %v", err)
		}
	})
	return worker
}

func acknowledgeCausalTask(t *testing.T, task core.TaskMessage) {
	t.Helper()
	acknowledger, ok := task.(core.Acknowledger)
	if !ok || !acknowledger.AcknowledgementRequired() {
		t.Fatal("durable supplier did not require acknowledgement")
	}
	if err := acknowledger.Ack(); err != nil {
		t.Fatal(err)
	}
}

func assertCausalSettlement(t *testing.T, ctx context.Context, worker *valkeystream.Worker, reclaimed uint64) {
	t.Helper()
	stats, err := worker.Stats(ctx)
	if err != nil || stats.Acknowledged != 1 || stats.Reclaimed != reclaimed || stats.Pending != 0 || !stats.LagKnown || stats.Lag != 0 {
		t.Fatalf("durable settlement = %#v, %v", stats, err)
	}
}
