package referencedurability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/faustbrian/golib/pkg/idempotency"
	"github.com/faustbrian/golib/pkg/idempotency/idempotencyoutbox"
	idempotencypostgres "github.com/faustbrian/golib/pkg/idempotency/postgres"
	"github.com/faustbrian/golib/pkg/outbox"
	outboxqueue "github.com/faustbrian/golib/pkg/outbox/adapters/queue"
	outboxpostgres "github.com/faustbrian/golib/pkg/outbox/postgres"
	"github.com/faustbrian/golib/pkg/outbox/relay"
	golibpostgres "github.com/faustbrian/golib/pkg/postgres"
	"github.com/faustbrian/golib/pkg/queue/core"
	"github.com/faustbrian/golib/pkg/queue/valkeystream"
	"github.com/jackc/pgx/v5/stdlib"
)

var (
	// ErrInvalidRecoveryExpectation identifies missing durable identities needed
	// to distinguish recovery from consuming unrelated work.
	ErrInvalidRecoveryExpectation = errors.New("reference durability: invalid recovery expectation")
)

// RecoveryExpectation contains the stable durable identities written before a
// process is terminated and required by the recovery process.
type RecoveryExpectation struct {
	// EnvelopeID identifies the committed outbox record.
	EnvelopeID string `json:"envelope_id"`
	// TaskID identifies the queue task derived from the envelope.
	TaskID string `json:"task_id"`
	// TaskKey identifies the command-level idempotency key.
	TaskKey string `json:"task_key"`
}

// RecoveryResult reports the state observed and acknowledged by a fresh
// process after durable dependencies become available again.
type RecoveryResult struct {
	// ReplayOutcome is the idempotency result for the committed command.
	ReplayOutcome idempotency.Outcome `json:"replay_outcome"`
	// BusinessRows is the number of committed application records.
	BusinessRows int `json:"business_rows"`
	// OutboxState is the persisted outbox lifecycle state.
	OutboxState string `json:"outbox_state"`
	// TaskID identifies the reclaimed queue task.
	TaskID string `json:"task_id"`
	// TaskKey identifies the reclaimed task's idempotency key.
	TaskKey string `json:"task_key"`
	// Reclaimed reports that a fresh consumer received the abandoned task.
	Reclaimed bool `json:"reclaimed"`
	// Acknowledged reports that recovery durably acknowledged the task.
	Acknowledged bool `json:"acknowledged"`
}

// RecoverySession owns the live resources intentionally abandoned by the
// process-death harness. Production callers should not use this fixture type.
type RecoverySession struct {
	expectation RecoveryExpectation
	close       func() error
	once        sync.Once
	err         error
}

// Expectation returns stable identities for a fresh recovery process.
func (session *RecoverySession) Expectation() RecoveryExpectation {
	if session == nil {
		return RecoveryExpectation{}
	}

	return session.expectation
}

// Close releases a session when a test exits without killing its process.
func (session *RecoverySession) Close() error {
	if session == nil {
		return nil
	}
	session.once.Do(func() {
		if session.close != nil {
			session.err = session.close()
		}
	})

	return session.err
}

// PrepareRecovery atomically commits business, idempotency, and outbox state,
// publishes and claims the resulting queue task without acknowledging it, and
// retains the live consumer so an external harness can terminate the process.
func PrepareRecovery(ctx context.Context, config Config) (*RecoverySession, error) {
	if err := validateRecoveryInput(ctx, config); err != nil {
		return nil, err
	}
	pool, err := openRecoveryPool(ctx, config)
	if err != nil {
		return nil, err
	}
	var producer *valkeystream.Publisher
	var worker *valkeystream.Worker
	cleanup := func() error {
		var cleanupErr error
		if worker != nil {
			cleanupErr = errors.Join(cleanupErr, worker.Shutdown())
		}
		if producer != nil {
			cleanupErr = errors.Join(cleanupErr, producer.Shutdown())
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(cleanupErr, pool.Close(closeCtx))
	}
	owned := true
	defer func() {
		if owned {
			_ = cleanup()
		}
	}()

	store, key, fingerprint, err := recoveryIdempotency(pool)
	if err != nil {
		return nil, err
	}
	acquired, err := store.Acquire(ctx, idempotency.AcquireRequest{
		Key: key, Fingerprint: fingerprint, Lease: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("reference durability: acquire recovery command: %w", err)
	}
	if acquired.Outcome != idempotency.OutcomeAcquired {
		return nil, fmt.Errorf("reference durability: recovery prepare outcome %q", acquired.Outcome)
	}
	envelope, err := recoveryEnvelope()
	if err != nil {
		return nil, err
	}
	writer, err := outboxpostgres.NewWriter(outboxpostgres.WriterConfig{})
	if err != nil {
		return nil, fmt.Errorf("reference durability: create recovery outbox writer: %w", err)
	}
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("reference durability: begin recovery transaction: %w", err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, "INSERT INTO reference_commands (id, value) VALUES ($1, $2)", operationKey, resultValue); err != nil {
		return nil, fmt.Errorf("reference durability: insert recovery business state: %w", err)
	}
	if _, err := idempotencyoutbox.InsertAndComplete(
		ctx, tx, writer, envelope, store,
		idempotency.CompleteRequest{Ownership: acquired.Record.Ownership(), Result: []byte(resultValue)},
	); err != nil {
		return nil, fmt.Errorf("reference durability: stage recovery completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("reference durability: commit recovery transaction: %w", err)
	}

	producer, err = valkeystream.NewPublisherE(
		valkeystream.WithAddress(config.ValkeyAddress),
		valkeystream.WithStreamName(config.Stream), valkeystream.WithMaxLength(128),
	)
	if err != nil {
		return nil, fmt.Errorf("reference durability: create recovery publisher: %w", err)
	}
	worker, err = recoveryWorker(config, "process-to-kill")
	if err != nil {
		return nil, err
	}
	queuePublisher, err := outboxqueue.New(producer)
	if err != nil {
		return nil, fmt.Errorf("reference durability: create recovery queue publisher: %w", err)
	}
	outboxStore, err := outboxpostgres.NewStore(pool.Raw(), outboxpostgres.StoreConfig{MaxClaimBatch: 8})
	if err != nil {
		return nil, fmt.Errorf("reference durability: create recovery outbox store: %w", err)
	}
	outboxRelay, err := relay.New(outboxStore, queuePublisher, relay.Config{
		Owner: "recovery-relay", BatchSize: 8, Workers: 1,
		LeaseDuration: time.Minute, ClassifyError: outboxqueue.ClassifyError,
	})
	if err != nil {
		return nil, fmt.Errorf("reference durability: create recovery relay: %w", err)
	}
	relayResult, err := outboxRelay.RunOnce(ctx)
	if err != nil {
		return nil, fmt.Errorf("reference durability: relay recovery task: %w", err)
	}
	if relayResult.Delivered != 1 {
		return nil, fmt.Errorf("reference durability: recovery relay delivered %d envelopes", relayResult.Delivered)
	}
	delivery, err := worker.Request()
	if err != nil {
		return nil, fmt.Errorf("reference durability: claim recovery task: %w", err)
	}
	acknowledger, ok := delivery.(core.Acknowledger)
	if !ok || !acknowledger.AcknowledgementRequired() {
		return nil, errors.New("reference durability: recovery task requires durable acknowledgement")
	}
	var task outboxqueue.Task
	if err := json.Unmarshal(delivery.Payload(), &task); err != nil {
		return nil, fmt.Errorf("reference durability: decode prepared recovery task: %w", err)
	}
	expectation := RecoveryExpectation{
		EnvelopeID: envelope.ID, TaskID: task.TaskID, TaskKey: task.IdempotencyKey,
	}
	if err := validateRecoveryExpectation(expectation); err != nil {
		return nil, err
	}
	owned = false

	return &RecoverySession{expectation: expectation, close: cleanup}, nil
}

// Recover reconnects to durable dependencies, verifies the committed command,
// reclaims its abandoned queue task, and acknowledges that exact task.
func Recover(ctx context.Context, config Config, expectation RecoveryExpectation) (result RecoveryResult, err error) {
	if err := validateRecoveryInput(ctx, config); err != nil {
		return RecoveryResult{}, err
	}
	if err := validateRecoveryExpectation(expectation); err != nil {
		return RecoveryResult{}, err
	}
	pool, err := openRecoveryPool(ctx, config)
	if err != nil {
		return RecoveryResult{}, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = errors.Join(err, pool.Close(closeCtx))
	}()
	store, key, fingerprint, err := recoveryIdempotency(pool)
	if err != nil {
		return RecoveryResult{}, err
	}
	replayed, err := store.Acquire(ctx, idempotency.AcquireRequest{
		Key: key, Fingerprint: fingerprint, Lease: time.Minute,
	})
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("reference durability: inspect recovery command: %w", err)
	}
	if replayed.Outcome != idempotency.OutcomeReplayed || string(replayed.Record.Result) != resultValue {
		return RecoveryResult{}, fmt.Errorf("reference durability: recovery replay outcome %q", replayed.Outcome)
	}
	result.ReplayOutcome = replayed.Outcome
	if err := pool.Raw().QueryRow(ctx, "SELECT count(*) FROM reference_commands").Scan(&result.BusinessRows); err != nil {
		return RecoveryResult{}, fmt.Errorf("reference durability: inspect recovered business state: %w", err)
	}
	if err := pool.Raw().QueryRow(
		ctx, "SELECT state FROM outbox_messages WHERE id = $1", expectation.EnvelopeID,
	).Scan(&result.OutboxState); err != nil {
		return RecoveryResult{}, fmt.Errorf("reference durability: inspect recovered outbox state: %w", err)
	}
	if result.BusinessRows != 1 || result.OutboxState != "delivered" {
		return RecoveryResult{}, fmt.Errorf("reference durability: inconsistent recovered database state: %#v", result)
	}

	worker, err := recoveryWorker(config, "replacement-process")
	if err != nil {
		return RecoveryResult{}, err
	}
	defer func() { err = errors.Join(err, worker.Shutdown()) }()
	delivery, err := worker.Request()
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("reference durability: reclaim recovery task: %w", err)
	}
	var task outboxqueue.Task
	if err := json.Unmarshal(delivery.Payload(), &task); err != nil {
		return RecoveryResult{}, fmt.Errorf("reference durability: decode recovered queue task: %w", err)
	}
	if task.TaskID != expectation.TaskID || task.IdempotencyKey != expectation.TaskKey {
		return RecoveryResult{}, fmt.Errorf("reference durability: reclaimed unexpected task %q/%q", task.TaskID, task.IdempotencyKey)
	}
	acknowledgement, ok := delivery.(core.Acknowledger)
	if !ok || !acknowledgement.AcknowledgementRequired() {
		return RecoveryResult{}, errors.New("reference durability: reclaimed task requires durable acknowledgement")
	}
	if err := acknowledgement.Ack(); err != nil {
		return RecoveryResult{}, fmt.Errorf("reference durability: acknowledge recovered task: %w", err)
	}
	result.TaskID = task.TaskID
	result.TaskKey = task.IdempotencyKey
	result.Reclaimed = true
	result.Acknowledged = true

	return result, nil
}

// VerifyRecoveryAcknowledgement proves that the recovery consumer group has no
// pending or lagging work after the broker has restarted.
func VerifyRecoveryAcknowledgement(ctx context.Context, config Config) (err error) {
	if err := validateRecoveryInput(ctx, config); err != nil {
		return err
	}
	worker, err := recoveryWorker(config, "acknowledgement-verifier")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, worker.Shutdown()) }()
	stats, err := worker.Stats(ctx)
	if err != nil {
		return fmt.Errorf("reference durability: inspect recovered acknowledgement: %w", err)
	}
	if !stats.LagKnown || stats.Pending != 0 || stats.Lag != 0 || stats.Depth != 0 {
		return fmt.Errorf("reference durability: recovered acknowledgement state: %#v", stats)
	}
	return nil
}

func validateRecoveryInput(ctx context.Context, config Config) error {
	if ctx == nil {
		return ErrContextRequired
	}
	if config.DatabaseURL == "" || config.ValkeyAddress == "" || config.Stream == "" {
		return ErrInvalidConfig
	}
	return ctx.Err()
}

func validateRecoveryExpectation(expectation RecoveryExpectation) error {
	if expectation.EnvelopeID == "" || expectation.TaskID == "" || expectation.TaskKey == "" ||
		expectation.EnvelopeID != expectation.TaskID || expectation.TaskKey != operationKey {
		return ErrInvalidRecoveryExpectation
	}
	return nil
}

func openRecoveryPool(ctx context.Context, config Config) (*golibpostgres.Pool, error) {
	pool, err := golibpostgres.New(ctx, golibpostgres.Config{
		DSN: config.DatabaseURL, MaxConns: 4,
		AcquireTimeout: 5 * time.Second, PingTimeout: 5 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("reference durability: open recovery PostgreSQL: %w", err)
	}
	if err := migrate(ctx, stdlibDatabase(pool)); err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = pool.Close(closeCtx)
		return nil, err
	}
	return pool, nil
}

func stdlibDatabase(pool *golibpostgres.Pool) *sql.DB {
	return stdlib.OpenDBFromPool(pool.Raw())
}

func recoveryIdempotency(pool *golibpostgres.Pool) (*idempotencypostgres.Store, idempotency.Key, idempotency.Fingerprint, error) {
	store, err := idempotencypostgres.New(pool.Raw(), idempotencypostgres.Options{
		Retention: 24 * time.Hour, OwnerTokens: randomToken,
	})
	if err != nil {
		return nil, idempotency.Key{}, idempotency.Fingerprint{}, fmt.Errorf("reference durability: create recovery idempotency store: %w", err)
	}
	key, err := idempotency.NewKey("assurance", "tenant-1", "create", "caller-1", operationKey)
	if err != nil {
		return nil, idempotency.Key{}, idempotency.Fingerprint{}, fmt.Errorf("reference durability: create recovery key: %w", err)
	}
	fingerprint, err := idempotency.NewFingerprint("v1", []byte(`{"value":"reference"}`))
	if err != nil {
		return nil, idempotency.Key{}, idempotency.Fingerprint{}, fmt.Errorf("reference durability: create recovery fingerprint: %w", err)
	}
	return store, key, fingerprint, nil
}

func recoveryEnvelope() (outbox.Envelope, error) {
	builder, err := outbox.NewEnvelopeBuilder()
	if err != nil {
		return outbox.Envelope{}, fmt.Errorf("reference durability: create recovery envelope builder: %w", err)
	}
	envelope, err := builder.Build(outbox.NewEnvelopeParams{
		Topic: "reference.created", Payload: []byte(`{"id":"reference-1"}`),
		IdempotencyKey: operationKey,
	})
	if err != nil {
		return outbox.Envelope{}, fmt.Errorf("reference durability: build recovery envelope: %w", err)
	}
	return envelope, nil
}

func recoveryWorker(config Config, consumer string) (*valkeystream.Worker, error) {
	worker, err := valkeystream.NewWorkerE(
		valkeystream.WithAddress(config.ValkeyAddress),
		valkeystream.WithStreamName(config.Stream), valkeystream.WithGroup("recovery-assurance"),
		valkeystream.WithConsumer(consumer), valkeystream.WithMaxLength(128),
		valkeystream.WithRequestTimeout(5*time.Second),
		valkeystream.WithReclaim(100*time.Millisecond, 100*time.Millisecond, 16),
	)
	if err != nil {
		return nil, fmt.Errorf("reference durability: create %s recovery worker: %w", consumer, err)
	}
	return worker, nil
}
