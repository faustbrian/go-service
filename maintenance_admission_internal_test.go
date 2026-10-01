package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceSnapshotAdmissionPreservesActiveState(t *testing.T) {
	data := acquireOrdinaryMaintenanceSnapshot(t)
	state, err := parseMaintenanceSnapshot(t.Context(), data, nil)
	want := MaintenanceState{
		Enabled:    true,
		Since:      time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
		RetryAfter: time.Second,
		Refresh:    2 * time.Second,
		Redirect:   "/maintenance",
	}
	if err != nil || state != want {
		t.Fatalf("admission = %#v, %v; want %#v", state, err, want)
	}
}

func TestMaintenanceSnapshotAdmissionCancellationWins(t *testing.T) {
	data := acquireOrdinaryMaintenanceSnapshot(t)
	readFailure := errors.New("ordinary read failure")
	for _, test := range []struct {
		name    string
		data    []byte
		readErr error
	}{
		{"valid snapshot", data, nil},
		{"invalid snapshot", []byte("{"), nil},
		{"read failure", nil, readFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("ordinary operation canceled")
			cancel(cause)
			state, err := parseMaintenanceSnapshot(ctx, test.data, test.readErr)
			if state != (MaintenanceState{}) || !errors.Is(err, cause) || errors.Is(err, readFailure) {
				t.Fatalf("canceled admission = %#v, %v; want empty state and cancellation cause", state, err)
			}
		})
	}
}

func acquireOrdinaryMaintenanceSnapshot(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maintenance.json")
	snapshot := []byte(`{"enabled":true,"since":"2026-10-01T00:00:00Z","retry_after_seconds":1,"refresh_seconds":2,"redirect":"/maintenance"}`)
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := readMaintenanceSnapshot(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("snapshot acquisition: read=%v close=%v", readErr, closeErr)
	}
	return data
}
