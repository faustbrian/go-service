package referencedurability_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	postgres "github.com/faustbrian/go-postgres/v2"
	referencedurability "github.com/faustbrian/go-service/integration/reference-durability"
)

func TestPostgresV2CallersResolveConfigurationPrivately(t *testing.T) {
	t.Parallel()

	config := completeRecoveryConfig()
	config.DatabaseURL = "invalid-configuration-marker"
	for name, call := range map[string]func(context.Context) error{
		"run": func(ctx context.Context) error {
			_, err := referencedurability.Run(ctx, config)
			return err
		},
		"recovery": func(ctx context.Context) error {
			_, err := referencedurability.PrepareRecovery(ctx, config)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call(context.Background())
			var invalid *postgres.ConfigError
			if !errors.As(err, &invalid) || invalid.Field != "resolver" ||
				invalid.Problem != "could not resolve configuration" {
				t.Fatalf("configuration error = %v, want private resolver failure", err)
			}
			if invalid.Cause != nil || strings.Contains(err.Error(), config.DatabaseURL) {
				t.Fatal("configuration failure disclosed resolver input or cause")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled caller error = %v, want context.Canceled", err)
			}
		})
	}
}
