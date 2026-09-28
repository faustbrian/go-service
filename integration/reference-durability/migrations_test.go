package referencedurability

import (
	"context"
	"testing"

	idempotencyv2 "github.com/faustbrian/go-idempotency/v2"
	idempotencyv2postgres "github.com/faustbrian/go-idempotency/v2/postgres"
	"github.com/faustbrian/go-migrations/v2"
	golibpostgres "github.com/faustbrian/go-postgres"
	outbox "github.com/faustbrian/go-transactional-outbox"
	outboxpostgres "github.com/faustbrian/go-transactional-outbox/postgres"
	"github.com/jackc/pgx/v5"
)

var (
	_ func(context.Context, pgx.Tx, *outboxpostgres.Writer, outbox.Envelope, *idempotencyv2postgres.Store, idempotencyv2.Ownership) error = stageCommand
	_ func(*golibpostgres.Pool) (*idempotencyv2postgres.Store, idempotencyv2.Key, idempotencyv2.Fingerprint, error)                       = recoveryIdempotency
)

func TestIdempotencyV2MigrationPreservesComposedSchema(t *testing.T) {
	t.Parallel()

	var published migrations.Migration
	var err error
	published, err = idempotencyv2postgres.GoMigration()
	if err != nil {
		t.Fatalf("GoMigration() error = %v", err)
	}
	const upSQL = `CREATE TABLE idempotency_records (
    record_key bytea PRIMARY KEY,
    record jsonb NOT NULL,
    purge_at timestamptz NOT NULL
);
CREATE INDEX idempotency_records_purge_at_idx
    ON idempotency_records (purge_at);`
	const downSQL = `DROP TABLE idempotency_records;`
	if published.Version() != 1 || published.Name() != "create_idempotency_records" ||
		published.TransactionMode() != migrations.TransactionModeDefault ||
		published.UpSQL() != upSQL || published.DownSQL() != downSQL {
		t.Fatalf("published v2 migration changed: version=%d name=%q mode=%q up=%q down=%q",
			published.Version(), published.Name(), published.TransactionMode(), published.UpSQL(), published.DownSQL())
	}
	sequence, err := composedMigrations(context.Background())
	if err != nil {
		t.Fatalf("composedMigrations() error = %v", err)
	}
	if len(sequence) != 3 {
		t.Fatalf("composed migration count = %d, want 3", len(sequence))
	}
	got := sequence[1]
	if got.Version() != 2 || got.Name() != published.Name() ||
		got.TransactionMode() != published.TransactionMode() ||
		got.UpSQL() != upSQL || got.DownSQL() != downSQL {
		t.Fatalf("composed v2 migration changed: version=%d name=%q mode=%q up=%q down=%q",
			got.Version(), got.Name(), got.TransactionMode(), got.UpSQL(), got.DownSQL())
	}
}

func TestComposedMigrationsPreserveOwnedSchema(t *testing.T) {
	t.Parallel()

	sequence, err := composedMigrations(context.Background())
	if err != nil {
		t.Fatalf("composedMigrations() error = %v", err)
	}
	wantNames := []string{
		"create_outbox",
		"create_idempotency_records",
		"create_reference_commands",
	}
	if len(sequence) != len(wantNames) {
		t.Fatalf("migration count = %d, want %d", len(sequence), len(wantNames))
	}
	for index, migration := range sequence {
		if migration.Version() != migrations.Version(index+1) || migration.Name() != wantNames[index] {
			t.Errorf("migration %d identity = %d/%q, want %d/%q", index, migration.Version(), migration.Name(), index+1, wantNames[index])
		}
		if migration.UpSQL() == "" || migration.DownSQL() == "" {
			t.Errorf("migration %d missing reversible SQL", index)
		}
	}
}
