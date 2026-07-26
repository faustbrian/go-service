package servicetest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/faustbrian/golib/pkg/service/service"
	"github.com/faustbrian/golib/pkg/service/servicetest"
)

func TestBarrierSupportsReleaseAndCancellationWithoutSleeps(t *testing.T) {
	t.Parallel()

	var barrier servicetest.Barrier
	result := make(chan error, 1)
	go func() { result <- barrier.Wait(context.Background()) }()
	<-barrier.Entered()
	barrier.Release()
	barrier.Release()
	if err := <-result; err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	var canceled servicetest.Barrier
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("stop waiting")
	waitResult := make(chan error, 1)
	go func() { waitResult <- canceled.Wait(ctx) }()
	<-canceled.Entered()
	cancel(cause)
	if err := <-waitResult; !errors.Is(err, cause) {
		t.Fatalf("Wait() error = %v, want cause", err)
	}
}

func TestBarrierSupportsConcurrentFirstWaiters(t *testing.T) {
	t.Parallel()

	var barrier servicetest.Barrier
	results := make(chan error, 16)
	for range 16 {
		go func() { results <- barrier.Wait(context.Background()) }()
	}
	<-barrier.Entered()
	barrier.Release()
	for range 16 {
		if err := <-results; err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}
}

func TestControlledComponentRecordsAndInjectsFailures(t *testing.T) {
	t.Parallel()

	startFailure := errors.New("start failed")
	stopFailure := errors.New("stop failed")
	recorder := &servicetest.Recorder{}
	startBarrier := &servicetest.Barrier{}
	stopBarrier := &servicetest.Barrier{}
	component, err := servicetest.NewComponent(servicetest.ComponentConfig{
		Name:         "worker",
		Recorder:     recorder,
		StartBarrier: startBarrier,
		StopBarrier:  stopBarrier,
		StartError:   startFailure,
		StopError:    stopFailure,
	})
	if err != nil {
		t.Fatalf("NewComponent() error = %v", err)
	}

	startResult := make(chan error, 1)
	go func() { startResult <- component.Start(context.Background()) }()
	<-startBarrier.Entered()
	startBarrier.Release()
	if err := <-startResult; !errors.Is(err, startFailure) {
		t.Fatalf("Start() error = %v, want start failure", err)
	}

	stopResult := make(chan error, 1)
	go func() { stopResult <- component.Stop(context.Background()) }()
	<-stopBarrier.Entered()
	stopBarrier.Release()
	if err := <-stopResult; !errors.Is(err, stopFailure) {
		t.Fatalf("Stop() error = %v, want stop failure", err)
	}
	want := []string{"start worker", "stop worker"}
	if events := recorder.Events(); !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}

	snapshot := recorder.Events()
	snapshot[0] = "changed"
	if recorder.Events()[0] != "start worker" {
		t.Fatal("Events() returned mutable recorder storage")
	}
}

func TestProbeCapturesBoundedResponse(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Probe", "value")
		writer.WriteHeader(http.StatusAccepted)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte("response"))
	})
	result, err := servicetest.Probe(
		handler,
		httptest.NewRequest(http.MethodGet, "/", nil),
		4,
	)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if result.Status != http.StatusAccepted ||
		result.Header.Get("X-Probe") != "value" ||
		string(result.Body) != "resp" ||
		!result.Truncated {
		t.Fatalf("Probe() result = %#v", result)
	}
}

func TestControlledComponentWorksInService(t *testing.T) {
	t.Parallel()

	component, err := servicetest.NewComponent(servicetest.ComponentConfig{Name: "worker"})
	if err != nil {
		t.Fatalf("NewComponent() error = %v", err)
	}
	runtime, err := service.New(service.Config{Components: []service.Component{component}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestUtilityValidationAndCanceledComponentBarriers(t *testing.T) {
	t.Parallel()

	var barrier servicetest.Barrier
	//lint:ignore SA1012 Public boundary must reject nil context safely.
	//nolint:staticcheck // This test verifies the documented nil rejection.
	if err := barrier.Wait(nil); !errors.Is(err, servicetest.ErrInvalidConfig) {
		t.Fatalf("Wait(nil) error = %v, want ErrInvalidConfig", err)
	}
	if _, err := servicetest.NewComponent(servicetest.ComponentConfig{}); !errors.Is(err, servicetest.ErrInvalidConfig) {
		t.Fatalf("NewComponent() error = %v, want ErrInvalidConfig", err)
	}

	startBarrier := &servicetest.Barrier{}
	stopBarrier := &servicetest.Barrier{}
	component, err := servicetest.NewComponent(servicetest.ComponentConfig{
		Name:         "worker",
		StartBarrier: startBarrier,
		StopBarrier:  stopBarrier,
	})
	if err != nil {
		t.Fatalf("NewComponent() error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := component.Start(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context.Canceled", err)
	}
	if err := component.Stop(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}

	configError := &servicetest.ConfigError{Field: "field", Reason: "reason"}
	if configError.Error() == "" || !errors.Is(configError, servicetest.ErrInvalidConfig) {
		t.Fatalf("ConfigError = %v", configError)
	}
}

func TestProbeValidationAndCompleteResponse(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for name, operation := range map[string]func() error{
		"nil handler": func() error {
			_, err := servicetest.Probe(nil, request, 1)

			return err
		},
		"nil request": func() error {
			_, err := servicetest.Probe(http.NotFoundHandler(), nil, 1)

			return err
		},
		"negative body": func() error {
			_, err := servicetest.Probe(http.NotFoundHandler(), request, -1)

			return err
		},
		"excessive body": func() error {
			_, err := servicetest.Probe(http.NotFoundHandler(), request, 16<<20+1)

			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); !errors.Is(err, servicetest.ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	result, err := servicetest.Probe(http.NotFoundHandler(), request, 1024)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if result.Truncated {
		t.Fatal("Probe() unexpectedly truncated response")
	}
}
