package service

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestConfiguredSignalsSelectsDefaultsOrCopiesExplicitValues(t *testing.T) {
	t.Parallel()

	if defaults := configuredSignals(nil); len(defaults) == 0 {
		t.Fatal("configuredSignals(nil) returned no platform defaults")
	}

	explicit := []os.Signal{os.Interrupt}
	configured := configuredSignals(explicit)
	explicit[0] = nil
	if len(configured) != 1 || configured[0] != os.Interrupt {
		t.Fatalf("configuredSignals(explicit) = %v, want copied interrupt", configured)
	}
}

func TestCancelWithCauseWithdrawsReadinessOnlyFromReady(t *testing.T) {
	t.Parallel()

	cause := errors.New("runtime failure")
	ready, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := ready.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ready.cancelWithCause(cause)
	if state := ready.State(); state != StateDraining {
		t.Fatalf("ready service state = %v, want draining", state)
	}
	if actual := context.Cause(ready.Context()); !errors.Is(actual, cause) {
		t.Fatalf("ready service cause = %v, want %v", actual, cause)
	}

	unstarted, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	unstarted.cancelWithCause(cause)
	if state := unstarted.State(); state != StateNew {
		t.Fatalf("unstarted service state = %v, want new", state)
	}
}
