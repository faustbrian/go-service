package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/faustbrian/go-service/v3/serverhttp"
)

func TestExecuteRetainsMixedOneShotFailureWhenParentCancels(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		parent, cancel := context.WithCancel(context.Background())
		command := CommandFor(CommandSpec[struct{}]{
			Name: "migrate", Kind: CommandKindOneShot,
			Load: func(context.Context, Invocation) (struct{}, error) { return struct{}{}, nil },
			Build: func(context.Context, BuildContext, struct{}) (Plan, error) {
				return Plan{Tasks: []Task{{Name: "work", Run: func(ctx context.Context) error {
					cancel()
					<-ctx.Done()
					if mixed {
						return errors.Join(ctx.Err(), errors.New("independent task failure"))
					}
					return ctx.Err()
				}}}}, nil
			},
		})
		exit := Execute(parent, Definition{Identity: Identity{Name: "mixed-task"}, Commands: Commands{Migrate: command}}, Invocation{Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard})
		cancel()
		if mixed && exit == 0 {
			t.Fatal("mixed one-shot failure became successful parent cancellation")
		}
		if !mixed && exit != 0 {
			t.Fatalf("pure one-shot cancellation = %d, want success", exit)
		}
	}
}

type cyclicTaskError struct{}

func (*cyclicTaskError) Error() string     { return "cyclic task error" }
func (err *cyclicTaskError) Unwrap() error { return err }

type taskErrorCauses []error

func (taskErrorCauses) Error() string       { return "task error causes" }
func (err taskErrorCauses) Unwrap() []error { return []error(err) }

//nolint:errorlint // Exact retained-value identity is the compatibility contract.
func TestCallbackErrorGraphFailurePreservesUnsafeValueWithoutUnwrapping(t *testing.T) {
	cycle := &cyclicTaskError{}
	err := invoke("work", "run", func(context.Context) error { return cycle }, t.Context())
	var graphError *ErrorGraphError
	if !errors.As(err, &graphError) || graphError.Value != cycle {
		t.Fatalf("unsafe original value not retained: %T", err)
	}
	if err.Error() == "" || errors.Unwrap(err) != nil {
		t.Fatal("unsafe graph was exposed to automatic traversal")
	}
	deep := error(errors.New("inner failure"))
	for range 65 {
		deep = fmt.Errorf("nested: %w", deep)
	}
	err = invoke("work", "run", func(context.Context) error { return deep }, t.Context())
	if err != deep {
		t.Fatal("finite nested callback graph was changed")
	}
	wide := make([]error, 128)
	for index := range wide {
		wide[index] = context.Canceled
	}
	joined := errors.Join(wide...)
	if err := invoke("work", "run", func(context.Context) error { return joined }, t.Context()); err != joined {
		t.Fatal("wide finite error graph was changed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deepCancellation := error(context.Canceled)
	for range 128 {
		deepCancellation = fmt.Errorf("nested: %w", deepCancellation)
	}
	if !isCancellationResult(ctx, deepCancellation) {
		t.Fatal("finite nested cancellation became failure")
	}
}

func TestCancellationClassificationRetainsUnprovenGraphsAndCustomCauses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{nil, &cyclicTaskError{}, taskErrorCauses{}, taskErrorCauses{nil}, taskErrorCauses{context.Canceled, errors.New("failure")}} {
		if isCancellationResult(ctx, err) {
			t.Fatalf("unproven error graph suppressed: %T", err)
		}
	}
	custom := fmt.Errorf("operator stop: %w", errors.New("custom cause"))
	parent, stop := context.WithCancelCause(context.Background())
	stop(custom)
	if !isCancellationResult(parent, fmt.Errorf("task: %w", custom)) {
		t.Fatal("wrapped parent cause became a new task failure")
	}
	if !isCancellationResult(ctx, taskErrorCauses{nil, context.Canceled}) {
		t.Fatal("non-comparable pure cancellation became failure")
	}
	if isCancellationResult(context.Background(), context.Canceled) {
		t.Fatal("live parent suppressed unrelated task cancellation")
	}
}

func TestSupervisedTaskRetainsFailuresAlongsideParentDeadline(t *testing.T) {
	for _, mode := range []string{"mixed-serving-failure", "http-cleanup-deadline", "mixed-task-failure", "pure-wrapped-deadline", "pure-joined-deadline", "pure-wide-cancellations"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				runtime, err := New(Config{})
				if err != nil {
					t.Fatal(err)
				}
				if err := runtime.Start(parent); err != nil {
					t.Fatal(err)
				}
				failure := errors.New("independent failure")
				var taskError error
				entered := make(chan struct{})
				if err := runtime.Go("http", func(ctx context.Context) error {
					close(entered)
					<-ctx.Done()
					switch mode {
					case "mixed-serving-failure":
						taskError = &serverhttp.RunError{Failures: []error{ctx.Err(), &serverhttp.ServeError{Err: failure}}}
					case "http-cleanup-deadline":
						taskError = &serverhttp.RunError{Failures: []error{ctx.Err()}}
					case "mixed-task-failure":
						taskError = fmt.Errorf("task: %w", errors.Join(ctx.Err(), failure))
					case "pure-wrapped-deadline":
						taskError = fmt.Errorf("task: %w", ctx.Err())
					case "pure-joined-deadline":
						taskError = errors.Join(ctx.Err(), context.Cause(ctx))
					case "pure-wide-cancellations":
						causes := make([]error, 64)
						for index := range causes {
							causes[index] = ctx.Err()
						}
						taskError = errors.Join(causes...)
					}
					return taskError
				}); err != nil {
					t.Fatal(err)
				}
				<-entered
				<-parent.Done()
				cleanup, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				err = runtime.Shutdown(cleanup)
				if strings.HasPrefix(mode, "pure-") {
					if err != nil {
						t.Fatalf("pure cancellation became failure: %v", err)
					}
				} else if !errors.Is(err, taskError) {
					t.Fatalf("task failure discarded when parent expired: %v; want %v", err, taskError)
				}
			})
		})
	}
}

func TestExecuteTerminatesForCyclicTaskFailures(t *testing.T) {
	for _, kind := range []CommandKind{CommandKindLongRunning, CommandKindOneShot} {
		parent, cancel := context.WithCancel(context.Background())
		management, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = management.Close() })
		name := "worker"
		if kind == CommandKindOneShot {
			name = "migrate"
		}
		command := CommandFor(CommandSpec[struct{}]{
			Name: name, Kind: kind,
			Load: func(context.Context, Invocation) (struct{}, error) { return struct{}{}, nil },
			Build: func(context.Context, BuildContext, struct{}) (Plan, error) {
				return Plan{ShutdownTimeout: time.Second, Tasks: []Task{{Name: "work", Run: func(ctx context.Context) error {
					cancel()
					<-ctx.Done()
					return errors.Join(ctx.Err(), &cyclicTaskError{})
				}}}}, nil
			},
		})
		commands := Commands{Worker: command}
		if kind == CommandKindOneShot {
			commands = Commands{Migrate: command}
		}
		exit := Execute(parent, Definition{Identity: Identity{Name: "cyclic-task"}, Commands: commands, Management: Management{Listener: management}}, Invocation{Args: []string{name}, Stdout: io.Discard, Stderr: io.Discard})
		cancel()
		if exit == 0 {
			t.Fatalf("cyclic task failure reported success for %s", name)
		}
	}
}
