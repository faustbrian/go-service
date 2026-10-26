package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v3"
)

func TestExecuteSelectedPlanShutdownBudget(t *testing.T) {
	for _, allowance := range []time.Duration{0, 250 * time.Millisecond, 45 * time.Second, 1<<63 - 1} {
		for _, mode := range []string{"one-shot", "one-shot-failure", "one-shot-parent", "one-shot-signal", "worker-parent", "worker-signal", "worker-failure"} {
			t.Run(mode+"/"+allowance.String(), func(t *testing.T) {
				management, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = management.Close() })
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				signals := make(chan os.Signal, 2)
				started := make(chan struct{})
				deadlines := make(chan time.Time, 2)
				isWorker := mode == "worker-parent" || mode == "worker-signal" || mode == "worker-failure"
				kind, name := service.CommandKindOneShot, "migrate"
				if isWorker {
					kind, name = service.CommandKindLongRunning, "worker"
				}
				var stopCount atomic.Int32
				command := service.CommandFor(service.CommandSpec[struct{}]{
					Name: name, Kind: kind,
					Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
					Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
						stop := func(ctx context.Context) error {
							deadline, _ := ctx.Deadline()
							deadlines <- deadline
							stopCount.Add(1)
							return nil
						}
						return service.Plan{
							ShutdownTimeout: allowance,
							Components:      []service.Component{{Name: "first", Stop: stop}, {Name: "second", Stop: stop}},
							Tasks: []service.Task{{Name: "work", Run: func(ctx context.Context) error {
								close(started)
								switch mode {
								case "one-shot":
									return nil
								case "one-shot-failure", "worker-failure":
									return errors.New("command failure")
								default:
									<-ctx.Done()
									return context.Cause(ctx)
								}
							}}},
						}, nil
					},
				})
				commands := service.Commands{Migrate: command}
				if isWorker {
					commands = service.Commands{Worker: command}
				}
				var invocationSignals <-chan os.Signal
				if mode == "one-shot-signal" || mode == "worker-signal" {
					invocationSignals = signals
				}
				before := time.Now()
				result := make(chan int, 1)
				go func() {
					result <- service.Execute(ctx, service.Definition{
						Identity: service.Identity{Name: "budget-test"}, Commands: commands,
						Management: service.Management{Listener: management},
					}, service.Invocation{Args: []string{name}, Signals: invocationSignals, Stdout: io.Discard, Stderr: io.Discard})
				}()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("task did not start")
				}
				switch mode {
				case "one-shot-parent", "worker-parent":
					cancel()
				case "one-shot-signal", "worker-signal":
					signals <- os.Interrupt
				}
				wantExit := 0
				switch mode {
				case "one-shot-failure":
					wantExit = 1
				case "worker-failure":
					wantExit = 70
				case "one-shot-signal", "worker-signal":
					wantExit = 130
				}
				select {
				case code := <-result:
					if code != wantExit {
						t.Errorf("exit = %d, want %d", code, wantExit)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("cleanup did not complete")
				}
				if stopCount.Load() != 2 {
					t.Fatalf("cleanup count = %d", stopCount.Load())
				}
				first, second := <-deadlines, <-deadlines
				if !first.Equal(second) {
					t.Errorf("cleanup phases received different deadlines: %v, %v", first, second)
				}
				want := allowance
				if want == 0 {
					want = 30 * time.Second
				}
				if first.Before(before.Add(want)) || first.After(time.Now().Add(want)) {
					t.Errorf("cleanup deadline = %v, want shutdown entry plus %v", first, want)
				}
			})
		}
	}
}

func TestExecuteRejectsNegativePlanShutdownBudgetBeforeOwnership(t *testing.T) {
	for _, kind := range []service.CommandKind{service.CommandKindOneShot, service.CommandKindLongRunning} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			business, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = business.Close() })
			var acquired atomic.Bool
			command := service.CommandFor(service.CommandSpec[struct{}]{
				Name: "check", Kind: kind,
				Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
				Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
					plan := service.Plan{ShutdownTimeout: -1, Components: []service.Component{{Name: "resource", Start: func(context.Context) error {
						acquired.Store(true)
						return nil
					}, Stop: func(context.Context) error { acquired.Store(true); return nil }}},
						Tasks: []service.Task{{Name: "work", Run: func(context.Context) error { acquired.Store(true); return nil }}},
					}
					if kind == service.CommandKindLongRunning {
						plan.HTTP = &service.HTTP{Listener: business, Handler: http.NotFoundHandler()}
					}
					return plan, nil
				},
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			code := service.Execute(ctx, service.Definition{
				Identity: service.Identity{Name: "budget-test"}, Commands: service.Commands{Custom: []service.Command{command}},
				Management: service.Management{Listener: listener},
			}, service.Invocation{Args: []string{"check"}, Stdout: io.Discard, Stderr: io.Discard})
			if code != 70 || acquired.Load() {
				t.Errorf("negative allowance exit=%d acquired=%v", code, acquired.Load())
			}
			for _, borrowed := range []net.Listener{listener, business} {
				connection, err := net.DialTimeout("tcp", borrowed.Addr().String(), time.Second)
				if err != nil {
					t.Fatalf("validation took borrowed listener ownership: %v", err)
				}
				_ = connection.Close()
			}
		})
	}
}

func TestExecutePlanShutdownBudgetStartsAfterRunningAndSurvivesEscalation(t *testing.T) {
	const allowance = 500 * time.Millisecond
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = management.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, stopEntered, stopFinished := make(chan struct{}), make(chan time.Time, 1), make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	worker := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker", Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{ShutdownTimeout: allowance,
				Components: []service.Component{{Name: "resource", Stop: func(ctx context.Context) error {
					defer close(stopFinished)
					deadline, _ := ctx.Deadline()
					stopEntered <- deadline
					<-release
					return context.Cause(ctx)
				}}},
				Tasks: []service.Task{{Name: "worker", Run: func(ctx context.Context) error {
					close(started)
					<-ctx.Done()
					return context.Cause(ctx)
				}}},
			}, nil
		},
	})
	signals := make(chan os.Signal, 2)
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, service.Definition{Identity: service.Identity{Name: "budget-test"},
			Commands: service.Commands{Worker: worker}, Management: service.Management{Listener: management},
		}, service.Invocation{Args: []string{"worker"}, Signals: signals, Stdout: io.Discard, Stderr: io.Discard})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	select {
	case code := <-result:
		t.Fatalf("shutdown allowance limited ordinary runtime: %d", code)
	case <-time.After(2 * allowance):
	}
	shutdownStarted := time.Now()
	signals <- os.Interrupt
	var originalDeadline time.Time
	select {
	case deadline := <-stopEntered:
		originalDeadline = deadline
		if deadline.Before(shutdownStarted.Add(allowance)) || deadline.After(time.Now().Add(allowance)) {
			t.Errorf("cleanup deadline = %v, want original shutdown entry plus %v", deadline, allowance)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	// Escalate late enough that renewing the same allowance would outlive the
	// original deadline by much more than the scheduling tolerance below.
	escalateAt := time.NewTimer(time.Until(originalDeadline.Add(-100 * time.Millisecond)))
	defer escalateAt.Stop()
	select {
	case code := <-result:
		t.Fatalf("uncooperative cleanup ended before its deadline: %d", code)
	case <-escalateAt.C:
	}
	signals <- os.Interrupt
	watchdog := time.NewTimer(time.Until(originalDeadline.Add(150 * time.Millisecond)))
	defer watchdog.Stop()
	select {
	case code := <-result:
		if code != 124 {
			t.Errorf("expired cleanup after escalation = %d, want 124", code)
		}
	case <-watchdog.C:
		t.Fatal("escalation extended the selected shutdown allowance")
	}
	unblock()
	select {
	case <-stopFinished:
	case <-time.After(time.Second):
		t.Fatal("released cleanup did not join")
	}
}

func TestExecutePlanShutdownBudgetTinyPositiveIsNotDefault(t *testing.T) {
	stopped := make(chan struct{})
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate", Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{ShutdownTimeout: time.Nanosecond,
				Components: []service.Component{{Name: "resource", Stop: func(ctx context.Context) error {
					defer close(stopped)
					<-ctx.Done()
					return context.Cause(ctx)
				}}},
				Tasks: []service.Task{{Name: "command", Run: func(context.Context) error { return nil }}},
			}, nil
		},
	})
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(t.Context(), service.Definition{
			Identity: service.Identity{Name: "budget-test"}, Commands: service.Commands{Migrate: command},
		}, service.Invocation{Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard})
	}()
	select {
	case code := <-result:
		if code != 124 {
			t.Errorf("expired cleanup exit = %d, want 124", code)
		}
	case <-time.After(time.Second):
		t.Fatal("tiny positive budget became the default")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("timed-out cleanup did not join")
	}
}

func TestExecutePlanShutdownBudgetDoesNotChangeStartupRollback(t *testing.T) {
	deadline := make(chan time.Time, 1)
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate", Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{ShutdownTimeout: time.Nanosecond, Components: []service.Component{
				{Name: "acquired", Stop: func(ctx context.Context) error { value, _ := ctx.Deadline(); deadline <- value; return nil }},
				{Name: "fails", Start: func(context.Context) error { return errors.New("startup failed") }, Stop: func(context.Context) error { t.Error("failed start transferred ownership"); return nil }},
			}}, nil
		},
	})
	before := time.Now()
	code := service.Execute(t.Context(), service.Definition{
		Identity: service.Identity{Name: "budget-test"}, Commands: service.Commands{Migrate: command},
	}, service.Invocation{Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard})
	if code != 75 {
		t.Errorf("startup failure exit = %d, want 75", code)
	}
	select {
	case value := <-deadline:
		if value.Before(before.Add(30*time.Second)) || value.After(time.Now().Add(30*time.Second)) {
			t.Errorf("startup rollback allowance changed: %v", value)
		}
	default:
		t.Fatal("startup rollback did not release acquired ownership")
	}
}

func TestExecutePlanShutdownBudgetWithRealHTTP(t *testing.T) {
	const allowance = 45 * time.Second
	business, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = business.Close() })
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = management.Close() })
	deadline := make(chan time.Time, 1)
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve", Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{ShutdownTimeout: allowance,
				HTTP:       &service.HTTP{Listener: business, Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "served") })},
				Components: []service.Component{{Name: "resource", Stop: func(ctx context.Context) error { value, _ := ctx.Deadline(); deadline <- value; return nil }}},
			}, nil
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, service.Definition{
			Identity: service.Identity{Name: "budget-http"}, Commands: service.Commands{Serve: command},
			Management: service.Management{Listener: management},
		}, service.Invocation{Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard})
	}()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("http://" + business.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "served" {
		t.Fatalf("HTTP response = %d %q", response.StatusCode, body)
	}
	before := time.Now()
	cancel()
	select {
	case code := <-result:
		if code != 0 {
			t.Errorf("HTTP retirement exit = %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP owner did not retire")
	}
	select {
	case value := <-deadline:
		if value.Before(before.Add(allowance)) || value.After(time.Now().Add(allowance)) {
			t.Errorf("HTTP cleanup deadline = %v, want selected allowance %v", value, allowance)
		}
	default:
		t.Error("HTTP cleanup was skipped")
	}
	for _, retired := range []net.Listener{business, management} {
		connection, err := net.DialTimeout("tcp", retired.Addr().String(), time.Second)
		if err == nil {
			_ = connection.Close()
			t.Error("retired listener still accepts connections")
		}
	}
}
