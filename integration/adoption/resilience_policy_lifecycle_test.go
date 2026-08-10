package adoption_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	throttle "github.com/faustbrian/golib/pkg/adaptive-throttle"
	"github.com/faustbrian/golib/pkg/bulkhead"
	breaker "github.com/faustbrian/golib/pkg/circuit-breaker"
	concurrencylimit "github.com/faustbrian/golib/pkg/concurrency-limit"
	"github.com/faustbrian/golib/pkg/resilience"
	"github.com/faustbrian/golib/pkg/retry"
	"github.com/faustbrian/golib/pkg/service"
	serviceintegration "github.com/faustbrian/golib/pkg/service/integration"
)

func TestResiliencePoliciesRemainReadyThroughBoundedDependencyFailureAndOverload(t *testing.T) {
	t.Parallel()

	outage := errors.New("inventory backend unavailable")
	policies := newLifecyclePolicies(t, outage)
	runtime := newLifecycleRuntime(t, policies)

	assertColdPolicyState(t, runtime, policies)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !runtime.StartupComplete() || !runtime.Ready() || runtime.State() != service.StateReady {
		t.Fatalf("started state = %s, startup = %t, ready = %t", runtime.State(), runtime.StartupComplete(), runtime.Ready())
	}

	var downstreamCalls atomic.Uint64
	result := policies.execute(context.Background(), func(context.Context) error {
		downstreamCalls.Add(1)

		return retry.Retryable(outage)
	})
	if !errors.Is(result.err, outage) {
		t.Fatalf("execute() error = %v, want outage", result.err)
	}
	if result.retry.Attempts != 2 || result.retry.Reason != retry.ReasonAttemptsExhausted {
		t.Fatalf("retry result = %+v, want two bounded attempts", result.retry)
	}
	if result.budget.AdditionalAdmitted != 1 || result.budget.AdditionalActive != 0 || result.budget.AdditionalRecent != 1 {
		t.Fatalf("budget snapshot = %+v, want one settled retry", result.budget)
	}
	if downstreamCalls.Load() != 2 {
		t.Fatalf("downstream calls = %d, want 2", downstreamCalls.Load())
	}

	breakerSnapshot := policies.breaker.Snapshot()
	if breakerSnapshot.Failures != 2 || breakerSnapshot.Completed != 2 || breakerSnapshot.State != breaker.StateClosed {
		t.Fatalf("breaker snapshot = %+v, want two failures without an implicit restart", breakerSnapshot)
	}
	limiterSnapshot := policies.limiter.Snapshot()
	if limiterSnapshot.Outcomes.Overload != 2 || limiterSnapshot.InFlight != 0 {
		t.Fatalf("limiter snapshot = %+v, want two settled overload attempts", limiterSnapshot)
	}
	bulkheadSnapshot := policies.bulkhead.Snapshot()
	if bulkheadSnapshot.Executions != 1 || bulkheadSnapshot.ActiveWeight != 0 {
		t.Fatalf("bulkhead snapshot = %+v, want one settled logical execution", bulkheadSnapshot)
	}
	throttleSnapshot, ok := policies.throttler.Snapshot(policies.resource)
	if !ok || throttleSnapshot.Requests != 1 || throttleSnapshot.Overloads != 1 || throttleSnapshot.Samples != 1 || throttleSnapshot.RejectionProbability == 0 {
		t.Fatalf("throttle snapshot = %+v, %t, want retained overload pressure", throttleSnapshot, ok)
	}
	if !runtime.Ready() || policies.starts.Load() != 1 || policies.stops.Load() != 0 {
		t.Fatalf("dependency failure changed liveness: ready = %t, starts = %d, stops = %d", runtime.Ready(), policies.starts.Load(), policies.stops.Load())
	}

	result = policies.execute(context.Background(), func(context.Context) error {
		downstreamCalls.Add(1)

		return nil
	})
	if !errors.Is(result.err, throttle.ErrRejected) {
		t.Fatalf("overloaded execute() error = %v, want local throttle rejection", result.err)
	}
	if downstreamCalls.Load() != 2 {
		t.Fatalf("locally rejected request reached downstream; calls = %d", downstreamCalls.Load())
	}
	throttleSnapshot, _ = policies.throttler.Snapshot(policies.resource)
	if throttleSnapshot.Requests != 2 || throttleSnapshot.LocalRejections != 1 || throttleSnapshot.Samples != 1 {
		t.Fatalf("throttle snapshot after shedding = %+v", throttleSnapshot)
	}
	if !runtime.Ready() {
		t.Fatal("local overload rejection made the service unready")
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated Shutdown() error = %v", err)
	}
	if runtime.State() != service.StateStopped || runtime.Ready() || !runtime.StartupComplete() {
		t.Fatalf("terminal state = %s, startup = %t, ready = %t", runtime.State(), runtime.StartupComplete(), runtime.Ready())
	}
	if policies.starts.Load() != 1 || policies.stops.Load() != 1 {
		t.Fatalf("lifecycle calls = start %d, stop %d, want exactly once", policies.starts.Load(), policies.stops.Load())
	}
	if snapshot := policies.bulkhead.Snapshot(); !snapshot.Draining || !snapshot.Drained {
		t.Fatalf("bulkhead shutdown snapshot = %+v", snapshot)
	}
	if snapshot := policies.limiter.Snapshot(); !snapshot.Draining {
		t.Fatalf("limiter shutdown snapshot = %+v", snapshot)
	}
	if _, err := policies.bulkhead.Acquire(context.Background(), 1); !errors.Is(err, bulkhead.ErrClosed) {
		t.Fatalf("bulkhead admission after shutdown error = %v, want ErrClosed", err)
	}
	if _, err := policies.limiter.Acquire(context.Background()); !errors.Is(err, concurrencylimit.ErrDraining) {
		t.Fatalf("limiter admission after shutdown error = %v, want ErrDraining", err)
	}
}

func TestDrainUnblocksQueuedPoliciesAndReportsUncooperativeActiveWork(t *testing.T) {
	t.Parallel()

	policies := newLifecyclePolicies(t, errors.New("unused outage"))
	runtime := newLifecycleRuntime(t, policies)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	operationEntered := make(chan struct{})
	releaseOperation := make(chan struct{})
	activeResult := make(chan lifecycleExecutionResult, 1)
	go func() {
		activeResult <- policies.execute(context.Background(), func(context.Context) error {
			close(operationEntered)
			<-releaseOperation

			return nil
		})
	}()
	<-operationEntered

	outboundWaiter := make(chan error, 1)
	go func() {
		permit, err := policies.limiter.Acquire(context.Background())
		if permit != nil {
			_ = permit.Complete(concurrencylimit.OutcomeIgnored)
		}
		outboundWaiter <- err
	}()
	<-policies.limiterQueued
	if snapshot := policies.limiter.Snapshot(); snapshot.InFlight != 1 || snapshot.Queued != 1 {
		t.Fatalf("limiter pre-drain snapshot = %+v, want active and queued outbound work", snapshot)
	}

	inboundWaiter := make(chan lifecycleExecutionResult, 1)
	go func() {
		inboundWaiter <- policies.execute(context.Background(), func(context.Context) error {
			return errors.New("queued inbound work must not execute")
		})
	}()
	<-policies.bulkheadQueued
	if snapshot := policies.bulkhead.Snapshot(); snapshot.ActiveWeight != 1 || snapshot.QueueDepth != 1 {
		t.Fatalf("bulkhead pre-drain snapshot = %+v, want active and queued inbound work", snapshot)
	}

	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if runtime.Ready() || runtime.State() != service.StateDraining {
		t.Fatalf("drain state = %s, ready = %t", runtime.State(), runtime.Ready())
	}
	if err := <-outboundWaiter; !errors.Is(err, concurrencylimit.ErrDraining) {
		t.Fatalf("queued outbound error = %v, want ErrDraining", err)
	}
	if result := <-inboundWaiter; !errors.Is(result.err, bulkhead.ErrClosed) {
		t.Fatalf("queued inbound error = %v, want ErrClosed", result.err)
	}
	if _, err := policies.bulkhead.Acquire(context.Background(), 1); !errors.Is(err, bulkhead.ErrClosed) {
		t.Fatalf("new inbound admission error = %v, want ErrClosed", err)
	}
	if _, err := policies.limiter.Acquire(context.Background()); !errors.Is(err, concurrencylimit.ErrDraining) {
		t.Fatalf("new outbound admission error = %v, want ErrDraining", err)
	}

	deadline := newControlledDeadlineContext()
	firstShutdown := make(chan error, 1)
	go func() { firstShutdown <- runtime.Shutdown(deadline) }()
	<-policies.stopEntered
	deadline.expire()
	if err := <-firstShutdown; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline-bounded Shutdown() error = %v, want DeadlineExceeded", err)
	}

	shutdownErr := runtime.Shutdown(context.Background())
	if !errors.Is(shutdownErr, bulkhead.ErrDrainIncomplete) || !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("completed Shutdown() error = %v, want incomplete deadline-bounded drain", shutdownErr)
	}
	if repeated := runtime.Shutdown(context.Background()); repeated != shutdownErr {
		t.Fatalf("repeated Shutdown() error = %v, want cached %v", repeated, shutdownErr)
	}
	if snapshot := policies.bulkhead.Snapshot(); snapshot.ActiveWeight != 1 || !snapshot.Draining || snapshot.Drained {
		t.Fatalf("uncooperative bulkhead snapshot = %+v, want honestly retained active work", snapshot)
	}
	if snapshot := policies.limiter.Snapshot(); snapshot.InFlight != 1 || !snapshot.Draining {
		t.Fatalf("uncooperative limiter snapshot = %+v, want honestly retained active attempt", snapshot)
	}
	if runtime.State() != service.StateStopped || runtime.Ready() || !runtime.StartupComplete() {
		t.Fatalf("post-deadline state = %s, startup = %t, ready = %t", runtime.State(), runtime.StartupComplete(), runtime.Ready())
	}

	close(releaseOperation)
	if result := <-activeResult; result.err != nil {
		t.Fatalf("active execution completion error = %v", result.err)
	}
	if snapshot := policies.bulkhead.Snapshot(); snapshot.ActiveWeight != 0 || !snapshot.Drained {
		t.Fatalf("settled bulkhead snapshot = %+v", snapshot)
	}
	if snapshot := policies.limiter.Snapshot(); snapshot.InFlight != 0 || snapshot.Outcomes.Success != 1 {
		t.Fatalf("settled limiter snapshot = %+v", snapshot)
	}
	if repeated := runtime.Shutdown(context.Background()); repeated != shutdownErr {
		t.Fatalf("late repeated Shutdown() error = %v, want original honest result %v", repeated, shutdownErr)
	}
}

type lifecyclePolicies struct {
	resource        string
	bulkhead        *bulkhead.Bulkhead
	breaker         *breaker.Breaker
	limiter         *concurrencylimit.Limiter
	throttler       *throttle.Throttler
	retry           *retry.Policy
	budget          *resilience.Budget
	component       service.Component
	bulkheadQueued  <-chan struct{}
	limiterQueued   <-chan struct{}
	stopEntered     <-chan struct{}
	starts          atomic.Uint64
	stops           atomic.Uint64
	nextExecutionID atomic.Uint64
}

type lifecycleExecutionResult struct {
	err    error
	retry  retry.Result
	budget resilience.BudgetSnapshot
}

func newLifecyclePolicies(t *testing.T, overload error) *lifecyclePolicies {
	t.Helper()

	const resource = "inventory-backend"
	startedAt := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	clock := fixedLifecycleClock{now: startedAt}
	bulkheadQueued := make(chan struct{}, 1)
	limiterQueued := make(chan struct{}, 1)
	stopEntered := make(chan struct{})
	bulkheadClock := bulkheadLifecycleClock{fixedLifecycleClock: clock, queued: bulkheadQueued}
	limiterClock := limiterLifecycleClock{fixedLifecycleClock: clock, queued: limiterQueued}
	breakerClock := breakerLifecycleClock{fixedLifecycleClock: clock}

	policies := &lifecyclePolicies{
		resource:       resource,
		bulkheadQueued: bulkheadQueued,
		limiterQueued:  limiterQueued,
		stopEntered:    stopEntered,
	}
	var err error
	policies.bulkhead, err = bulkhead.New(bulkhead.Config{
		Resource: resource,
		Capacity: 1,
		Admission: bulkhead.Wait{
			MaxQueued: 2,
			MaxWait:   time.Hour,
		},
		Clock:          bulkheadClock,
		PolicyRevision: "lifecycle-v1",
	})
	if err != nil {
		t.Fatalf("bulkhead.New() error = %v", err)
	}
	policies.breaker, err = breaker.New(breaker.Config{
		Name:  resource,
		Clock: breakerClock,
	})
	if err != nil {
		t.Fatalf("breaker.New() error = %v", err)
	}
	policies.limiter, err = concurrencylimit.New(concurrencylimit.Config{
		MinLimit:     1,
		MaxLimit:     1,
		InitialLimit: 1,
		Algorithm:    concurrencylimit.NewFixedAlgorithm(),
		Queue: concurrencylimit.QueueConfig{
			MaxQueued: 2,
			MaxWait:   time.Hour,
		},
		Clock: limiterClock,
		Classifier: func(completion concurrencylimit.Completion) concurrencylimit.Outcome {
			switch {
			case completion.Context != nil && completion.Context.Err() != nil:
				return concurrencylimit.OutcomeIgnored
			case errors.Is(completion.Err, overload):
				return concurrencylimit.OutcomeOverload
			case completion.Err != nil:
				return concurrencylimit.OutcomeDependencyFailure
			default:
				return concurrencylimit.OutcomeSuccess
			}
		},
	})
	if err != nil {
		t.Fatalf("concurrencylimit.New() error = %v", err)
	}
	adaptivePolicy, err := throttle.NewPolicy(throttle.PolicyConfig{
		Revision:                    "lifecycle-v1",
		MinimumSamples:              1,
		Algorithm:                   throttle.GoogleSRE{AcceptMultiplier: 1},
		MaxRejectionProbability:     0.9,
		MinimumAdmissionProbability: 0.1,
		MaxResources:                1,
		Clock:                       clock,
		Random:                      zeroLifecycleRandom{},
		Classifier: func(completion throttle.Completion) throttle.Classification {
			switch {
			case completion.Err == nil:
				return throttle.Classification{Outcome: throttle.Accepted, Reason: throttle.ReasonSuccess}
			case errors.Is(completion.Err, overload):
				return throttle.Classification{Outcome: throttle.DownstreamOverload, Reason: throttle.ReasonExplicitOverload}
			case completion.Context != nil && completion.Context.Err() != nil:
				return throttle.Classification{Outcome: throttle.Ignored, Reason: throttle.ReasonCallerCanceled}
			default:
				return throttle.Classification{Outcome: throttle.DownstreamFailure, Reason: throttle.ReasonDownstreamFailure}
			}
		},
	})
	if err != nil {
		t.Fatalf("throttle.NewPolicy() error = %v", err)
	}
	policies.throttler, err = throttle.New(adaptivePolicy)
	if err != nil {
		t.Fatalf("throttle.New() error = %v", err)
	}
	policies.retry, err = retry.NewPolicy(retry.Config{
		Backoff:             retry.Constant(0),
		MaxAttempts:         2,
		Clock:               clock,
		Sleeper:             immediateLifecycleSleeper{},
		Classifier:          retry.RetryableClassifier(),
		HistoryLimit:        2,
		UseResilienceBudget: true,
	})
	if err != nil {
		t.Fatalf("retry.NewPolicy() error = %v", err)
	}
	policies.budget, err = resilience.NewBudget(resilience.BudgetConfig{
		MaxResources:              1,
		MaxAdditionalPerExecution: 1,
		MaxConcurrentAdditional:   1,
		MaxAdditionalPerWindow:    8,
		AdditionalWindow:          time.Hour,
		PermitTTL:                 time.Hour,
		Clock:                     clock,
	})
	if err != nil {
		t.Fatalf("resilience.NewBudget() error = %v", err)
	}

	var stopOnce sync.Once
	policies.component, err = serviceintegration.New(resource+"-policies", serviceintegration.Hooks{
		Start: func(context.Context) error {
			policies.starts.Add(1)

			return nil
		},
		CloseAdmission: func() error {
			policies.limiter.BeginDrain()

			return policies.bulkhead.Close()
		},
		Stop: func(ctx context.Context) error {
			policies.stops.Add(1)
			stopOnce.Do(func() { close(stopEntered) })

			return errors.Join(policies.bulkhead.Drain(ctx), policies.breaker.Shutdown(ctx))
		},
	})
	if err != nil {
		t.Fatalf("integration.New() error = %v", err)
	}

	return policies
}

func newLifecycleRuntime(t *testing.T, policies *lifecyclePolicies) *service.Service {
	t.Helper()

	runtime, err := service.New(service.Config{Components: []service.Component{policies.component}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}

	return runtime
}

func assertColdPolicyState(t *testing.T, runtime *service.Service, policies *lifecyclePolicies) {
	t.Helper()

	if runtime.State() != service.StateNew || runtime.Ready() || runtime.StartupComplete() {
		t.Fatalf("cold service state = %s, startup = %t, ready = %t", runtime.State(), runtime.StartupComplete(), runtime.Ready())
	}
	if snapshot := policies.bulkhead.Snapshot(); snapshot.Admissions != 0 || snapshot.Executions != 0 || snapshot.Draining {
		t.Fatalf("cold bulkhead snapshot = %+v", snapshot)
	}
	if snapshot := policies.breaker.Snapshot(); snapshot.State != breaker.StateClosed || snapshot.Admitted != 0 || snapshot.Completed != 0 {
		t.Fatalf("cold breaker snapshot = %+v", snapshot)
	}
	if snapshot := policies.limiter.Snapshot(); snapshot.Generation != 1 || snapshot.InFlight != 0 || snapshot.Samples != 0 || snapshot.Draining {
		t.Fatalf("cold limiter snapshot = %+v", snapshot)
	}
	if snapshots := policies.throttler.Snapshots(); len(snapshots) != 0 {
		t.Fatalf("cold throttle snapshots = %+v", snapshots)
	}
}

func (policies *lifecyclePolicies) execute(
	ctx context.Context,
	operation func(context.Context) error,
) lifecycleExecutionResult {
	logicalID := fmt.Sprintf("request-%d", policies.nextExecutionID.Add(1))
	metadata, err := resilience.NewMetadata(logicalID, "inventory.fetch", policies.resource)
	if err != nil {
		return lifecycleExecutionResult{err: err}
	}
	scope, budgetContext, err := policies.budget.Start(ctx, metadata)
	if err != nil {
		return lifecycleExecutionResult{err: err}
	}

	var retryResult retry.Result
	_, executionErr := throttle.Execute(budgetContext, policies.throttler, policies.resource, func(ctx context.Context) (struct{}, error) {
		value, _, bulkheadErr := bulkhead.Execute(ctx, policies.bulkhead, 1, func(ctx context.Context) (struct{}, error) {
			value, result, retryErr := retry.Do(ctx, policies.retry, func(ctx context.Context) (struct{}, error) {
				return breaker.Execute(ctx, policies.breaker, func(ctx context.Context) (struct{}, error) {
					return concurrencylimit.Execute(ctx, policies.limiter, func(ctx context.Context) (struct{}, error) {
						return struct{}{}, operation(ctx)
					})
				})
			})
			retryResult = result

			return value, retryErr
		})

		return value, bulkheadErr
	})
	budgetSnapshot := scope.Snapshot()
	closeErr := scope.Close()

	return lifecycleExecutionResult{
		err:    errors.Join(executionErr, closeErr),
		retry:  retryResult,
		budget: budgetSnapshot,
	}
}

type fixedLifecycleClock struct{ now time.Time }

func (clock fixedLifecycleClock) Now() time.Time { return clock.now }

type dormantLifecycleTimer struct{ channel <-chan time.Time }

func (timer dormantLifecycleTimer) C() <-chan time.Time { return timer.channel }
func (dormantLifecycleTimer) Stop() bool                { return true }

type bulkheadLifecycleClock struct {
	fixedLifecycleClock
	queued chan<- struct{}
}

func (clock bulkheadLifecycleClock) NewTimer(time.Duration) bulkhead.Timer {
	signalLifecycleQueue(clock.queued)

	return dormantLifecycleTimer{channel: make(chan time.Time)}
}

type limiterLifecycleClock struct {
	fixedLifecycleClock
	queued chan<- struct{}
}

func (clock limiterLifecycleClock) NewTimer(time.Duration) concurrencylimit.Timer {
	signalLifecycleQueue(clock.queued)

	return dormantLifecycleTimer{channel: make(chan time.Time)}
}

type breakerLifecycleClock struct{ fixedLifecycleClock }

func (breakerLifecycleClock) NewTimer(time.Duration) breaker.Timer {
	return dormantLifecycleTimer{channel: make(chan time.Time)}
}

func signalLifecycleQueue(queue chan<- struct{}) {
	select {
	case queue <- struct{}{}:
	default:
	}
}

type zeroLifecycleRandom struct{}

func (zeroLifecycleRandom) Float64() float64 { return 0 }

type immediateLifecycleSleeper struct{}

func (immediateLifecycleSleeper) Sleep(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

type controlledDeadlineContext struct {
	done chan struct{}
	once sync.Once
}

func newControlledDeadlineContext() *controlledDeadlineContext {
	return &controlledDeadlineContext{done: make(chan struct{})}
}

func (*controlledDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *controlledDeadlineContext) Done() <-chan struct{}   { return ctx.done }
func (*controlledDeadlineContext) Value(any) any               { return nil }

func (ctx *controlledDeadlineContext) Err() error {
	select {
	case <-ctx.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (ctx *controlledDeadlineContext) expire() { ctx.once.Do(func() { close(ctx.done) }) }
