package service_test

import (
	"context"
	"errors"
	"os"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/service"
)

func TestRunWithSignalsPreservesSignalCauseAndShutdownBound(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	stopCause := make(chan error, 1)
	stopHasDeadline := make(chan bool, 1)
	stopContext := make(chan context.Context, 1)
	var runtime *service.Service
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Start: func(context.Context) error {
			close(started)

			return nil
		},
		Stop: func(ctx context.Context) error {
			stopContext <- ctx
			stopCause <- context.Cause(runtime.Context())
			_, hasDeadline := ctx.Deadline()
			stopHasDeadline <- hasDeadline

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	signals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- service.RunWithSignals(
			context.Background(),
			runtime,
			time.Second,
			signals,
		)
	}()
	<-started
	signals <- os.Interrupt

	if err := <-result; err != nil {
		t.Fatalf("RunWithSignals() error = %v", err)
	}
	var signalError *service.SignalError
	if cause := <-stopCause; !errors.As(cause, &signalError) {
		t.Fatalf("service cause = %v, want SignalError", cause)
	}
	if signalError.Signal != os.Interrupt {
		t.Fatalf("SignalError.Signal = %v, want os.Interrupt", signalError.Signal)
	}
	if !errors.Is(signalError, service.ErrSignal) {
		t.Fatalf("SignalError = %v, want ErrSignal", signalError)
	}
	if !<-stopHasDeadline {
		t.Fatal("shutdown context has no deadline")
	}
	select {
	case <-(<-stopContext).Done():
	default:
		t.Fatal("completed shutdown retained its timeout timer")
	}
}

func TestRunWithSignalsHandlesSignalStormOnce(t *testing.T) {
	t.Parallel()

	var stopCalls atomic.Int32
	started := make(chan struct{})
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Start: func(context.Context) error {
			close(started)

			return nil
		},
		Stop: func(context.Context) error {
			stopCalls.Add(1)

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	signals := make(chan os.Signal, 3)
	result := make(chan error, 1)
	go func() {
		result <- service.RunWithSignals(context.Background(), runtime, time.Second, signals)
	}()
	<-started
	signals <- os.Interrupt
	signals <- os.Interrupt
	signals <- os.Interrupt
	if err := <-result; err != nil {
		t.Fatalf("RunWithSignals() error = %v", err)
	}
	if calls := stopCalls.Load(); calls != 1 {
		t.Fatalf("stop calls = %d, want 1", calls)
	}
}

func TestRunStopsWhenParentIsCanceled(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Start: func(context.Context) error {
			close(started)

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- service.Run(ctx, runtime, service.RunConfig{
			ShutdownTimeout: time.Second,
		})
	}()
	<-started
	for !runtime.Ready() {
		goruntime.Gosched()
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunValidationAndClosedSignalChannel(t *testing.T) {
	t.Parallel()

	validService, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	validSignals := make(chan os.Signal)
	tests := map[string]func() error{
		"nil run context": func() error {
			//lint:ignore SA1012 Public boundary must reject nil context safely.
			//nolint:staticcheck // This test verifies explicit nil rejection.
			return service.Run(nil, validService, service.RunConfig{})
		},
		"nil service": func() error {
			return service.Run(context.Background(), nil, service.RunConfig{})
		},
		"negative timeout": func() error {
			return service.Run(context.Background(), validService, service.RunConfig{
				ShutdownTimeout: -time.Second,
			})
		},
		"nil signal channel": func() error {
			return service.RunWithSignals(
				context.Background(),
				validService,
				time.Second,
				nil,
			)
		},
		"invalid signal runner timeout": func() error {
			return service.RunWithSignals(
				context.Background(),
				validService,
				-time.Second,
				validSignals,
			)
		},
		"nil owned signal": func() error {
			runtime, runtimeErr := service.New(service.Config{})
			if runtimeErr != nil {
				return runtimeErr
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			return service.Run(ctx, runtime, service.RunConfig{
				Signals: []os.Signal{nil},
			})
		},
	}
	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); !errors.Is(err, service.ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	closedSignals := make(chan os.Signal)
	close(closedSignals)
	closedService, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := service.RunWithSignals(
		context.Background(),
		closedService,
		0,
		closedSignals,
	); err != nil {
		t.Fatalf("RunWithSignals() error = %v", err)
	}
	if cause := context.Cause(closedService.Context()); !errors.Is(cause, service.ErrSignal) {
		t.Fatalf("service cause = %v, want ErrSignal", cause)
	}

	startFailure := errors.New("start failed")
	failingService, err := service.New(service.Config{Components: []service.Component{{
		Name:  "failing",
		Start: func(context.Context) error { return startFailure },
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := service.RunWithSignals(
		context.Background(),
		failingService,
		time.Second,
		validSignals,
	); !errors.Is(err, startFailure) {
		t.Fatalf("RunWithSignals() error = %v, want start failure", err)
	}

	signalError := &service.SignalError{Signal: os.Interrupt}
	if got := signalError.Error(); got == "" {
		t.Fatal("SignalError.Error() is blank")
	}
}

func TestWaitWithSignalsStopsAlreadyStartedService(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	taskCanceled := make(chan struct{})
	if err := runtime.Go("worker", func(ctx context.Context) error {
		<-ctx.Done()
		close(taskCanceled)

		return nil
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	signals := make(chan os.Signal, 1)
	signals <- os.Interrupt
	if err := service.WaitWithSignals(
		context.Background(),
		runtime,
		time.Second,
		signals,
	); err != nil {
		t.Fatalf("WaitWithSignals() error = %v", err)
	}
	<-taskCanceled
}

func TestWaitWithSignalsStopsAfterSupervisedTaskFailure(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	taskFailure := errors.New("worker failed")
	releaseTask := make(chan struct{})
	if err := runtime.Go("worker", func(context.Context) error {
		<-releaseTask

		return taskFailure
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- service.WaitWithSignals(
			context.Background(),
			runtime,
			time.Second,
			make(chan os.Signal),
		)
	}()
	close(releaseTask)

	if err := <-waitResult; !errors.Is(err, taskFailure) {
		t.Fatalf("WaitWithSignals() error = %v, want task failure", err)
	}
	if state := runtime.State(); state != service.StateStopped {
		t.Fatalf("State() = %v, want stopped", state)
	}
}

func TestWaitOwnedSignalsAndValidation(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Wait(ctx, runtime, service.RunConfig{}); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if cause := context.Cause(runtime.Context()); !errors.Is(cause, context.Canceled) {
		t.Fatalf("service cause = %v, want context.Canceled", cause)
	}
	//lint:ignore SA1012 Public boundary must reject nil context safely.
	//nolint:staticcheck // This test verifies the documented nil rejection.
	if err := service.Wait(nil, runtime, service.RunConfig{}); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Wait(nil) error = %v, want ErrInvalidConfig", err)
	}

	newRuntime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	signals := make(chan os.Signal)
	if err := service.WaitWithSignals(
		context.Background(),
		newRuntime,
		-time.Second,
		signals,
	); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("WaitWithSignals() timeout error = %v", err)
	}
	if err := service.Wait(context.Background(), newRuntime, service.RunConfig{
		Signals: []os.Signal{nil},
	}); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Wait() nil owned signal error = %v, want ErrInvalidConfig", err)
	}
	if err := service.WaitWithSignals(
		context.Background(),
		newRuntime,
		time.Second,
		signals,
	); !errors.Is(err, service.ErrInvalidState) {
		t.Fatalf("WaitWithSignals() state error = %v, want ErrInvalidState", err)
	}
	if err := newRuntime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := service.WaitWithSignals(
		context.Background(),
		newRuntime,
		time.Second,
		nil,
	); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("WaitWithSignals() nil signals error = %v", err)
	}
	closedSignals := make(chan os.Signal)
	close(closedSignals)
	if err := service.WaitWithSignals(
		context.Background(),
		newRuntime,
		0,
		closedSignals,
	); err != nil {
		t.Fatalf("WaitWithSignals() zero timeout error = %v", err)
	}

	drainingRuntime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := drainingRuntime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := drainingRuntime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	drainingSignals := make(chan os.Signal)
	close(drainingSignals)
	if err := service.WaitWithSignals(
		context.Background(),
		drainingRuntime,
		time.Second,
		drainingSignals,
	); err != nil {
		t.Fatalf("WaitWithSignals() draining error = %v", err)
	}
}
