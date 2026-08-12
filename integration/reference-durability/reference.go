package referencedurability

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/faustbrian/golib/pkg/idempotency"
	"github.com/faustbrian/golib/pkg/idempotency/idempotencyoutbox"
	idempotencypostgres "github.com/faustbrian/golib/pkg/idempotency/postgres"
	"github.com/faustbrian/golib/pkg/migrations"
	migrationpostgres "github.com/faustbrian/golib/pkg/migrations/postgres"
	"github.com/faustbrian/golib/pkg/outbox"
	outboxqueue "github.com/faustbrian/golib/pkg/outbox/adapters/queue"
	outboxpostgres "github.com/faustbrian/golib/pkg/outbox/postgres"
	"github.com/faustbrian/golib/pkg/outbox/relay"
	golibpostgres "github.com/faustbrian/golib/pkg/postgres"
	"github.com/faustbrian/golib/pkg/queue/core"
	"github.com/faustbrian/golib/pkg/queue/valkeystream"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

var (
	// ErrContextRequired identifies a missing caller-owned lifecycle context.
	ErrContextRequired = errors.New("reference durability: context is required")
	// ErrInvalidConfig identifies incomplete external-service configuration.
	ErrInvalidConfig = errors.New("reference durability: invalid configuration")
)

const (
	operationKey = "reference-command-1"
	resultValue  = "accepted"
)

// Config identifies disposable PostgreSQL and Valkey dependencies.
type Config struct {
	// DatabaseURL is a PostgreSQL connection string for disposable assurance state.
	DatabaseURL string
	// ValkeyAddress is a standalone Valkey host and port.
	ValkeyAddress string
	// Stream is the task-owned Valkey Stream key.
	Stream string
}

// Result is the payload-free proof returned by a completed scenario.
type Result struct {
	// FirstOutcome is the initial durable idempotency acquisition result.
	FirstOutcome idempotency.Outcome `json:"first_outcome"`
	// ReplayOutcome is the result returned for the identical repeated command.
	ReplayOutcome idempotency.Outcome `json:"replay_outcome"`
	// BusinessRows is the number of committed application records.
	BusinessRows int `json:"business_rows"`
	// OutboxState is the final durable outbox lifecycle state.
	OutboxState string `json:"outbox_state"`
	// TaskID is the stable envelope identity observed by the queue consumer.
	TaskID string `json:"task_id"`
	// TaskKey is the stable idempotency identity observed by the queue consumer.
	TaskKey string `json:"task_key"`
	// Redelivered reports that an unacknowledged task survived consumer restart.
	Redelivered bool `json:"redelivered"`
	// RollbackIsolated reports that an aborted transaction exposed no business,
	// completion, or outbox state.
	RollbackIsolated bool `json:"rollback_isolated"`
}

// Run migrates a disposable database, commits one business mutation with its
// idempotency result and outbox intent, relays the intent through Valkey
// Streams, acknowledges it, and proves a repeated command replays.
func Run(ctx context.Context, config Config) (result Result, err error) {
	if ctx == nil {
		return Result{}, ErrContextRequired
	}
	if config.DatabaseURL == "" || config.ValkeyAddress == "" || config.Stream == "" {
		return Result{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	pool, err := golibpostgres.New(ctx, golibpostgres.Config{
		DSN: config.DatabaseURL, MaxConns: 4,
		AcquireTimeout: 5 * time.Second, PingTimeout: 5 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: open PostgreSQL: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = errors.Join(err, pool.Close(closeCtx))
	}()

	if err := migrate(ctx, stdlib.OpenDBFromPool(pool.Raw())); err != nil {
		return Result{}, err
	}

	idempotencyStore, err := idempotencypostgres.New(pool.Raw(), idempotencypostgres.Options{
		Retention: 24 * time.Hour, OwnerTokens: randomToken,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create idempotency store: %w", err)
	}
	key, err := idempotency.NewKey("assurance", "tenant-1", "create", "caller-1", operationKey)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create key: %w", err)
	}
	fingerprint, err := idempotency.NewFingerprint("v1", []byte(`{"value":"reference"}`))
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create fingerprint: %w", err)
	}
	acquired, err := idempotencyStore.Acquire(ctx, idempotency.AcquireRequest{
		Key: key, Fingerprint: fingerprint, Lease: time.Minute,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: acquire command: %w", err)
	}
	if acquired.Outcome != idempotency.OutcomeAcquired {
		return Result{}, fmt.Errorf("reference durability: first outcome %q", acquired.Outcome)
	}
	result.FirstOutcome = acquired.Outcome

	builder, err := outbox.NewEnvelopeBuilder()
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create envelope builder: %w", err)
	}
	envelope, err := builder.Build(outbox.NewEnvelopeParams{
		Topic: "reference.created", Payload: []byte(`{"id":"reference-1"}`),
		IdempotencyKey: operationKey,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: build envelope: %w", err)
	}
	writer, err := outboxpostgres.NewWriter(outboxpostgres.WriterConfig{})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create outbox writer: %w", err)
	}
	rollbackTx, err := pool.Raw().Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: begin rollback transaction: %w", err)
	}
	if err := stageCommand(ctx, rollbackTx, writer, envelope, idempotencyStore, acquired.Record.Ownership()); err != nil {
		rollback(rollbackTx)
		return Result{}, err
	}
	if err := rollbackTx.Rollback(ctx); err != nil {
		return Result{}, fmt.Errorf("reference durability: rollback transaction: %w", err)
	}
	retained, err := idempotencyStore.Inspect(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: inspect rollback ownership: %w", err)
	}
	var rolledBackBusiness, rolledBackOutbox int
	if err := pool.Raw().QueryRow(ctx, "SELECT count(*) FROM reference_commands").Scan(&rolledBackBusiness); err != nil {
		return Result{}, fmt.Errorf("reference durability: inspect rolled-back business state: %w", err)
	}
	if err := pool.Raw().QueryRow(ctx, "SELECT count(*) FROM outbox_messages").Scan(&rolledBackOutbox); err != nil {
		return Result{}, fmt.Errorf("reference durability: inspect rolled-back outbox state: %w", err)
	}
	if retained.State != idempotency.StateAcquired || rolledBackBusiness != 0 || rolledBackOutbox != 0 {
		return Result{}, errors.New("reference durability: rollback exposed staged state")
	}
	result.RollbackIsolated = true

	commitTx, err := pool.Raw().Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: begin commit transaction: %w", err)
	}
	defer rollback(commitTx)
	if err := stageCommand(ctx, commitTx, writer, envelope, idempotencyStore, acquired.Record.Ownership()); err != nil {
		return Result{}, err
	}
	if err := commitTx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("reference durability: commit transaction: %w", err)
	}

	producer, err := valkeystream.NewPublisherE(
		valkeystream.WithAddress(config.ValkeyAddress),
		valkeystream.WithStreamName(config.Stream), valkeystream.WithMaxLength(128),
	)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create Valkey publisher: %w", err)
	}
	defer func() { err = joinShutdown(err, producer.Shutdown()) }()
	worker, err := valkeystream.NewWorkerE(
		valkeystream.WithAddress(config.ValkeyAddress),
		valkeystream.WithStreamName(config.Stream), valkeystream.WithGroup("assurance"),
		valkeystream.WithConsumer("reference-consumer"), valkeystream.WithMaxLength(128),
		valkeystream.WithRequestTimeout(5*time.Second),
		valkeystream.WithReclaim(100*time.Millisecond, 100*time.Millisecond, 16),
	)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create Valkey worker: %w", err)
	}
	workerClosed := false
	defer func() {
		if !workerClosed {
			err = joinShutdown(err, worker.Shutdown())
		}
	}()
	queuePublisher, err := outboxqueue.New(producer)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create queue publisher: %w", err)
	}
	store, err := outboxpostgres.NewStore(pool.Raw(), outboxpostgres.StoreConfig{MaxClaimBatch: 8})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create outbox store: %w", err)
	}
	outboxRelay, err := relay.New(store, queuePublisher, relay.Config{
		Owner: "reference-relay", BatchSize: 8, Workers: 1,
		LeaseDuration: time.Minute, ClassifyError: outboxqueue.ClassifyError,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create relay: %w", err)
	}
	relayResult, err := outboxRelay.RunOnce(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: relay: %w", err)
	}
	if relayResult.Delivered != 1 {
		return Result{}, fmt.Errorf("reference durability: delivered %d envelopes", relayResult.Delivered)
	}

	delivery, err := worker.Request()
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: receive task: %w", err)
	}
	acknowledger, ok := delivery.(core.Acknowledger)
	if !ok || !acknowledger.AcknowledgementRequired() {
		return Result{}, errors.New("reference durability: durable acknowledgement is required")
	}
	firstPayload := append([]byte(nil), delivery.Payload()...)
	if err := worker.Shutdown(); err != nil {
		return Result{}, fmt.Errorf("reference durability: stop abandoned consumer: %w", err)
	}
	workerClosed = true

	reclaimer, err := valkeystream.NewWorkerE(
		valkeystream.WithAddress(config.ValkeyAddress),
		valkeystream.WithStreamName(config.Stream), valkeystream.WithGroup("assurance"),
		valkeystream.WithConsumer("recovery-consumer"), valkeystream.WithMaxLength(128),
		valkeystream.WithRequestTimeout(5*time.Second),
		valkeystream.WithReclaim(100*time.Millisecond, 100*time.Millisecond, 16),
	)
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: create recovery worker: %w", err)
	}
	defer func() { err = joinShutdown(err, reclaimer.Shutdown()) }()
	recovered, err := reclaimer.Request()
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: recover task: %w", err)
	}
	if !bytes.Equal(firstPayload, recovered.Payload()) {
		return Result{}, errors.New("reference durability: recovered task changed")
	}
	var task outboxqueue.Task
	if err := json.Unmarshal(recovered.Payload(), &task); err != nil {
		return Result{}, fmt.Errorf("reference durability: decode recovered task: %w", err)
	}
	recoveryAcknowledgement, ok := recovered.(core.Acknowledger)
	if !ok || !recoveryAcknowledgement.AcknowledgementRequired() {
		return Result{}, errors.New("reference durability: recovered acknowledgement is required")
	}
	if err := recoveryAcknowledgement.Ack(); err != nil {
		return Result{}, fmt.Errorf("reference durability: acknowledge task: %w", err)
	}
	result.TaskID = task.TaskID
	result.TaskKey = task.IdempotencyKey
	result.Redelivered = true

	replayed, err := idempotencyStore.Acquire(ctx, idempotency.AcquireRequest{
		Key: key, Fingerprint: fingerprint, Lease: time.Minute,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reference durability: replay command: %w", err)
	}
	if replayed.Outcome != idempotency.OutcomeReplayed || string(replayed.Record.Result) != resultValue {
		return Result{}, fmt.Errorf("reference durability: replay outcome %q", replayed.Outcome)
	}
	result.ReplayOutcome = replayed.Outcome
	if err := pool.Raw().QueryRow(ctx, "SELECT count(*) FROM reference_commands").Scan(&result.BusinessRows); err != nil {
		return Result{}, fmt.Errorf("reference durability: count business rows: %w", err)
	}
	if err := pool.Raw().QueryRow(ctx, "SELECT state FROM outbox_messages WHERE id = $1", envelope.ID).Scan(&result.OutboxState); err != nil {
		return Result{}, fmt.Errorf("reference durability: inspect outbox state: %w", err)
	}
	if result.BusinessRows != 1 || result.OutboxState != "delivered" ||
		result.TaskID != envelope.ID || result.TaskKey != operationKey {
		return Result{}, fmt.Errorf("reference durability: inconsistent result: %#v", result)
	}

	return result, nil
}

func migrate(ctx context.Context, database *sql.DB) error {
	defer func() { _ = database.Close() }()
	source, err := composedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("reference durability: compose migrations: %w", err)
	}
	backend, err := migrationpostgres.New(database, migrationpostgres.WithLockTimeout(5*time.Second), migrationpostgres.WithStatementTimeout(30*time.Second))
	if err != nil {
		return fmt.Errorf("reference durability: create migration backend: %w", err)
	}
	runner, err := migrations.NewRunner(staticSource(source), backend)
	if err != nil {
		return fmt.Errorf("reference durability: create migration runner: %w", err)
	}
	if _, err := runner.Up(ctx); err != nil {
		return fmt.Errorf("reference durability: migrate: %w", err)
	}

	return nil
}

func stageCommand(
	ctx context.Context,
	tx pgx.Tx,
	writer *outboxpostgres.Writer,
	envelope outbox.Envelope,
	store *idempotencypostgres.Store,
	ownership idempotency.Ownership,
) error {
	if _, err := tx.Exec(ctx, "INSERT INTO reference_commands (id, value) VALUES ($1, $2)", operationKey, resultValue); err != nil {
		return fmt.Errorf("reference durability: insert business state: %w", err)
	}
	if _, err := idempotencyoutbox.InsertAndComplete(
		ctx, tx, writer, envelope, store,
		idempotency.CompleteRequest{Ownership: ownership, Result: []byte(resultValue)},
	); err != nil {
		return fmt.Errorf("reference durability: stage completion: %w", err)
	}

	return nil
}

func composedMigrations(ctx context.Context) ([]migrations.Migration, error) {
	outboxSource, err := migrations.NewFSSource(outboxpostgres.Migrations(), ".")
	if err != nil {
		return nil, err
	}
	outboxMigrations, err := outboxSource.Load(ctx)
	if err != nil {
		return nil, err
	}
	if len(outboxMigrations) != 1 {
		return nil, fmt.Errorf("outbox migration count %d", len(outboxMigrations))
	}
	outboxMigration, err := migrations.NewMigration(
		1, "create_outbox", outboxMigrations[0].TransactionMode(),
		outboxMigrations[0].UpSQL(), outboxMigrations[0].DownSQL(),
	)
	if err != nil {
		return nil, err
	}
	idempotencyMigration, err := idempotencypostgres.GoMigration()
	if err != nil {
		return nil, err
	}
	idempotencyMigration, err = migrations.NewMigration(
		2, idempotencyMigration.Name(), idempotencyMigration.TransactionMode(),
		idempotencyMigration.UpSQL(), idempotencyMigration.DownSQL(),
	)
	if err != nil {
		return nil, err
	}
	applicationMigration, err := migrations.NewMigration(
		3, "create_reference_commands", migrations.TransactionModeDefault,
		"CREATE TABLE reference_commands (id text PRIMARY KEY, value text NOT NULL)",
		"DROP TABLE reference_commands",
	)
	if err != nil {
		return nil, err
	}

	return []migrations.Migration{outboxMigration, idempotencyMigration, applicationMigration}, nil
}

type staticSource []migrations.Migration

// Load returns an isolated copy of the composed migration sequence.
func (source staticSource) Load(ctx context.Context) ([]migrations.Migration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return append([]migrations.Migration(nil), source...), nil
}

func randomToken() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(data[:]), nil
}

func joinShutdown(current, shutdown error) error {
	if shutdown == nil {
		return current
	}

	return errors.Join(current, shutdown)
}

func rollback(tx interface{ Rollback(context.Context) error }) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
