package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

type testSignal string

func (signal testSignal) String() string { return string(signal) }
func (testSignal) Signal()               {}

type stagedCancelContext struct {
	context.Context

	checked  chan struct{}
	done     chan struct{}
	check    sync.Once
	cancel   sync.Once
	mu       sync.RWMutex
	canceled bool
}

func newStagedCancelContext() *stagedCancelContext {
	return &stagedCancelContext{
		Context: context.Background(),
		checked: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (ctx *stagedCancelContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *stagedCancelContext) Err() error {
	ctx.check.Do(func() { close(ctx.checked) })
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	if ctx.canceled {
		return context.Canceled
	}

	return nil
}

func (ctx *stagedCancelContext) Cancel() {
	ctx.mu.Lock()
	ctx.canceled = true
	ctx.mu.Unlock()
	ctx.cancel.Do(func() { close(ctx.done) })
}

func TestHTTPComponentStopsHonorExpiredContext(t *testing.T) {
	t.Parallel()

	for _, management := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen() error = %v", err)
		}
		server, err := serverhttp.New(listener, http.NotFoundHandler())
		if err != nil {
			_ = listener.Close()
			t.Fatalf("serverhttp.New() error = %v", err)
		}
		cause := errors.New("shutdown deadline")
		ctx, cancelContext := context.WithCancelCause(context.Background())
		cancelContext(cause)
		done := make(chan error, 1)
		done <- nil
		cancelRun := func() {}

		if management {
			owner := &managementOwner{
				server: server, cancel: cancelRun, done: done,
			}
			if err := owner.stop(ctx); !errors.Is(err, cause) {
				t.Fatalf("management stop error = %v", err)
			}
		} else {
			owner := &businessOwner{server: server}
			if err := owner.stop(ctx); err != nil {
				t.Fatalf("business stop error = %v", err)
			}
		}
	}
}

func TestStopHTTPServerForcesCloseWhenContextExpiresWhileWaiting(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	server, err := serverhttp.New(listener, http.NotFoundHandler())
	if err != nil {
		_ = listener.Close()
		t.Fatalf("serverhttp.New() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Run(context.Background())
	}()
	ctx := newStagedCancelContext()
	stopped := make(chan error, 1)
	go func() {
		stopped <- stopHTTPServer(ctx, server, done)
	}()
	<-ctx.checked
	ctx.Cancel()

	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Fatalf("stopHTTPServer() error = %v, want context cancellation", err)
	}
}

func TestShutdownAfterRegistrationFailurePreservesCause(t *testing.T) {
	t.Parallel()

	runtime, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	primary := errors.New("task registration failed")
	if err := shutdownAfterFailure(runtime, primary); !errors.Is(err, primary) {
		t.Fatalf("shutdownAfterFailure() error = %v", err)
	}
	if state := runtime.State(); state != StateStopped {
		t.Fatalf("state = %v, want stopped", state)
	}
}

func TestOneShotRegistrationFailureStopsRuntime(t *testing.T) {
	t.Parallel()

	runtime, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	availability := newPlatformState(func() *Service { return runtime })
	err = executeOneShot(
		context.Background(),
		Invocation{},
		runtime,
		[]Task{{
			Name: "migration",
			Run:  func(context.Context) error { return nil },
		}},
		availability,
	)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("executeOneShot() error = %v, want invalid state", err)
	}
	if state := runtime.State(); state != StateStopped {
		t.Fatalf("state = %v, want stopped", state)
	}
}

func TestLongRunningRegistrationFailureStopsRuntime(t *testing.T) {
	t.Parallel()

	runtime, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	err = executeLongRunning(
		context.Background(),
		Invocation{},
		runtime,
		[]Task{{
			Name: "worker",
			Run:  func(context.Context) error { return nil },
		}},
		nil,
	)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("executeLongRunning() error = %v", err)
	}
	if state := runtime.State(); state != StateStopped {
		t.Fatalf("state = %v, want stopped", state)
	}
}

func TestResolvedManagementAddress(t *testing.T) {
	t.Parallel()

	if address := resolvedManagementAddress(""); address != defaultManagementAddress {
		t.Fatalf("default address = %q", address)
	}
	if address := resolvedManagementAddress("127.0.0.1:0"); address != "127.0.0.1:0" {
		t.Fatalf("explicit address = %q", address)
	}
}

func TestExitCodeFallbacks(t *testing.T) {
	t.Parallel()

	if exit := exitCode(&SignalError{Signal: testSignal("terminated")}); exit != 143 {
		t.Fatalf("signal exit = %d, want 143", exit)
	}
	timeout := &ShutdownTimeoutError{Err: context.DeadlineExceeded}
	if exit := exitCode(timeout); exit != 124 {
		t.Fatalf("shutdown timeout exit = %d, want 124", exit)
	}
	if !errors.Is(timeout, context.DeadlineExceeded) {
		t.Fatalf("ShutdownTimeoutError = %v, want context deadline cause", timeout)
	}
	if timeout.Error() == "" {
		t.Fatal("ShutdownTimeoutError.Error() is blank")
	}
	if exit := exitCode(errors.New("unclassified")); exit != 70 {
		t.Fatalf("unclassified exit = %d, want 70", exit)
	}
}

func TestManagementConnectionLimitReleasesClosedConnections(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	limited := limitConnections(listener, 1)
	t.Cleanup(func() { _ = limited.Close() })

	firstClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("first net.Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = firstClient.Close() })
	firstServer, err := limited.Accept()
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}

	secondClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("second net.Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = secondClient.Close() })
	secondAccepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := limited.Accept()
		if acceptErr == nil {
			secondAccepted <- connection
		}
	}()
	select {
	case connection := <-secondAccepted:
		_ = connection.Close()
		t.Fatal("second connection accepted before the first closed")
	case <-time.After(20 * time.Millisecond):
	}
	if err := firstServer.Close(); err != nil {
		t.Fatalf("first server connection Close() error = %v", err)
	}
	select {
	case connection := <-secondAccepted:
		_ = connection.Close()
	case <-time.After(time.Second):
		t.Fatal("second connection was not accepted after capacity released")
	}
	bounded := limited.(*limitedListener)
	bounded.capacity <- struct{}{}
	if err := limited.Close(); err != nil {
		t.Fatalf("limited Close() error = %v", err)
	}
	if _, err := limited.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() after Close error = %v, want net.ErrClosed", err)
	}

	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("closed-listener net.Listen() error = %v", err)
	}
	if err := closedListener.Close(); err != nil {
		t.Fatalf("closed-listener Close() error = %v", err)
	}
	if _, err := limitConnections(closedListener, 1).Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed underlying Accept() error = %v, want net.ErrClosed", err)
	}
}

func TestPlatformStateActivatesOnlyAfterTaskInstallation(t *testing.T) {
	t.Parallel()

	runtime, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	state := newPlatformState(func() *Service { return runtime })
	if state.StartupComplete() || state.Ready() {
		t.Fatal("platform state became available before activation")
	}
	state.Activate()
	if !state.StartupComplete() || !state.Ready() {
		t.Fatal("platform state did not become available after activation")
	}
	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if !state.StartupComplete() || state.Ready() {
		t.Fatal("platform state did not preserve startup while withdrawing readiness")
	}
}

func TestSnapshotPlanOwnsMutableRegistrationSlices(t *testing.T) {
	t.Parallel()

	option := serverhttp.WithReadTimeout(time.Second)
	original := Plan{
		Components: []Component{{Name: "database"}},
		Tasks:      []Task{{Name: "worker"}},
		Readiness:  []ReadinessCheck{{Name: "database"}},
		HTTP: &HTTP{
			Address: "127.0.0.1:0",
			Handler: http.NotFoundHandler(),
			Options: []serverhttp.Option{option},
		},
	}
	snapshot := snapshotPlan(original)
	original.Components[0].Name = "changed"
	original.Tasks[0].Name = "changed"
	original.Readiness[0].Name = "changed"
	original.HTTP.Options[0] = nil

	if snapshot.Components[0].Name != "database" ||
		snapshot.Tasks[0].Name != "worker" ||
		snapshot.Readiness[0].Name != "database" ||
		snapshot.HTTP.Options[0] == nil {
		t.Fatalf("snapshot retained mutable plan storage: %#v", snapshot)
	}
}

func TestReadinessValidationEnforcesProbeCapacity(t *testing.T) {
	t.Parallel()

	checks := make([]ReadinessCheck, 65)
	for index := range checks {
		checks[index] = ReadinessCheck{
			Name: fmt.Sprintf("dependency-%d", index),
			Run:  func(context.Context) error { return nil },
		}
	}
	if err := validateReadiness(checks); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("validateReadiness() error = %v, want ErrInvalidDefinition", err)
	}
	runtime, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	availability := newPlatformState(func() *Service { return runtime })
	owner := newManagementOwner(
		Management{},
		nil,
		nil,
		checks,
		func() *Service { return runtime },
		availability,
	)
	var constructionError *ConstructionError
	if err := owner.start(context.Background()); !errors.As(err, &constructionError) {
		t.Fatalf("management start error = %v, want ConstructionError", err)
	}
}
