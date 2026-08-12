package referencedurability_test

import (
	"context"
	"errors"
	"testing"

	referencedurability "github.com/faustbrian/golib/pkg/service/integration/reference-durability"
)

func TestRunRejectsIncompleteConfiguration(t *testing.T) {
	t.Parallel()

	tests := map[string]referencedurability.Config{
		"database URL":   {ValkeyAddress: "127.0.0.1:6379", Stream: "reference"},
		"Valkey address": {DatabaseURL: "postgres://reference", Stream: "reference"},
		"stream":         {DatabaseURL: "postgres://reference", ValkeyAddress: "127.0.0.1:6379"},
	}
	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := referencedurability.Run(context.Background(), config)
			if !errors.Is(err, referencedurability.ErrInvalidConfig) {
				t.Fatalf("Run() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestRunRejectsNilContext(t *testing.T) {
	t.Parallel()

	var missing context.Context
	_, err := referencedurability.Run(missing, referencedurability.Config{
		DatabaseURL:   "postgres://reference",
		ValkeyAddress: "127.0.0.1:6379",
		Stream:        "reference",
	})
	if !errors.Is(err, referencedurability.ErrContextRequired) {
		t.Fatalf("Run() error = %v, want ErrContextRequired", err)
	}
}
