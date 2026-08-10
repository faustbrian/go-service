package adoption_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	throttle "github.com/faustbrian/golib/pkg/adaptive-throttle"
	"github.com/faustbrian/golib/pkg/resilience"
	"github.com/faustbrian/golib/pkg/retry"
)

const (
	fleetResource           = "inventory-backend"
	fleetMaximumReplicas    = 4
	fleetDemandPerReplica   = 4
	fleetAdditionalPerCall  = 1
	fleetAttemptsPerColdPod = 1 + fleetAdditionalPerCall
)

var errFleetBackendUnavailable = errors.New("fleet backend unavailable")

func TestResilienceFleetBoundsOutageAmplificationDuringScalingAndRollout(t *testing.T) {
	t.Parallel()

	revisions := []fleetRevision{
		{name: "policy-v1", maximumAttempts: 4},
		{name: "policy-v2", maximumAttempts: 7},
	}
	fleet := newResilienceFleet(t, revisions)
	rounds := []struct {
		name             string
		wantReplicas     int
		wantAttempts     int
		wantAdmitted     int
		wantRejected     int
		wantNextReplicas int
	}{
		{name: "single cold replica", wantReplicas: 1, wantAttempts: 2, wantAdmitted: 1, wantRejected: 7, wantNextReplicas: 3},
		{name: "feedback scale out and mixed rollout", wantReplicas: 3, wantAttempts: 4, wantAdmitted: 2, wantRejected: 6, wantNextReplicas: 3},
		{name: "warm fleet feedback scale down", wantReplicas: 3, wantAttempts: 0, wantAdmitted: 0, wantRejected: 8, wantNextReplicas: 2},
		{name: "bounded feedback convergence", wantReplicas: 2, wantAttempts: 0, wantAdmitted: 0, wantRejected: 8, wantNextReplicas: 2},
	}

	const offeredDemand = 8
	desiredReplicas := 1
	var totalAttempts int
	peakReplicas := 0
	for _, round := range rounds {
		t.Run(round.name, func(t *testing.T) {
			previousReplicas := len(fleet.replicas)
			changed := fleet.scaleTo(t, desiredReplicas)
			var added []*fleetReplica
			if len(fleet.replicas) > previousReplicas {
				added = changed
			}
			if len(fleet.replicas) != round.wantReplicas {
				t.Fatalf("replicas = %d, want %d", len(fleet.replicas), round.wantReplicas)
			}
			peakReplicas = max(peakReplicas, len(fleet.replicas))
			for _, replica := range added {
				if snapshots := replica.throttler.Snapshots(); len(snapshots) != 0 {
					t.Fatalf("cold replica %d inherited policy state: %+v", replica.id, snapshots)
				}
			}

			summary := fleet.runOutageRound(t, offeredDemand)
			if summary.backendAttempts != round.wantAttempts {
				t.Fatalf("backend attempts = %d, want %d", summary.backendAttempts, round.wantAttempts)
			}
			if summary.admitted != round.wantAdmitted || summary.rejected != round.wantRejected {
				t.Fatalf(
					"policy outcomes = admitted:%d rejected:%d, want admitted:%d rejected:%d",
					summary.admitted,
					summary.rejected,
					round.wantAdmitted,
					round.wantRejected,
				)
			}
			if summary.backendAttempts > len(added)*fleetAttemptsPerColdPod {
				t.Fatalf(
					"round amplification = %d, cold-replica bound = %d",
					summary.backendAttempts,
					len(added)*fleetAttemptsPerColdPod,
				)
			}
			totalAttempts += summary.backendAttempts
			desiredReplicas = fleetReplicasForWork(summary.hpaWork())
			if desiredReplicas != round.wantNextReplicas {
				t.Fatalf("next HPA decision = %d, want %d", desiredReplicas, round.wantNextReplicas)
			}
		})
	}

	if revisions := fleet.policyRevisions(t); fmt.Sprint(revisions) != "[policy-v1 policy-v2]" {
		t.Fatalf("policy revisions = %v", revisions)
	}
	reviewedMaximumAttempts := fleetMaximumReplicas * fleetAttemptsPerColdPod
	if totalAttempts != peakReplicas*fleetAttemptsPerColdPod || totalAttempts > reviewedMaximumAttempts {
		t.Fatalf(
			"fleet attempts = %d, cold-replica bound = %d, configured-maximum bound = %d",
			totalAttempts,
			peakReplicas*fleetAttemptsPerColdPod,
			reviewedMaximumAttempts,
		)
	}
	totalDemand := len(rounds) * offeredDemand
	if totalAttempts >= totalDemand || peakReplicas >= fleetMaximumReplicas {
		t.Fatalf(
			"bounded feedback = attempts:%d demand:%d peak-replicas:%d maximum:%d",
			totalAttempts,
			totalDemand,
			peakReplicas,
			fleetMaximumReplicas,
		)
	}
	if desiredReplicas != len(fleet.replicas) {
		t.Fatalf("feedback did not converge: replicas = %d, next decision = %d", len(fleet.replicas), desiredReplicas)
	}
}

type fleetRevision struct {
	name            string
	maximumAttempts uint
}

type resilienceFleet struct {
	revisions []fleetRevision
	replicas  []*fleetReplica
	clock     *fleetClock
	nextID    int
	nextCall  int
}

type fleetReplica struct {
	id        int
	revision  fleetRevision
	throttler *throttle.Throttler
	retry     *retry.Policy
	budget    *resilience.Budget
}

type fleetRoundSummary struct {
	backendAttempts int
	admitted        int
	rejected        int
}

func (summary fleetRoundSummary) hpaWork() int {
	return summary.backendAttempts + summary.rejected
}

func newResilienceFleet(t *testing.T, revisions []fleetRevision) *resilienceFleet {
	t.Helper()

	return &resilienceFleet{
		revisions: revisions,
		clock:     &fleetClock{now: time.Unix(1_800_000_000, 0)},
	}
}

func (fleet *resilienceFleet) scaleTo(t *testing.T, desired int) []*fleetReplica {
	t.Helper()

	if desired < len(fleet.replicas) {
		removed := append([]*fleetReplica(nil), fleet.replicas[desired:]...)
		fleet.replicas = fleet.replicas[:desired]

		return removed
	}
	added := make([]*fleetReplica, 0, desired-len(fleet.replicas))
	for len(fleet.replicas) < desired {
		revision := fleet.revisions[min(fleet.nextID, len(fleet.revisions)-1)]
		replica := newFleetReplica(t, fleet.nextID, revision, fleet.clock)
		fleet.nextID++
		fleet.replicas = append(fleet.replicas, replica)
		added = append(added, replica)
	}

	return added
}

func newFleetReplica(t *testing.T, id int, revision fleetRevision, clock *fleetClock) *fleetReplica {
	t.Helper()

	policy, err := throttle.NewPolicy(throttle.PolicyConfig{
		Revision:                    revision.name,
		MinimumSamples:              1,
		Algorithm:                   throttle.GoogleSRE{AcceptMultiplier: 1},
		MaxRejectionProbability:     0.9,
		MinimumAdmissionProbability: 0.1,
		MaxResources:                1,
		Clock:                       clock,
		Random:                      fleetRejectingRandom{},
	})
	if err != nil {
		t.Fatalf("throttle.NewPolicy() error = %v", err)
	}
	throttler, err := throttle.New(policy)
	if err != nil {
		t.Fatalf("throttle.New() error = %v", err)
	}
	retryPolicy, err := retry.NewPolicy(retry.Config{
		Backoff:             retry.Constant(0),
		MaxAttempts:         revision.maximumAttempts,
		Clock:               clock,
		Sleeper:             fleetSleeper{},
		Classifier:          retry.RetryableClassifier(),
		UseResilienceBudget: true,
	})
	if err != nil {
		t.Fatalf("retry.NewPolicy() error = %v", err)
	}
	budget, err := resilience.NewBudget(resilience.BudgetConfig{
		MaxResources:              1,
		MaxAdditionalPerExecution: fleetAdditionalPerCall,
		MaxConcurrentAdditional:   fleetAdditionalPerCall,
		MaxAdditionalPerWindow:    fleetAdditionalPerCall,
		AdditionalWindow:          time.Hour,
		PermitTTL:                 time.Hour,
		Clock:                     clock,
	})
	if err != nil {
		t.Fatalf("resilience.NewBudget() error = %v", err)
	}

	return &fleetReplica{id: id, revision: revision, throttler: throttler, retry: retryPolicy, budget: budget}
}

func (fleet *resilienceFleet) runOutageRound(t *testing.T, demand int) fleetRoundSummary {
	t.Helper()

	summary := fleetRoundSummary{}
	for request := range demand {
		replica := fleet.replicas[request%len(fleet.replicas)]
		attempts, rejected := fleet.executeOutage(t, replica)
		summary.backendAttempts += attempts
		if rejected {
			summary.rejected++
		} else {
			summary.admitted++
		}
	}
	if summary.admitted+summary.rejected != demand {
		t.Fatalf("accounted demand = %d, want %d", summary.admitted+summary.rejected, demand)
	}

	return summary
}

func (fleet *resilienceFleet) executeOutage(t *testing.T, replica *fleetReplica) (int, bool) {
	t.Helper()

	permit, err := replica.throttler.TryAcquire(context.Background(), fleetResource)
	if errors.Is(err, throttle.ErrRejected) {
		return 0, true
	}
	if err != nil {
		t.Fatalf("replica %d TryAcquire() error = %v", replica.id, err)
	}

	fleet.nextCall++
	metadata, err := resilience.NewMetadata(
		fmt.Sprintf("fleet-call-%d", fleet.nextCall),
		"fleet.outbound",
		fleetResource,
	)
	if err != nil {
		t.Fatalf("resilience.NewMetadata() error = %v", err)
	}
	scope, budgetContext, err := replica.budget.Start(context.Background(), metadata)
	if err != nil {
		t.Fatalf("replica %d budget.Start() error = %v", replica.id, err)
	}
	attempts := 0
	_, result, executeErr := retry.Do(budgetContext, replica.retry, func(context.Context) (struct{}, error) {
		attempts++

		return struct{}{}, retry.Retryable(errFleetBackendUnavailable)
	})
	if result.Attempts != uint(fleetAttemptsPerColdPod) || result.Reason != retry.ReasonWorkBudget {
		t.Fatalf("replica %d retry result = %+v", replica.id, result)
	}
	if !errors.Is(executeErr, resilience.ErrBudgetRejected) {
		t.Fatalf("replica %d retry error = %v, want budget rejection", replica.id, executeErr)
	}
	if snapshot := scope.Snapshot(); snapshot.AdditionalAdmitted != fleetAdditionalPerCall || snapshot.AdditionalActive != 0 {
		t.Fatalf("replica %d budget snapshot = %+v", replica.id, snapshot)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("replica %d scope.Close() error = %v", replica.id, err)
	}
	if err := permit.Record(throttle.Classification{
		Outcome: throttle.DownstreamOverload,
		Reason:  throttle.ReasonExplicitOverload,
	}); err != nil {
		t.Fatalf("replica %d permit.Record() error = %v", replica.id, err)
	}

	return attempts, false
}

func (fleet *resilienceFleet) policyRevisions(t *testing.T) []string {
	t.Helper()

	revisions := make([]string, 0, len(fleet.replicas))
	for _, replica := range fleet.replicas {
		snapshot, ok := replica.throttler.Snapshot(fleetResource)
		if !ok {
			t.Fatalf("replica %d has no policy snapshot", replica.id)
		}
		revisions = append(revisions, snapshot.Revision)
	}

	return revisions
}

func fleetReplicasForWork(attempts int) int {
	return min(fleetMaximumReplicas, max(1, (attempts+fleetDemandPerReplica-1)/fleetDemandPerReplica))
}

type fleetRejectingRandom struct{}

func (fleetRejectingRandom) Float64() float64 { return 0 }

type fleetClock struct {
	now time.Time
}

func (clock *fleetClock) Now() time.Time { return clock.now }

type fleetSleeper struct{}

func (fleetSleeper) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }
