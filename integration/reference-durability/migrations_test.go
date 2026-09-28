package referencedurability

import (
	"context"
	"testing"

	"github.com/faustbrian/go-migrations/v2"
)

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
