package healthhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/faustbrian/golib/pkg/service/healthhttp"
	"github.com/faustbrian/golib/pkg/service/service"
)

type stateSource struct {
	state atomic.Uint32
}

func (source *stateSource) State() service.State {
	return service.State(source.state.Load())
}

func (source *stateSource) set(state service.State) {
	source.state.Store(uint32(state))
}

func TestProbeHandlersFollowLifecycleAndHideCheckErrors(t *testing.T) {
	t.Parallel()

	source := &stateSource{}
	checkFailure := errors.New("secret database address")
	probes, err := healthhttp.New(healthhttp.Config{
		Lifecycle: source,
		Checks: []healthhttp.Check{{
			Name: "database",
			Run: func(context.Context) error {
				return checkFailure
			},
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	assertProbe(t, probes.Liveness(), http.StatusOK, healthhttp.Response{
		Status: "ok",
		Probe:  "liveness",
	})
	assertProbe(t, probes.Startup(), http.StatusServiceUnavailable, healthhttp.Response{
		Status: "unavailable",
		Probe:  "startup",
	})
	assertProbe(t, probes.Readiness(), http.StatusServiceUnavailable, healthhttp.Response{
		Status: "unavailable",
		Probe:  "readiness",
	})

	source.set(service.StateReady)
	assertProbe(t, probes.Startup(), http.StatusOK, healthhttp.Response{
		Status: "ok",
		Probe:  "startup",
	})
	assertProbe(t, probes.Readiness(), http.StatusServiceUnavailable, healthhttp.Response{
		Status: "unavailable",
		Probe:  "readiness",
	})

	source.set(service.StateDraining)
	assertProbe(t, probes.Startup(), http.StatusOK, healthhttp.Response{
		Status: "ok",
		Probe:  "startup",
	})
	assertProbe(t, probes.Readiness(), http.StatusServiceUnavailable, healthhttp.Response{
		Status: "unavailable",
		Probe:  "readiness",
	})

	for _, state := range []service.State{service.StateStopping, service.StateStopped} {
		source.set(state)
		assertProbe(t, probes.Startup(), http.StatusServiceUnavailable, healthhttp.Response{
			Status: "unavailable",
			Probe:  "startup",
		})
	}
}

func TestStandaloneStartupAndConfigurationError(t *testing.T) {
	t.Parallel()

	probes, err := healthhttp.New(healthhttp.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	assertProbe(t, probes.Startup(), http.StatusOK, healthhttp.Response{
		Status: "ok",
		Probe:  "startup",
	})
	configError := &healthhttp.ConfigError{Field: "field", Reason: "reason"}
	if configError.Error() == "" {
		t.Fatal("ConfigError.Error() is blank")
	}
}

func TestStartupProbeRejectsFailedLifecycle(t *testing.T) {
	t.Parallel()

	startFailure := errors.New("startup failed")
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "failing",
		Start: func(context.Context) error {
			return startFailure
		},
	}}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, startFailure) {
		t.Fatalf("Start() error = %v, want startup failure", err)
	}
	probes, err := healthhttp.New(healthhttp.Config{Lifecycle: runtime})
	if err != nil {
		t.Fatalf("healthhttp.New() error = %v", err)
	}
	assertProbe(t, probes.Startup(), http.StatusServiceUnavailable, healthhttp.Response{
		Status: "unavailable",
		Probe:  "startup",
	})
}

func assertProbe(
	t *testing.T,
	handler http.Handler,
	wantStatus int,
	wantBody healthhttp.Response,
) {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var response healthhttp.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON error = %v", err)
	}
	if !reflect.DeepEqual(response, wantBody) {
		t.Fatalf("response = %#v, want %#v", response, wantBody)
	}
}
