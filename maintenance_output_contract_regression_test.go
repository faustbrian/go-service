package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	service "github.com/faustbrian/go-service/v3"
)

func TestMaintenanceCommandsKeepPublishedBypassCredentialOffCommandStreams(t *testing.T) {
	var stored service.MaintenanceState
	var storeFailure error
	store, err := service.NewSharedMaintenanceStore(service.MaintenanceStoreOperations{
		Load: func(context.Context) (service.MaintenanceState, error) { return stored, nil },
		Store: func(_ context.Context, next service.MaintenanceState) error {
			if storeFailure != nil {
				return storeFailure
			}
			stored = next
			return nil
		},
		Clear: func(context.Context) error { stored = service.MaintenanceState{}; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	definition := service.Definition{
		Identity: service.Identity{Name: "synthetic-maintenance"},
		Commands: service.Commands{Migrate: service.CommandFor(service.CommandSpec[struct{}]{
			Name: "migrate", Kind: service.CommandKindOneShot,
			Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
			Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
				return service.Plan{}, nil
			},
		})},
		Maintenance: service.Maintenance{Store: store},
	}
	for _, args := range [][]string{{"down", "--secret=synthetic-test-marker"}, {"down", "--with-secret"}} {
		var stdout, stderr bytes.Buffer
		if exit := service.Execute(t.Context(), definition, service.Invocation{Args: args, Stdout: &stdout, Stderr: &stderr}); exit != 0 {
			t.Fatalf("synthetic maintenance command failed with exit %d", exit)
		}
		if !stored.Enabled || stored.Secret == "" {
			t.Fatal("successful command did not publish bypass-enabled state")
		}
		retrieved, err := store.LoadMaintenance(t.Context())
		if err != nil || retrieved.Secret != stored.Secret {
			t.Fatal("caller-owned secure retrieval lost published credential")
		}
		if strings.Contains(stdout.String(), stored.Secret) || strings.Contains(stderr.String(), stored.Secret) {
			t.Error("command stream disclosed the test bypass credential")
		}
		stdout.Reset()
		stderr.Reset()
		if exit := service.Execute(t.Context(), definition, service.Invocation{Args: []string{"status"}, Stdout: &stdout, Stderr: &stderr}); exit != 0 {
			t.Fatalf("status failed with exit %d", exit)
		}
		if !strings.Contains(stdout.String(), `"bypass_configured":true`) || strings.Contains(stdout.String(), stored.Secret) || strings.Contains(stderr.String(), stored.Secret) {
			t.Error("status lost safe configured-state reporting")
		}
	}
	before := stored
	for _, args := range [][]string{{"down", "--secret=synthetic-test-marker", "--with-secret"}, {"down", "--retry=-1s"}} {
		var stdout, stderr bytes.Buffer
		exit := service.Execute(t.Context(), definition, service.Invocation{Args: args, Stdout: &stdout, Stderr: &stderr})
		if exit == 0 || stored != before || stdout.Len() != 0 || strings.Contains(stderr.String(), before.Secret) {
			t.Error("invalid options altered state or disclosed credential")
		}
	}
	storeFailure = errors.New("synthetic-private-store-error")
	var stdout, stderr bytes.Buffer
	exit := service.Execute(t.Context(), definition, service.Invocation{Args: []string{"down", "--with-secret"}, Stdout: &stdout, Stderr: &stderr})
	if exit != 75 || stored != before || stdout.Len() != 0 || strings.Contains(stderr.String(), storeFailure.Error()) {
		t.Error("failed publication altered state, printed success, or disclosed backend detail")
	}
}
