package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

type shutdownBudgetKey struct{}

// shutdownBudget is the two-phase shutdown allowance selected by a positive
// Plan.ComponentStopTimeout. Its mutex owns the phase transition so the drain
// deadline and the component deadline can never both apply.
type shutdownBudget struct {
	cancel    context.CancelCauseFunc
	component time.Duration

	mu         sync.Mutex
	timer      *time.Timer
	deadline   time.Time
	components bool
}

// newShutdownContext returns the context a platform shutdown waiter passes to
// Service.Shutdown. Without a component budget it is the single total
// allowance. With one, total bounds only the work before component stopping;
// the first component Stop replaces it with a fresh component deadline.
func newShutdownContext(runtime *Service, total time.Duration) (context.Context, context.CancelFunc) {
	component := runtime.componentStopTimeout
	if component <= 0 {
		return context.WithTimeout(context.Background(), total)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	budget := &shutdownBudget{cancel: cancel, component: component, deadline: time.Now().Add(total)}
	budget.timer = time.AfterFunc(total, budget.expire)

	return context.WithValue(ctx, shutdownBudgetKey{}, budget), func() {
		budget.mu.Lock()
		budget.timer.Stop()
		budget.mu.Unlock()
		cancel(context.Canceled)
	}
}

// The waiter context has no native deadline because the phase deadline moves;
// its cause still reports DeadlineExceeded so timeouts classify unchanged.
func (budget *shutdownBudget) expire() {
	budget.cancel(context.DeadlineExceeded)
}

// shutdownDeadline reports the currently effective waiter deadline.
func shutdownDeadline(ctx context.Context) (time.Time, bool) {
	budget, ok := ctx.Value(shutdownBudgetKey{}).(*shutdownBudget)
	if !ok {
		return ctx.Deadline()
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()

	return budget.deadline, true
}

// beginComponentStop derives the shared context for one component-stop
// phase. A drain that already exhausted its allowance, or an escalation that
// canceled cleanup, does not earn a fresh component deadline; those stops get
// the current deadline, as the zero-budget path's stops do.
func beginComponentStop(ctx context.Context) (context.Context, context.CancelFunc) {
	budget, ok := ctx.Value(shutdownBudgetKey{}).(*shutdownBudget)
	if !ok {
		return ctx, func() {}
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if !budget.components && ctx.Err() == nil && budget.timer.Stop() {
		budget.components = true
		budget.deadline = time.Now().Add(budget.component)
		budget.timer = time.AfterFunc(budget.component, budget.expire)
	}

	return withPhaseDeadline(ctx, budget.deadline)
}

// withPhaseDeadline gives stops a native deadline. The waiter's own expiry
// fires at the same instant; detaching from it keeps stops observing
// DeadlineExceeded rather than a racing cancellation, while an explicit
// cancellation such as escalation still reaches them.
func withPhaseDeadline(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	detached, cancelDetached := context.WithCancelCause(context.WithoutCancel(ctx))
	phase, cancelPhase := context.WithDeadline(detached, deadline)
	stopPropagation := context.AfterFunc(ctx, func() {
		if cause := context.Cause(ctx); !errors.Is(cause, context.DeadlineExceeded) {
			cancelDetached(cause)
		}
	})

	return phase, func() {
		stopPropagation()
		cancelPhase()
		cancelDetached(nil)
	}
}

// budgetedShutdown runs Service.Shutdown and keeps an expired component phase
// classified as a timeout. A stop can return its own error as its deadline
// passes and complete cleanup before the waiter's timer fires; that result
// still exceeded the budget.
func budgetedShutdown(runtime *Service, ctx context.Context) error {
	err := runtime.Shutdown(ctx)
	budget, ok := ctx.Value(shutdownBudgetKey{}).(*shutdownBudget)
	if !ok || err == nil || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	budget.mu.Lock()
	expired := budget.components && !time.Now().Before(budget.deadline)
	budget.mu.Unlock()
	if expired {
		return errors.Join(context.DeadlineExceeded, err)
	}

	return err
}
