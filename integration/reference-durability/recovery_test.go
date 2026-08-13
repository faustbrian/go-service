package referencedurability_test

import (
	"context"
	"errors"
	"testing"

	referencedurability "github.com/faustbrian/golib/pkg/service/integration/reference-durability"
)

func TestRecoveryRejectsMissingContext(t *testing.T) {
	t.Parallel()

	var missing context.Context
	config := completeRecoveryConfig()
	if _, err := referencedurability.PrepareRecovery(missing, config); !errors.Is(err, referencedurability.ErrContextRequired) {
		t.Fatalf("PrepareRecovery() error = %v, want ErrContextRequired", err)
	}
	if _, err := referencedurability.Recover(missing, config, referencedurability.RecoveryExpectation{}); !errors.Is(err, referencedurability.ErrContextRequired) {
		t.Fatalf("Recover() error = %v, want ErrContextRequired", err)
	}
	if err := referencedurability.VerifyRecoveryAcknowledgement(missing, config); !errors.Is(err, referencedurability.ErrContextRequired) {
		t.Fatalf("VerifyRecoveryAcknowledgement() error = %v, want ErrContextRequired", err)
	}
}

func TestRecoveryRejectsInvalidInputsBeforeConnecting(t *testing.T) {
	t.Parallel()

	if _, err := referencedurability.PrepareRecovery(context.Background(), referencedurability.Config{}); !errors.Is(err, referencedurability.ErrInvalidConfig) {
		t.Fatalf("PrepareRecovery() error = %v, want ErrInvalidConfig", err)
	}
	if _, err := referencedurability.Recover(
		context.Background(), completeRecoveryConfig(), referencedurability.RecoveryExpectation{},
	); !errors.Is(err, referencedurability.ErrInvalidRecoveryExpectation) {
		t.Fatalf("Recover() error = %v, want ErrInvalidRecoveryExpectation", err)
	}
	if err := referencedurability.VerifyRecoveryAcknowledgement(context.Background(), referencedurability.Config{}); !errors.Is(err, referencedurability.ErrInvalidConfig) {
		t.Fatalf("VerifyRecoveryAcknowledgement() error = %v, want ErrInvalidConfig", err)
	}
}

func completeRecoveryConfig() referencedurability.Config {
	return referencedurability.Config{
		DatabaseURL:   "postgres://reference",
		ValkeyAddress: "127.0.0.1:6379",
		Stream:        "reference-recovery",
	}
}
