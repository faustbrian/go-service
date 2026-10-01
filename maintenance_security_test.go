package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	service "github.com/faustbrian/go-service"
)

func TestFileMaintenanceStoreValidatesSecondsBeforeConversion(t *testing.T) {
	for _, field := range []string{"retry_after_seconds", "refresh_seconds"} {
		for _, test := range []struct {
			name    string
			seconds int64
			valid   bool
		}{
			{"zero", 0, true},
			{"exact_seven_days", 604800, true},
			{"one_second_over", 604801, false},
			{"negative", -1, false},
			{"conversion_overflow", 18446744074, false},
		} {
			t.Run(field+"/"+test.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "maintenance.json")
				data := []byte(fmt.Sprintf(`{"enabled":true,"since":"2026-10-01T00:00:00Z",%q:%d}`, field, test.seconds))
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				store, err := service.NewFileMaintenanceStore(path)
				if err != nil {
					t.Fatal(err)
				}
				state, err := store.LoadMaintenance(t.Context())
				if !test.valid {
					if err == nil || state != (service.MaintenanceState{}) {
						t.Fatalf("invalid persisted duration accepted: state=%#v, err=%v", state, err)
					}
					return
				}
				if err != nil || !state.Enabled {
					t.Fatalf("valid persisted state rejected: %v", err)
				}
				got := state.RetryAfter
				if field == "refresh_seconds" {
					got = state.Refresh
				}
				if want := time.Duration(test.seconds) * time.Second; got != want {
					t.Fatalf("duration=%v, want %v", got, want)
				}
			})
		}
	}
}

func TestFileMaintenanceStoreCanceledLoadReturnsEmptyState(t *testing.T) {
	store, err := service.NewFileMaintenanceStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, err := store.LoadMaintenance(ctx)
	if !errors.Is(err, context.Canceled) || state != (service.MaintenanceState{}) {
		t.Fatalf("canceled load: state=%#v, err=%v", state, err)
	}
}

func TestFileMaintenanceStoreConcurrentReadAndPublicationRemainCoherent(t *testing.T) {
	store, err := service.NewFileMaintenanceStore(filepath.Join(t.TempDir(), "maintenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	state := service.MaintenanceState{Enabled: true, Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	if err := store.StoreMaintenance(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 2)
	workers.Go(func() {
		for i := range 8 {
			state.RetryAfter = time.Duration(i) * time.Second
			state.Refresh = state.RetryAfter
			if err := store.StoreMaintenance(t.Context(), state); err != nil {
				failures <- err
				return
			}
		}
	})
	workers.Go(func() {
		for range 8 {
			loaded, err := store.LoadMaintenance(t.Context())
			if err != nil {
				failures <- err
				return
			}
			if !loaded.Enabled || loaded.Since.IsZero() || loaded.RetryAfter != loaded.Refresh {
				failures <- errors.New("incoherent maintenance snapshot")
				return
			}
		}
	})
	workers.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
