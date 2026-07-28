package platform_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
)

func TestWorkerCandidatesPreserveEquivalentBehavior(t *testing.T) {
	for _, candidate := range workerCandidates() {
		t.Run(candidate.name, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			candidate.start(t, fixture)
			var previous correlation.RequestID
			for range 3 {
				result, err := fixture.dispatch(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if result.CorrelationID != fixture.parent.CorrelationID {
					t.Fatalf(
						"correlation ID = %q, want %q",
						result.CorrelationID,
						fixture.parent.CorrelationID,
					)
				}
				if result.CausationID.String() != fixture.parent.RequestID.String() {
					t.Fatalf(
						"causation ID = %q, want %q",
						result.CausationID,
						fixture.parent.RequestID,
					)
				}
				if result.RequestID == fixture.parent.RequestID ||
					result.RequestID == previous {
					t.Fatalf("request ID was not fresh: %q", result.RequestID)
				}
				previous = result.RequestID
			}
		})
	}
}

type workerResult struct {
	values correlation.Values
	err    error
}

type workerFixture struct {
	factory *correlation.Factory
	parent  correlation.Values
	jobs    chan struct{}
	results chan workerResult
	ready   chan struct{}
}

func newWorkerFixture(testingObject testing.TB) *workerFixture {
	testingObject.Helper()
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		testingObject.Fatal(err)
	}
	parent, err := factory.Start()
	if err != nil {
		testingObject.Fatal(err)
	}

	return &workerFixture{
		factory: factory,
		parent:  parent,
		jobs:    make(chan struct{}),
		results: make(chan workerResult),
		ready:   make(chan struct{}),
	}
}

func (fixture *workerFixture) run(ctx context.Context) error {
	close(fixture.ready)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-fixture.jobs:
			values, err := fixture.factory.Next(fixture.parent)
			select {
			case fixture.results <- workerResult{values: values, err: err}:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (fixture *workerFixture) dispatch(ctx context.Context) (correlation.Values, error) {
	select {
	case fixture.jobs <- struct{}{}:
	case <-ctx.Done():
		return correlation.Values{}, context.Cause(ctx)
	}
	select {
	case result := <-fixture.results:
		return result.values, result.err
	case <-ctx.Done():
		return correlation.Values{}, context.Cause(ctx)
	}
}

type workerCandidate struct {
	name  string
	start func(testing.TB, *workerFixture)
}

func workerCandidates() []workerCandidate {
	return []workerCandidate{
		{name: "low-level-service", start: startLowLevelWorker},
		{name: "cohesive-service", start: startCohesiveWorker},
	}
}

func startLowLevelWorker(testingObject testing.TB, fixture *workerFixture) {
	testingObject.Helper()
	runtime, err := service.New(service.Config{})
	if err != nil {
		testingObject.Fatal(err)
	}
	if err := runtime.Start(testingObject.Context()); err != nil {
		testingObject.Fatal(err)
	}
	if err := runtime.Go("benchmark-worker", fixture.run); err != nil {
		testingObject.Fatal(err)
	}
	awaitWorkerStart(testingObject, fixture.ready, nil)
	testingObject.Cleanup(func() {
		if err := runtime.Drain(); err != nil {
			testingObject.Error(err)
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Shutdown(shutdownContext); err != nil {
			testingObject.Error(err)
		}
	})
}

func startCohesiveWorker(testingObject testing.TB, fixture *workerFixture) {
	testingObject.Helper()
	listener, err := (&net.ListenConfig{}).Listen(
		testingObject.Context(),
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		testingObject.Fatal(err)
	}
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name:    "worker",
		Summary: "run the equivalent worker fixture",
		Kind:    service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "benchmark-worker",
				Run:  fixture.run,
			}}}, nil
		},
	})
	go func() {
		done <- service.Execute(runContext, service.Definition{
			Identity: service.Identity{Name: "worker-benchmark"},
			Commands: service.Commands{Worker: command},
			Management: service.Management{
				Listener: listener,
			},
		}, service.Invocation{
			Args:        []string{"worker"},
			Environment: []string{},
			Stdout:      io.Discard,
			Stderr:      io.Discard,
		})
	}()
	awaitWorkerStart(testingObject, fixture.ready, done)
	testingObject.Cleanup(func() {
		cancel()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case code := <-done:
			if code != 0 {
				testingObject.Errorf("cohesive worker exit code = %d, want 0", code)
			}
		case <-timer.C:
			testingObject.Error("cohesive worker did not stop")
		}
	})
}

func awaitWorkerStart(
	testingObject testing.TB,
	ready <-chan struct{},
	done <-chan int,
) {
	testingObject.Helper()
	select {
	case <-ready:
	case code := <-done:
		testingObject.Fatalf("worker exited during startup with code %d", code)
	case <-testingObject.Context().Done():
		testingObject.Fatal(context.Cause(testingObject.Context()))
	}
}

func benchmarkEquivalentWorkerCandidates(benchmark *testing.B) {
	for _, candidate := range workerCandidates() {
		benchmark.Run(candidate.name, func(benchmark *testing.B) {
			fixture := newWorkerFixture(benchmark)
			candidate.start(benchmark, fixture)
			benchmark.ReportAllocs()
			benchmark.ResetTimer()
			for benchmark.Loop() {
				if _, err := fixture.dispatch(benchmark.Context()); err != nil {
					benchmark.Fatal(err)
				}
			}
		})
	}
}
