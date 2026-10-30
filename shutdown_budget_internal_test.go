package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

type budgetObservation struct {
	entered  time.Time
	deadline time.Time
	err      error
}

// startBudgetRuntime starts two components and one task that needs drain to
// finish after cancellation. Stops record their context and then run stop.
func startBudgetRuntime(
	t *testing.T,
	componentBudget time.Duration,
	drain time.Duration,
	stop func(context.Context) error,
) (*Service, chan budgetObservation) {
	t.Helper()
	observations := make(chan budgetObservation, 2)
	record := func(ctx context.Context) error {
		deadline, _ := ctx.Deadline()
		entered := time.Now()
		err := stop(ctx)
		observations <- budgetObservation{entered: entered, deadline: deadline, err: err}
		return err
	}
	runtime, err := New(Config{Components: []Component{{Name: "first", Stop: record}, {Name: "second", Stop: record}}})
	if err != nil {
		t.Fatal(err)
	}
	runtime.componentStopTimeout = componentBudget
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Go("drain", func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(drain)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return runtime, observations
}

func TestComponentStopBudgetStartsAfterDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime, observations := startBudgetRuntime(t, 10*time.Second, 4*time.Second, func(context.Context) error {
			time.Sleep(4 * time.Second)
			return nil
		})
		began := time.Now()
		ctx, cancel := newShutdownContext(runtime, 5*time.Second)
		defer cancel()
		if err := classifyShutdownError(runtime.Shutdown(ctx)); err != nil {
			t.Errorf("shutdown = %v, want success beyond the 5s total", err)
		}
		if elapsed := time.Since(began); elapsed != 12*time.Second {
			t.Errorf("shutdown took %v, want 4s drain plus two 4s stops", elapsed)
		}
		first, second := <-observations, <-observations
		wantDeadline := began.Add(4 * time.Second).Add(10 * time.Second)
		if !first.deadline.Equal(wantDeadline) || !second.deadline.Equal(wantDeadline) {
			t.Errorf("stop deadlines = %v, %v, want one shared %v", first.deadline, second.deadline, wantDeadline)
		}
		if !first.entered.Equal(began.Add(4 * time.Second)) {
			t.Errorf("component phase began at %v, want after drain", first.entered.Sub(began))
		}
	})
}

func TestComponentStopBudgetExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime, observations := startBudgetRuntime(t, 10*time.Second, 2*time.Second, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		began := time.Now()
		ctx, cancel := newShutdownContext(runtime, 5*time.Second)
		defer cancel()
		err := classifyShutdownError(runtime.Shutdown(ctx))
		var timeoutError *ShutdownTimeoutError
		if !errors.As(err, &timeoutError) {
			t.Errorf("shutdown = %v, want ShutdownTimeoutError", err)
		}
		if elapsed := time.Since(began); elapsed != 12*time.Second {
			t.Errorf("waiter returned after %v, want 2s drain plus 10s component budget", elapsed)
		}
		first := <-observations
		if !errors.Is(first.err, context.DeadlineExceeded) {
			t.Errorf("component context error = %v, want its own deadline", first.err)
		}
		synctest.Wait()
		second := <-observations
		if !errors.Is(second.err, context.DeadlineExceeded) || !second.entered.Equal(began.Add(12*time.Second)) {
			t.Errorf("second stop = %+v, want the shared expired deadline", second)
		}
	})
}

func TestComponentStopBudgetIsNotGrantedAfterDrainTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime, observations := startBudgetRuntime(t, 10*time.Second, 7*time.Second, func(ctx context.Context) error {
			return ctx.Err()
		})
		began := time.Now()
		ctx, cancel := newShutdownContext(runtime, 5*time.Second)
		defer cancel()
		err := classifyShutdownError(runtime.Shutdown(ctx))
		var timeoutError *ShutdownTimeoutError
		if !errors.As(err, &timeoutError) || time.Since(began) != 5*time.Second {
			t.Errorf("shutdown = %v after %v, want timeout at the 5s total", err, time.Since(began))
		}
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		first := <-observations
		// Like the zero-budget path, a late stop sees the expired drain deadline
		// itself, not a cancellation without a deadline or a fresh grant.
		if !first.deadline.Equal(began.Add(5*time.Second)) || !errors.Is(first.err, context.DeadlineExceeded) {
			t.Errorf("late component stop = %+v, want the expired drain deadline", first)
		}
		<-observations
	})
}

func TestComponentStopBudgetExpiryClassifiesAsTimeoutDespiteStopError(t *testing.T) {
	closeErr := errors.New("close failed")
	// The waiter and cleanup race at the deadline; repeat to cover both orders.
	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			runtime, observations := startBudgetRuntime(t, 10*time.Second, 2*time.Second, func(ctx context.Context) error {
				<-ctx.Done()
				return closeErr
			})
			ctx, cancel := newShutdownContext(runtime, 5*time.Second)
			defer cancel()
			err := classifyShutdownError(budgetedShutdown(runtime, ctx))
			var timeoutError *ShutdownTimeoutError
			// Whichever of the waiter's timer and cleanup completion wins, the
			// expired component phase is a timeout, never a plain ShutdownError.
			if !errors.As(err, &timeoutError) {
				t.Errorf("shutdown = %v, want ShutdownTimeoutError", err)
			}
			synctest.Wait()
			<-observations
			<-observations
		})
	}
}

func TestComponentStopBudgetZeroKeepsSingleAllowance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime, observations := startBudgetRuntime(t, 0, 4*time.Second, func(context.Context) error { return nil })
		began := time.Now()
		ctx, cancel := newShutdownContext(runtime, 5*time.Second)
		defer cancel()
		if err := runtime.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		first, second := <-observations, <-observations
		want := began.Add(5 * time.Second)
		if !first.deadline.Equal(want) || !second.deadline.Equal(want) {
			t.Errorf("stop deadlines = %v, %v, want the total allowance %v", first.deadline, second.deadline, want)
		}
	})
}

func TestComponentStopBudgetEscalationCancelsWithoutGrantingTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime, observations := startBudgetRuntime(t, 10*time.Second, 2*time.Second, func(ctx context.Context) error {
			<-ctx.Done()
			return context.Cause(ctx)
		})
		signals := make(chan os.Signal, 1)
		began := time.Now()
		result := make(chan error, 1)
		go func() { result <- shutdownWithEscalation(runtime, 5*time.Second, signals) }()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		signals <- os.Interrupt
		err := <-result
		if err == nil || time.Since(began) != 3*time.Second {
			t.Errorf("escalated shutdown = %v after %v, want prompt canceled cleanup", err, time.Since(began))
		}
		first, second := <-observations, <-observations
		if !errors.Is(first.err, context.Canceled) || !errors.Is(second.err, context.Canceled) {
			t.Errorf("escalated stops = %v, %v, want cancellation", first.err, second.err)
		}
	})
}

func TestComponentStopBudgetDrainEscalationCancelsStopsSynchronously(t *testing.T) {
	// The propagation race is narrow; repeat to cover scheduler orders.
	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			runtime, observations := startBudgetRuntime(t, 10*time.Second, 4*time.Second, func(ctx context.Context) error {
				// Observe the context at entry, before any scheduling point.
				return ctx.Err()
			})
			signals := make(chan os.Signal, 1)
			result := make(chan error, 1)
			go func() { result <- shutdownWithEscalation(runtime, 5*time.Second, signals) }()
			time.Sleep(time.Second)
			synctest.Wait()
			signals <- os.Interrupt
			<-result
			first, second := <-observations, <-observations
			if !errors.Is(first.err, context.Canceled) || !errors.Is(second.err, context.Canceled) {
				t.Errorf("stops after drain escalation = %v, %v, want already canceled", first.err, second.err)
			}
		})
	}
}

func TestShutdownDeadlineFollowsPhase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime := &Service{componentStopTimeout: 10 * time.Second}
		began := time.Now()
		ctx, cancel := newShutdownContext(runtime, 5*time.Second)
		defer cancel()
		if deadline, ok := shutdownDeadline(ctx); !ok || !deadline.Equal(began.Add(5*time.Second)) {
			t.Fatalf("drain deadline = %v, %t", deadline, ok)
		}
		time.Sleep(time.Second)
		components, release := beginComponentStop(ctx)
		defer release()
		want := began.Add(11 * time.Second)
		if deadline, _ := shutdownDeadline(ctx); !deadline.Equal(want) {
			t.Fatalf("component phase waiter deadline = %v, want %v", deadline, want)
		}
		if deadline, _ := components.Deadline(); !deadline.Equal(want) {
			t.Fatalf("component context deadline = %v, want %v", deadline, want)
		}
		time.Sleep(time.Second)
		again, releaseAgain := beginComponentStop(ctx)
		defer releaseAgain()
		if deadline, _ := again.Deadline(); !deadline.Equal(want) {
			t.Fatalf("second phase start deadline = %v, want the existing %v", deadline, want)
		}
	})
}
