package service

import (
	"context"
	"testing"
	"time"
)

func TestDefaultStartupAndAbsentSignalsCompleteWithoutWaiting(t *testing.T) {
	parent := context.Background()
	signals := coordinateCommandSignals(parent, nil)
	defer signals.stop()
	if signals.context != parent {
		t.Fatal("absent signals changed the caller context")
	}

	starts := 0
	startedAt := time.Now()
	runtime, err := New(Config{Components: []Component{{
		Name: "ready",
		Start: func(ctx context.Context) error {
			starts++
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("default startup has no deadline")
			}
			remaining := deadline.Sub(startedAt)
			if remaining < 29*time.Second || remaining > 31*time.Second {
				t.Fatalf("default startup deadline remaining = %s, want about 30s", remaining)
			}
			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(parent); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if starts != 1 || runtime.State() != StateReady {
		t.Fatalf("successful component startup = (%d, %s), want (1, ready)", starts, runtime.State())
	}
	if err := runtime.Shutdown(parent); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
