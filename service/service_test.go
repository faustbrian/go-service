package service_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/service"
)

func TestServiceStartsAndStopsComponentsInOwnershipOrder(t *testing.T) {
	t.Parallel()

	var events []string
	component := func(name string) service.Component {
		return service.Component{
			Name: name,
			Start: func(context.Context) error {
				events = append(events, "start "+name)

				return nil
			},
			Stop: func(context.Context) error {
				events = append(events, "stop "+name)

				return nil
			},
		}
	}

	runtime, err := service.New(service.Config{
		Components: []service.Component{component("listener"), component("worker")},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if state := runtime.State(); state != service.StateNew {
		t.Fatalf("initial state = %v, want %v", state, service.StateNew)
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if state := runtime.State(); state != service.StateReady {
		t.Fatalf("started state = %v, want %v", state, service.StateReady)
	}

	serviceContext := runtime.Context()
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if cause := context.Cause(serviceContext); !errors.Is(cause, service.ErrShutdown) {
		t.Fatalf("service context cause = %v, want ErrShutdown", cause)
	}
	if state := runtime.State(); state != service.StateStopped {
		t.Fatalf("shutdown state = %v, want %v", state, service.StateStopped)
	}

	want := []string{"start listener", "start worker", "stop worker", "stop listener"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := map[string]service.Config{
		"blank component name": {
			Components: []service.Component{{Start: func(context.Context) error { return nil }}},
		},
		"duplicate component name": {
			Components: []service.Component{{Name: "worker"}, {Name: "worker"}},
		},
		"negative rollback timeout": {
			RollbackTimeout: -time.Second,
		},
		"negative maximum tasks": {
			MaxTasks: -1,
		},
		"excessive maximum tasks": {
			MaxTasks: 4097,
		},
	}

	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime, err := service.New(config)
			if runtime != nil {
				t.Fatalf("New() runtime = %v, want nil", runtime)
			}
			if !errors.Is(err, service.ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}

			var configError *service.ConfigError
			if !errors.As(err, &configError) {
				t.Fatalf("New() error type = %T, want *ConfigError", err)
			}
			if strings.TrimSpace(configError.Field) == "" {
				t.Fatal("ConfigError.Field is blank")
			}
		})
	}
}

func TestStartRollsBackOwnedComponentsAndPreservesFailures(t *testing.T) {
	t.Parallel()

	startFailure := errors.New("dependency unavailable")
	rollbackFailure := errors.New("listener close failed")
	rollbackContext := make(chan context.Context, 1)
	var events []string
	runtime, err := service.New(service.Config{
		Components: []service.Component{
			{
				Name: "listener",
				Start: func(context.Context) error {
					events = append(events, "start listener")

					return nil
				},
				Stop: func(ctx context.Context) error {
					rollbackContext <- ctx
					events = append(events, "stop listener")

					return rollbackFailure
				},
			},
			{
				Name: "worker",
				Start: func(context.Context) error {
					events = append(events, "start worker")

					return startFailure
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = runtime.Start(context.Background())
	if !errors.Is(err, startFailure) {
		t.Fatalf("Start() error = %v, want start failure", err)
	}
	if !errors.Is(err, rollbackFailure) {
		t.Fatalf("Start() error = %v, want rollback failure", err)
	}
	if got := err.Error(); !strings.Contains(got, "rollback") {
		t.Fatalf("Start() error string = %q, want rollback", got)
	}

	var startupError *service.StartupError
	if !errors.As(err, &startupError) {
		t.Fatalf("Start() error type = %T, want *StartupError", err)
	}
	if startupError.Component != "worker" {
		t.Fatalf("StartupError.Component = %q, want worker", startupError.Component)
	}
	if state := runtime.State(); state != service.StateStopped {
		t.Fatalf("failed state = %v, want %v", state, service.StateStopped)
	}
	if cause := context.Cause(runtime.Context()); !errors.Is(cause, startFailure) {
		t.Fatalf("service context cause = %v, want start failure", cause)
	}

	want := []string{"start listener", "start worker", "stop listener"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	select {
	case <-(<-rollbackContext).Done():
	default:
		t.Fatal("completed rollback retained its timeout timer")
	}
}

func TestStartupRollbackTimeoutRemainsJoinable(t *testing.T) {
	t.Parallel()

	stopEntered := make(chan struct{})
	releaseStop := make(chan struct{})
	failureEntered := make(chan struct{})
	cancellationObserved := make(chan struct{})
	releaseFailure := make(chan struct{})
	startFailure := errors.New("start failed")
	runtime, err := service.New(service.Config{
		RollbackTimeout: time.Nanosecond,
		Components: []service.Component{
			{
				Name:  "stuck",
				Start: func(context.Context) error { return nil },
				Stop: func(context.Context) error {
					close(stopEntered)
					<-releaseStop

					return nil
				},
			},
			{
				Name: "failing",
				Start: func(ctx context.Context) error {
					close(failureEntered)
					<-ctx.Done()
					close(cancellationObserved)
					<-releaseFailure

					return startFailure
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	<-failureEntered
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- runtime.Shutdown(context.Background()) }()
	<-cancellationObserved
	close(releaseFailure)
	<-stopEntered
	err = <-startResult
	if !errors.Is(err, startFailure) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want start and rollback timeout", err)
	}
	if state := runtime.State(); state != service.StateStopping {
		t.Fatalf("State() = %v, want stopping", state)
	}
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown() returned before rollback joined: %v", err)
	default:
	}
	close(releaseStop)
	if err := <-shutdownResult; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestLifecycleTransitionsAreExplicitAndRepeatable(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{
		Components: []service.Component{{Name: "worker"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if runtime.Ready() {
		t.Fatal("Ready() = true before startup")
	}
	if err := runtime.Drain(); !errors.Is(err, service.ErrInvalidState) {
		t.Fatalf("Drain() before Start() error = %v, want ErrInvalidState", err)
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !runtime.Ready() {
		t.Fatal("Ready() = false after startup")
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, service.ErrInvalidState) {
		t.Fatalf("second Start() error = %v, want ErrInvalidState", err)
	}
	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if err := runtime.Drain(); err != nil {
		t.Fatalf("second Drain() error = %v", err)
	}
	if runtime.Ready() {
		t.Fatal("Ready() = true while draining")
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}

	var stateError *service.StateError
	err = runtime.Drain()
	if !errors.As(err, &stateError) || stateError.Operation != "drain" {
		t.Fatalf("Drain() stopped error = %#v, want drain StateError", err)
	}

	states := map[service.State]string{
		service.StateNew:      "new",
		service.StateStarting: "starting",
		service.StateReady:    "ready",
		service.StateDraining: "draining",
		service.StateStopping: "stopping",
		service.StateStopped:  "stopped",
		service.State(255):    "state(255)",
	}
	for state, want := range states {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}

func TestConcurrentLifecycleOperationsRespectStartingAndDraining(t *testing.T) {
	t.Parallel()

	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Start: func(context.Context) error {
			close(startEntered)
			<-releaseStart

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	<-startEntered
	if runtime.Ready() {
		t.Fatal("Ready() = true during startup")
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, service.ErrInvalidState) {
		t.Fatalf("concurrent Start() error = %v, want ErrInvalidState", err)
	}
	if err := runtime.Drain(); !errors.Is(err, service.ErrInvalidState) {
		t.Fatalf("concurrent Drain() error = %v, want ErrInvalidState", err)
	}
	close(releaseStart)
	if err := <-startResult; err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	const callers = 8
	releaseDrains := make(chan struct{})
	drainResults := make(chan error, callers)
	for range callers {
		go func() {
			<-releaseDrains
			drainResults <- runtime.Drain()
		}()
	}
	close(releaseDrains)
	for range callers {
		if err := <-drainResults; err != nil {
			t.Fatalf("concurrent Drain() error = %v", err)
		}
	}
	if state := runtime.State(); state != service.StateDraining {
		t.Fatalf("State() = %v, want draining", state)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestZeroServiceShutdownIsSafe(t *testing.T) {
	t.Parallel()

	var runtime service.Service
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if state := runtime.State(); state != service.StateStopped {
		t.Fatalf("State() = %v, want stopped", state)
	}
}

func TestZeroServiceUsesSafeSupervisionDefault(t *testing.T) {
	t.Parallel()

	var runtime service.Service
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	taskEntered := make(chan struct{})
	if err := runtime.Go("worker", func(ctx context.Context) error {
		close(taskEntered)
		<-ctx.Done()

		return nil
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	<-taskEntered
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestComponentPanicsAreContainedAndCleanupContinues(t *testing.T) {
	t.Parallel()

	t.Run("startup", func(t *testing.T) {
		t.Parallel()

		stopped := false
		runtime, err := service.New(service.Config{Components: []service.Component{
			{
				Name:  "owned",
				Start: func(context.Context) error { return nil },
				Stop:  func(context.Context) error { stopped = true; return nil },
			},
			{Name: "panicking", Start: func(context.Context) error { panic("boom") }},
		}})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		err = runtime.Start(context.Background())
		var panicError *service.PanicError
		if !errors.As(err, &panicError) {
			t.Fatalf("Start() error = %v, want PanicError", err)
		}
		if panicError.Component != "panicking" || panicError.Operation != "start" {
			t.Fatalf("PanicError = %#v", panicError)
		}
		if !stopped {
			t.Fatal("owned component was not rolled back")
		}
	})

	t.Run("shutdown", func(t *testing.T) {
		t.Parallel()

		var events []string
		runtime, err := service.New(service.Config{Components: []service.Component{
			{
				Name: "first",
				Stop: func(context.Context) error {
					events = append(events, "first")

					return nil
				},
			},
			{
				Name: "second",
				Stop: func(context.Context) error {
					events = append(events, "second")
					panic("stop boom")
				},
			},
		}})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if err := runtime.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		err = runtime.Shutdown(context.Background())
		var shutdownError *service.ShutdownError
		if !errors.As(err, &shutdownError) {
			t.Fatalf("Shutdown() error = %v, want ShutdownError", err)
		}
		if got := shutdownError.Error(); !strings.Contains(got, "shutdown failed") {
			t.Fatalf("ShutdownError.Error() = %q", got)
		}
		var panicError *service.PanicError
		if !errors.As(err, &panicError) {
			t.Fatalf("Shutdown() error = %v, want PanicError", err)
		}
		if want := []string{"second", "first"}; !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})
}

func TestNilOperationContextsAreRejected(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	//lint:ignore SA1012 Public boundary must reject nil context safely.
	//nolint:staticcheck // This test verifies the documented nil rejection.
	if err := runtime.Start(nil); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Start(nil) error = %v, want ErrInvalidConfig", err)
	}
	if state := runtime.State(); state != service.StateNew {
		t.Fatalf("State() = %v after Start(nil), want new", state)
	}
	//lint:ignore SA1012 Public boundary must reject nil context safely.
	//nolint:staticcheck // This test verifies the documented nil rejection.
	if err := runtime.Shutdown(nil); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Shutdown(nil) error = %v, want ErrInvalidConfig", err)
	}
	if state := runtime.State(); state != service.StateNew {
		t.Fatalf("State() = %v after Shutdown(nil), want new", state)
	}
}

func TestConcurrentShutdownRunsCleanupOnceAndBoundsEachWaiter(t *testing.T) {
	t.Parallel()

	stopEntered := make(chan struct{})
	releaseStop := make(chan struct{})
	stopCalls := make(chan struct{}, 2)
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Stop: func(context.Context) error {
			stopCalls <- struct{}{}
			close(stopEntered)
			<-releaseStop

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- runtime.Shutdown(context.Background())
	}()
	<-stopEntered

	waitContext, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if err := runtime.Shutdown(waitContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded Shutdown() error = %v, want context.Canceled", err)
	}

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- runtime.Shutdown(context.Background())
	}()
	close(releaseStop)

	if err := <-firstResult; err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	if calls := len(stopCalls); calls != 1 {
		t.Fatalf("stop calls = %d, want 1", calls)
	}
}

func TestShutdownCallerCanAbandonUncooperativeComponent(t *testing.T) {
	t.Parallel()

	stopEntered := make(chan struct{})
	releaseStop := make(chan struct{})
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "stuck",
		Stop: func(context.Context) error {
			close(stopEntered)
			<-releaseStop

			return nil
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.Shutdown(shutdownContext) }()
	<-stopEntered
	cancelShutdown()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v, want context.Canceled", err)
	}
	if state := runtime.State(); state != service.StateStopping {
		t.Fatalf("State() = %v, want stopping", state)
	}
	close(releaseStop)
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("joined Shutdown() error = %v", err)
	}
}

func TestShutdownCancelsAndJoinsStartup(t *testing.T) {
	t.Parallel()

	startEntered := make(chan struct{})
	cancellationObserved := make(chan struct{})
	allowStartReturn := make(chan struct{})
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Start: func(ctx context.Context) error {
			close(startEntered)
			<-ctx.Done()
			close(cancellationObserved)
			<-allowStartReturn

			return context.Cause(ctx)
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	startResult := make(chan error, 1)
	go func() {
		startResult <- runtime.Start(context.Background())
	}()
	<-startEntered

	shutdownResult := make(chan error, 1)
	go func() {
		shutdownResult <- runtime.Shutdown(context.Background())
	}()
	<-cancellationObserved

	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown() returned before startup joined: %v", err)
	default:
	}
	close(allowStartReturn)

	if err := <-shutdownResult; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-startResult; !errors.Is(err, service.ErrShutdown) {
		t.Fatalf("Start() error = %v, want ErrShutdown", err)
	}
	if state := runtime.State(); state != service.StateStopped {
		t.Fatalf("State() = %v, want stopped", state)
	}
}

func TestShutdownDuringStartupDoesNotStartLaterComponents(t *testing.T) {
	t.Parallel()

	firstEntered := make(chan struct{})
	secondStarted := make(chan struct{}, 1)
	firstStopped := make(chan struct{}, 1)
	runtime, err := service.New(service.Config{Components: []service.Component{
		{
			Name: "first",
			Start: func(ctx context.Context) error {
				close(firstEntered)
				<-ctx.Done()

				return nil
			},
			Stop: func(context.Context) error {
				firstStopped <- struct{}{}

				return nil
			},
		},
		{
			Name: "second",
			Start: func(context.Context) error {
				secondStarted <- struct{}{}

				return nil
			},
		},
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	<-firstEntered
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- runtime.Shutdown(context.Background()) }()

	if err := <-startResult; !errors.Is(err, service.ErrShutdown) {
		t.Fatalf("Start() error = %v, want ErrShutdown", err)
	}
	if err := <-shutdownResult; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-secondStarted:
		t.Fatal("second component started after shutdown began")
	default:
	}
	select {
	case <-firstStopped:
	default:
		t.Fatal("successfully started first component was not rolled back")
	}
}

func TestSupervisedWorkCancelsServiceAndIsJoined(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	taskEntered := make(chan struct{})
	cancellationObserved := make(chan struct{})
	allowTaskReturn := make(chan struct{})
	if err := runtime.Go("consumer", func(ctx context.Context) error {
		close(taskEntered)
		<-ctx.Done()
		close(cancellationObserved)
		<-allowTaskReturn

		return nil
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	<-taskEntered

	shutdownResult := make(chan error, 1)
	go func() {
		shutdownResult <- runtime.Shutdown(context.Background())
	}()
	<-cancellationObserved
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown() returned before task joined: %v", err)
	default:
	}
	close(allowTaskReturn)
	if err := <-shutdownResult; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestSupervisedTaskCancellationResultIsNormalShutdown(t *testing.T) {
	t.Parallel()

	taskFailure := errors.New("task failed during cancellation")
	tests := map[string]struct {
		result func(context.Context) error
		want   error
	}{
		"context error": {
			result: func(ctx context.Context) error { return ctx.Err() },
		},
		"cancellation cause": {
			result: context.Cause,
		},
		"unrelated failure": {
			result: func(context.Context) error { return taskFailure },
			want:   taskFailure,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runtime, err := service.New(service.Config{})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := runtime.Start(context.Background()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			started := make(chan struct{})
			if err := runtime.Go("scheduler", func(ctx context.Context) error {
				close(started)
				<-ctx.Done()

				return test.result(ctx)
			}); err != nil {
				t.Fatalf("Go() error = %v", err)
			}
			<-started

			err = runtime.Shutdown(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Shutdown() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSupervisedTasksRespectConfiguredBound(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{MaxTasks: 1})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	firstEntered := make(chan struct{})
	if err := runtime.Go("first", func(ctx context.Context) error {
		close(firstEntered)
		<-ctx.Done()

		return nil
	}); err != nil {
		t.Fatalf("Go(first) error = %v", err)
	}
	<-firstEntered
	secondCalled := false
	err = runtime.Go("second", func(context.Context) error {
		secondCalled = true

		return nil
	})
	if !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Go(second) error = %v, want ErrInvalidConfig", err)
	}
	if secondCalled {
		t.Fatal("task above MaxTasks was started")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestSupervisedTaskMayReturnSuccessfullyBeforeShutdown(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	returned := make(chan struct{})
	if err := runtime.Go("finite", func(context.Context) error {
		close(returned)

		return nil
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	<-returned
	if cause := context.Cause(runtime.Context()); cause != nil {
		t.Fatalf("service cause = %v, want nil", cause)
	}
	if !runtime.Ready() {
		t.Fatal("successful task completion drained the service")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestSupervisedFailureDrainsAndPreservesCause(t *testing.T) {
	t.Parallel()

	taskFailure := errors.New("consumer failed")
	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	releaseTask := make(chan struct{})
	if err := runtime.Go("consumer", func(context.Context) error {
		<-releaseTask

		return taskFailure
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	close(releaseTask)
	<-runtime.Context().Done()

	if runtime.Ready() {
		t.Fatal("Ready() = true after supervised failure")
	}
	if cause := context.Cause(runtime.Context()); !errors.Is(cause, taskFailure) {
		t.Fatalf("service context cause = %v, want task failure", cause)
	}
	if err := runtime.Shutdown(context.Background()); !errors.Is(err, taskFailure) {
		t.Fatalf("Shutdown() error = %v, want task failure", err)
	}
}

func TestLifecycleErrorContractsAndInvalidOperations(t *testing.T) {
	t.Parallel()

	configError := &service.ConfigError{Field: "field", Reason: "reason"}
	if got := configError.Error(); !strings.Contains(got, "field: reason") {
		t.Fatalf("ConfigError.Error() = %q", got)
	}
	stateError := &service.StateError{Operation: "operate", State: service.StateNew}
	if got := stateError.Error(); !strings.Contains(got, "operate") {
		t.Fatalf("StateError.Error() = %q", got)
	}
	componentFailure := errors.New("component failed")
	componentError := &service.ComponentError{
		Component: "worker",
		Operation: "run",
		Err:       componentFailure,
	}
	if got := componentError.Error(); !strings.Contains(got, "worker") {
		t.Fatalf("ComponentError.Error() = %q", got)
	}
	if !errors.Is(componentError, componentFailure) {
		t.Fatal("ComponentError does not unwrap its cause")
	}

	var runtime service.Service
	if runtime.Context() == nil {
		t.Fatal("zero Service.Context() = nil")
	}
	if err := runtime.Go("worker", func(context.Context) error { return nil }); !errors.Is(err, service.ErrInvalidState) {
		t.Fatalf("Go() before Start() error = %v, want ErrInvalidState", err)
	}

	started, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := started.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := started.Go(" ", func(context.Context) error { return nil }); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Go() blank name error = %v, want ErrInvalidConfig", err)
	}
	if err := started.Go("worker", nil); !errors.Is(err, service.ErrInvalidConfig) {
		t.Fatalf("Go() nil task error = %v, want ErrInvalidConfig", err)
	}
	if err := started.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestParentCancellationAfterStartsTriggersRollback(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancelCause(context.Background())
	parentFailure := errors.New("parent failed")
	cancel(parentFailure)
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "start-only",
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = runtime.Start(parent)
	if !errors.Is(err, parentFailure) {
		t.Fatalf("Start() error = %v, want parent failure", err)
	}
	var startupError *service.StartupError
	if !errors.As(err, &startupError) || startupError.Component != "service" {
		t.Fatalf("Start() error = %#v, want service StartupError", err)
	}
	if got := startupError.Error(); !strings.Contains(got, "parent failed") {
		t.Fatalf("StartupError.Error() = %q", got)
	}

	empty, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New(empty) error = %v", err)
	}
	if err := empty.Start(parent); !errors.Is(err, parentFailure) {
		t.Fatalf("Start(empty) error = %v, want parent failure", err)
	}
}

func TestStartupShutdownWaiterCanAbandonWait(t *testing.T) {
	t.Parallel()

	startEntered := make(chan struct{})
	cancellationObserved := make(chan struct{})
	allowStartReturn := make(chan struct{})
	runtime, err := service.New(service.Config{Components: []service.Component{{
		Name: "worker",
		Start: func(ctx context.Context) error {
			close(startEntered)
			<-ctx.Done()
			close(cancellationObserved)
			<-allowStartReturn

			return context.Cause(ctx)
		},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startResult := make(chan error, 1)
	go func() { startResult <- runtime.Start(context.Background()) }()
	<-startEntered

	waitContext, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if err := runtime.Shutdown(waitContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v, want context.Canceled", err)
	}
	<-cancellationObserved
	close(allowStartReturn)
	if err := <-startResult; !errors.Is(err, service.ErrShutdown) {
		t.Fatalf("Start() error = %v, want ErrShutdown", err)
	}
}

func TestSupervisedPanicIsRedactedAndReturned(t *testing.T) {
	t.Parallel()

	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Go("consumer", func(context.Context) error {
		panic("secret panic value")
	}); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	<-runtime.Context().Done()

	err = runtime.Shutdown(context.Background())
	var panicError *service.PanicError
	if !errors.As(err, &panicError) {
		t.Fatalf("Shutdown() error = %v, want PanicError", err)
	}
	if got := panicError.Error(); strings.Contains(got, "secret") {
		t.Fatalf("PanicError.Error() leaked panic value: %q", got)
	}
}
