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

type cancellationAlias struct {
	target error
	cause  error
}

func (*cancellationAlias) Error() string { return "cancellation acknowledgement" }

//nolint:errorlint // Is implements the standard library's shallow matching contract.
func (err *cancellationAlias) Is(target error) bool { return target == err.target }
func (err *cancellationAlias) Unwrap() error        { return err.cause }

func TestCancellationClassificationPreservesCustomWrapperSemantics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failure := errors.New("independent failure")
	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"custom leaf":           {&cancellationAlias{target: context.Canceled}, true},
		"custom transport":      {&cancellationAlias{target: context.Canceled, cause: errors.New("transport interrupted")}, true},
		"custom joined failure": {&cancellationAlias{target: context.Canceled, cause: errors.Join(context.Canceled, failure)}, false},
		"failure first":         {errors.Join(failure, &cancellationAlias{target: context.Canceled}), false},
		"failure last":          {errors.Join(&cancellationAlias{target: context.Canceled}, failure), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isCancellationResult(ctx, test.err); got != test.want {
				t.Fatalf("cancellation result = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCancellationClassificationAcknowledgesSharedDAG(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var result error = context.Canceled
	for range 40 {
		result = errors.Join(result, result)
	}
	if !isCancellationResult(ctx, result) {
		t.Fatal("shared finite cancellation causes became a task failure")
	}
}

func TestExecuteClassifiesFiniteCancellationResults(t *testing.T) {
	for _, kind := range []CommandKind{CommandKindLongRunning, CommandKindOneShot} {
		name := "worker"
		if kind == CommandKindOneShot {
			name = "migrate"
		}
		for _, mode := range []string{"pure-dag", "pure-custom-leaf", "pure-custom-wrapper", "mixed-left", "mixed-right", "mixed-custom-wrapper", "mixed-shared", "typed-timeout"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				management, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer management.Close()
				command := CommandFor(CommandSpec[struct{}]{
					Name: name, Kind: kind,
					Load: func(context.Context, Invocation) (struct{}, error) { return struct{}{}, nil },
					Build: func(context.Context, BuildContext, struct{}) (Plan, error) {
						return Plan{ShutdownTimeout: time.Second, Tasks: []Task{{Name: "work", Run: func(ctx context.Context) error {
							cancel()
							<-ctx.Done()
							failure := errors.New("independent failure")
							switch mode {
							case "pure-dag", "mixed-shared":
								var result error = ctx.Err()
								depth := 40
								if mode == "mixed-shared" {
									depth = 4
								}
								for range depth {
									result = errors.Join(result, result)
								}
								if mode == "mixed-shared" {
									return errors.Join(result, failure)
								}
								return result
							case "pure-custom-leaf":
								return &cancellationAlias{target: ctx.Err()}
							case "pure-custom-wrapper":
								return &cancellationAlias{target: ctx.Err(), cause: errors.New("transport interrupted")}
							case "mixed-left":
								return errors.Join(failure, ctx.Err())
							case "mixed-right":
								return errors.Join(ctx.Err(), failure)
							case "mixed-custom-wrapper":
								return &cancellationAlias{target: ctx.Err(), cause: errors.Join(ctx.Err(), failure)}
							default:
								return &ShutdownTimeoutError{Err: context.DeadlineExceeded}
							}
						}}}}, nil
					},
				})
				commands := Commands{Worker: command}
				if kind == CommandKindOneShot {
					commands = Commands{Migrate: command}
				}
				got := Execute(parent, Definition{Identity: Identity{Name: "finite-task"}, Commands: commands, Management: Management{Listener: management}}, Invocation{Args: []string{name}, Stdout: io.Discard, Stderr: io.Discard})
				want := 0
				if strings.HasPrefix(mode, "mixed-") {
					want = 70
					if kind == CommandKindOneShot {
						want = 1
					}
				}
				if mode == "typed-timeout" {
					want = 124
				}
				if got != want {
					t.Fatalf("Execute() = %d, want %d", got, want)
				}
			})
		}
	}
}

type taskErrorCauses []error

func (taskErrorCauses) Error() string       { return "task error causes" }
func (err taskErrorCauses) Unwrap() []error { return []error(err) }

//nolint:errorlint // Exact retained-value identity is the compatibility contract.
func TestCallbackPreservesFiniteErrorsAndCancellation(t *testing.T) {
	deep := errors.New("inner failure")
	for range 65 {
		deep = fmt.Errorf("nested: %w", deep)
	}
	err := invoke("work", "run", func(context.Context) error { return deep }, t.Context())
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
	deepCancellation := context.Canceled
	for range 128 {
		deepCancellation = fmt.Errorf("nested: %w", deepCancellation)
	}
	if !isCancellationResult(ctx, deepCancellation) {
		t.Fatal("finite nested cancellation became failure")
	}
}

func TestCancellationClassificationRetainsFiniteFailuresAndCustomCauses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{nil, taskErrorCauses{}, taskErrorCauses{nil}, taskErrorCauses{context.Canceled, errors.New("failure")}} {
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
