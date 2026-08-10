package healthhttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/healthhttp"
)

func TestSustainedBackendOutageWithdrawsReadinessWithoutRestartLoop(t *testing.T) {
	t.Parallel()

	var starts atomic.Int32
	var stops atomic.Int32
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "http-server",
		Start: func(context.Context) error {
			starts.Add(1)

			return nil
		},
		Stop: func(context.Context) error {
			stops.Add(1)

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	var backendOutage atomic.Bool
	var readinessChecks atomic.Int32
	backendUnavailable := errors.New("backend unavailable")
	probes, err := healthhttp.New(healthhttp.Config{
		Lifecycle: runtime,
		Checks: []healthhttp.Check{{
			Name: "backend",
			Run: func(context.Context) error {
				readinessChecks.Add(1)
				if backendOutage.Load() {
					return backendUnavailable
				}

				return nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("healthhttp.New() error = %v", err)
	}

	liveness := probes.Liveness()
	readiness := probes.Readiness()
	if status := resilienceProbeStatus(liveness, "/livez"); status != http.StatusOK {
		t.Fatalf("initial liveness status = %d, want 200", status)
	}
	if status := resilienceProbeStatus(readiness, "/readyz"); status != http.StatusOK {
		t.Fatalf("initial readiness status = %d, want 200", status)
	}

	backendOutage.Store(true)
	const outageProbeCycles = 8
	simulatedRestarts := 0
	for cycle := range outageProbeCycles {
		if status := resilienceProbeStatus(liveness, "/livez"); status != http.StatusOK {
			simulatedRestarts++
			t.Errorf("outage cycle %d liveness status = %d, want 200", cycle, status)
		}
		if status := resilienceProbeStatus(readiness, "/readyz"); status != http.StatusServiceUnavailable {
			t.Fatalf("outage cycle %d readiness status = %d, want 503", cycle, status)
		}
	}

	if simulatedRestarts != 0 {
		t.Fatalf("simulated liveness-triggered restarts = %d, want 0", simulatedRestarts)
	}
	if got := readinessChecks.Load(); got != outageProbeCycles+1 {
		t.Fatalf("readiness checks = %d, want %d; liveness must not call the backend", got, outageProbeCycles+1)
	}
	if state := runtime.State(); state != service.StateReady {
		t.Fatalf("service state during backend outage = %v, want %v", state, service.StateReady)
	}
	if !runtime.Ready() {
		t.Fatal("service lifecycle readiness was withdrawn by a dependency probe")
	}
	if cause := context.Cause(runtime.Context()); cause != nil {
		t.Fatalf("service lifetime canceled by backend outage: %v", cause)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("service starts during backend outage = %d, want 1", got)
	}
	if got := stops.Load(); got != 0 {
		t.Fatalf("service stops during backend outage = %d, want 0", got)
	}

	backendOutage.Store(false)
	if status := resilienceProbeStatus(readiness, "/readyz"); status != http.StatusOK {
		t.Fatalf("recovered readiness status = %d, want 200", status)
	}
	if status := resilienceProbeStatus(liveness, "/livez"); status != http.StatusOK {
		t.Fatalf("recovered liveness status = %d, want 200", status)
	}
	if got := readinessChecks.Load(); got != outageProbeCycles+2 {
		t.Fatalf("readiness checks after recovery = %d, want %d", got, outageProbeCycles+2)
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if state := runtime.State(); state != service.StateStopped {
		t.Fatalf("service state after shutdown = %v, want %v", state, service.StateStopped)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("service stops after shutdown = %d, want 1", got)
	}
}

func resilienceProbeStatus(handler http.Handler, path string) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

	return recorder.Code
}
