package adoption_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestServiceRolesAdoptExplicitNamedPolicyCompositions(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"api", "rpc", "worker", "scheduler", "one-shot"} {
		t.Run(role, func(t *testing.T) {
			policies := newRolePolicies(t, role)
			runtime, err := service.New(service.Config{
				Components: []service.Component{policies.component},
			})
			if err != nil {
				t.Fatalf("service.New() error = %v", err)
			}
			if err := runtime.Start(context.Background()); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			total, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := policies.execute(total); err != nil {
				t.Fatalf("execute() error = %v", err)
			}
			if err := runtime.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown() error = %v", err)
			}

			if snapshot := policies.limiter.Snapshot(); snapshot.Outcomes.Success != 1 {
				t.Fatalf("limiter snapshot = %+v", snapshot)
			}
			if snapshot := policies.breaker.Snapshot(); snapshot.Successes != 1 {
				t.Fatalf("breaker snapshot = %+v", snapshot)
			}
			if policies.inbound {
				if snapshot := policies.bulkhead.Snapshot(); snapshot.Admissions != 1 || snapshot.ActiveWeight != 0 {
					t.Fatalf("bulkhead snapshot = %+v", snapshot)
				}
				if snapshot, ok := policies.throttler.Snapshot(policies.name); !ok || snapshot.Requests != 1 {
					t.Fatalf("throttle snapshot = %+v, %t", snapshot, ok)
				}
				if policies.priorityCalls.Load() != 1 {
					t.Fatalf("trusted priority resolutions = %d, want 1", policies.priorityCalls.Load())
				}
			} else if snapshots := policies.throttler.Snapshots(); len(snapshots) != 0 {
				t.Fatalf("outbound-only role retained inbound throttle state: %+v", snapshots)
			}
		})
	}
}

func TestStatefulPolicyLifecycleReleasesBlockedWaitersAndDrainsActiveAttempts(t *testing.T) {
	t.Parallel()

	policy, err := bulkhead.New(bulkhead.Config{
		Resource:  "inventory-db",
		Capacity:  1,
		Admission: bulkhead.Wait{MaxQueued: 1, MaxWait: time.Minute},
	})
	if err != nil {
		t.Fatalf("bulkhead.New() error = %v", err)
	}
	component, err := serviceintegration.New("inventory-db-policies", serviceintegration.Hooks{
		CloseAdmission: policy.Close,
		Stop:           policy.Drain,
	})
	if err != nil {
		t.Fatalf("integration.New() error = %v", err)
	}
	runtime, err := service.New(service.Config{Components: []service.Component{component}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	holder, err := policy.Acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("holder Acquire() error = %v", err)
	}
	waiterResult := make(chan error, 1)
	go func() {
		_, acquireErr := policy.Acquire(context.Background(), 1)
		waiterResult <- acquireErr
	}()
	deadline := time.Now().Add(time.Second)
	for policy.Snapshot().QueueDepth != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if policy.Snapshot().QueueDepth != 1 {
		t.Fatal("waiter did not enter the bounded queue")
	}
	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if err := <-waiterResult; !errors.Is(err, bulkhead.ErrClosed) {
		t.Fatalf("waiter error = %v, want ErrClosed", err)
	}
	if err := holder.Release(); err != nil {
		t.Fatalf("holder Release() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}

func TestNamedPolicyConstructionRejectsUnboundedIdentity(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", strings.Repeat("p", resilience.MaxIdentityLength+1)} {
		if _, err := buildRolePolicies(name, false); err == nil {
			t.Fatalf("buildRolePolicies(%q) succeeded", name)
		}
	}
}

type rolePolicies struct {
	name          string
	component     service.Component
	bulkhead      *bulkhead.Bulkhead
	breaker       *breaker.Breaker
	limiter       *concurrencylimit.Limiter
	throttler     *throttle.Throttler
	retry         *retry.Policy
	budget        *resilience.Budget
	inbound       bool
	priorityCalls atomic.Uint64
}

func newRolePolicies(t *testing.T, role string) *rolePolicies {
	t.Helper()

	policies, err := buildRolePolicies("downstream:"+role, role == "api" || role == "rpc")
	if err != nil {
		t.Fatalf("buildRolePolicies() error = %v", err)
	}

	return policies
}

func buildRolePolicies(name string, inbound bool) (*rolePolicies, error) {
	if name == "" || len(name) > resilience.MaxIdentityLength {
		return nil, fmt.Errorf("policy identity must contain at most %d bytes", resilience.MaxIdentityLength)
	}
	policySet := &rolePolicies{name: name, inbound: inbound}
	var err error
	policySet.bulkhead, err = bulkhead.New(bulkhead.Config{Resource: name, Capacity: 2})
	if err != nil {
		return nil, err
	}
	policySet.breaker, err = breaker.New(breaker.Config{Name: name})
	if err != nil {
		return nil, err
	}
	policySet.limiter, err = concurrencylimit.New(concurrencylimit.Config{
		MinLimit: 1, MaxLimit: 2, InitialLimit: 2,
		Algorithm: concurrencylimit.NewFixedAlgorithm(),
	})
	if err != nil {
		return nil, err
	}
	adaptivePolicy, err := throttle.NewPolicy(throttle.PolicyConfig{
		Revision:                    name,
		MaxRejectionProbability:     0.9,
		MinimumAdmissionProbability: 0.1,
		MaxResources:                1,
		Priority: throttle.PriorityPolicy{
			RejectionScale: []float64{1, 0.5},
			Resolve: func(context.Context) throttle.Priority {
				policySet.priorityCalls.Add(1)

				return 1
			},
		},
	})
	if err != nil {
		return nil, err
	}
	policySet.throttler, err = throttle.New(adaptivePolicy)
	if err != nil {
		return nil, err
	}
	policySet.retry, err = retry.NewPolicy(retry.Config{
		Backoff: retry.Constant(0), MaxAttempts: 2,
		Clock: retry.SystemClock{}, Sleeper: retry.SystemSleeper{},
		Classifier: retry.RetryableClassifier(), UseResilienceBudget: true,
	})
	if err != nil {
		return nil, err
	}
	policySet.budget, err = resilience.NewBudget(resilience.BudgetConfig{
		MaxResources: 1, MaxAdditionalPerExecution: 1,
		MaxConcurrentAdditional: 1, MaxAdditionalPerWindow: 1,
		AdditionalWindow: time.Minute, PermitTTL: time.Minute,
		Clock: retry.SystemClock{},
	})
	if err != nil {
		return nil, err
	}
	policySet.component, err = serviceintegration.New(name, serviceintegration.Hooks{
		CloseAdmission: policySet.bulkhead.Close,
		Stop: func(ctx context.Context) error {
			return errors.Join(policySet.bulkhead.Drain(ctx), policySet.breaker.Shutdown(ctx))
		},
	})
	if err != nil {
		return nil, err
	}

	return policySet, nil
}

func (policies *rolePolicies) execute(ctx context.Context) error {
	metadata, err := resilience.NewMetadata("logical:"+policies.name, "role.execute", policies.name)
	if err != nil {
		return err
	}
	scope, budgetContext, err := policies.budget.Start(ctx, metadata)
	if err != nil {
		return err
	}
	defer func() { _ = scope.Close() }()

	outbound := func(ctx context.Context) (struct{}, error) {
		return breaker.Execute(ctx, policies.breaker, func(ctx context.Context) (struct{}, error) {
			return concurrencylimit.Execute(ctx, policies.limiter, func(context.Context) (struct{}, error) {
				return struct{}{}, nil
			})
		})
	}
	logical := func(ctx context.Context) (struct{}, error) {
		return retryValue(ctx, policies.retry, outbound)
	}
	operation := logical
	if policies.inbound {
		operation = func(ctx context.Context) (struct{}, error) {
			return throttle.Execute(ctx, policies.throttler, policies.name, func(ctx context.Context) (struct{}, error) {
				value, _, executeErr := bulkhead.Execute(ctx, policies.bulkhead, 1, logical)

				return value, executeErr
			})
		}
	}
	_, err = operation(budgetContext)
	if err != nil {
		return err
	}

	return nil
}

func retryValue(
	ctx context.Context,
	policy *retry.Policy,
	operation func(context.Context) (struct{}, error),
) (struct{}, error) {
	value, result, err := retry.Do(ctx, policy, operation)
	if err == nil && result.Attempts != 1 {
		return struct{}{}, fmt.Errorf("retry attempts = %d, want 1", result.Attempts)
	}

	return value, err
}
