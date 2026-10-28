package service

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	for _, mode := range []string{"mixed-serving-failure", "http-cleanup-deadline", "mixed-task-failure", "pure-wrapped-deadline", "pure-joined-deadline"} {
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
				if mode == "pure-wrapped-deadline" || mode == "pure-joined-deadline" {
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
