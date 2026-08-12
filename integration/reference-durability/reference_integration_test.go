//go:build integration

package referencedurability_test

import (
	"context"
	"os"
	"testing"
	"time"

	referencedurability "github.com/faustbrian/golib/pkg/service/integration/reference-durability"
)

func TestPostgresAndValkeyDurabilityComposition(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	valkeyAddress := os.Getenv("VALKEY_ADDRESS")
	if databaseURL == "" || valkeyAddress == "" {
		t.Fatal("DATABASE_URL and VALKEY_ADDRESS are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := referencedurability.Run(ctx, referencedurability.Config{
		DatabaseURL: databaseURL, ValkeyAddress: valkeyAddress,
		Stream: "reference-durability",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FirstOutcome != "acquired" || result.ReplayOutcome != "replayed" ||
		result.BusinessRows != 1 || result.OutboxState != "delivered" ||
		result.TaskID == "" || result.TaskKey != "reference-command-1" ||
		!result.Redelivered || !result.RollbackIsolated {
		t.Fatalf("Run() result = %#v", result)
	}
}
