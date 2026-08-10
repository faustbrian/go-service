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
		name         string
		demand       int
		wantReplicas int
		wantAttempts int
	}{
		{name: "single cold replica", demand: 4, wantReplicas: 1, wantAttempts: 2},
		{name: "mixed rollout", demand: 8, wantReplicas: 2, wantAttempts: 2},
		{name: "scale to maximum", demand: 16, wantReplicas: 4, wantAttempts: 4},
		{name: "sustained demand after warmup", demand: 32, wantReplicas: 4, wantAttempts: 0},
	}

	var totalDemand int
	var totalAttempts int
	var lastRoundAttempts int
	for _, round := range rounds {
		t.Run(round.name, func(t *testing.T) {
			desired := fleetReplicasForDemand(round.demand)
			added := fleet.scaleTo(t, desired)
			if len(fleet.replicas) != round.wantReplicas {
				t.Fatalf("replicas = %d, want %d", len(fleet.replicas), round.wantReplicas)
			}
			for _, replica := range added {
				if snapshots := replica.throttler.Snapshots(); len(snapshots) != 0 {
					t.Fatalf("cold replica %d inherited policy state: %+v", replica.id, snapshots)
				}
			}

			summary := fleet.runOutageRound(t, round.demand)
			if summary.backendAttempts != round.wantAttempts {
				t.Fatalf("backend attempts = %d, want %d", summary.backendAttempts, round.wantAttempts)
			}
			if summary.backendAttempts > len(added)*fleetAttemptsPerColdPod {
				t.Fatalf(
					"round amplification = %d, cold-replica bound = %d",
					summary.backendAttempts,
					len(added)*fleetAttemptsPerColdPod,
				)
			}
			totalDemand += round.demand
			totalAttempts += summary.backendAttempts
			lastRoundAttempts = summary.backendAttempts
		})
	}

	if revisions := fleet.policyRevisions(t); fmt.Sprint(revisions) != "[policy-v1 policy-v2 policy-v2 policy-v2]" {
		t.Fatalf("policy revisions = %v", revisions)
	}
	if totalAttempts != fleetMaximumReplicas*fleetAttemptsPerColdPod {
		t.Fatalf(
			"fleet attempts = %d, reviewed cold-start bound = %d",
			totalAttempts,
			fleetMaximumReplicas*fleetAttemptsPerColdPod,
		)
	}
	if totalAttempts >= totalDemand {
		t.Fatalf("backend attempts = %d, offered demand = %d", totalAttempts, totalDemand)
	}

	unsafeWorkEstimate := fleetReplicasForWork(lastRoundAttempts)
	if unsafeWorkEstimate != 1 || fleetReplicasForDemand(rounds[len(rounds)-1].demand) != fleetMaximumReplicas {
		t.Fatalf(
			"sustained-outage estimates = work:%d demand:%d",
			unsafeWorkEstimate,
			fleetReplicasForDemand(rounds[len(rounds)-1].demand),
		)
	}

	removed := fleet.scaleTo(t, 1)
	if len(removed) != fleetMaximumReplicas-1 || len(fleet.replicas) != 1 {
		t.Fatalf("scale-down removed %d replicas, remaining %d", len(removed), len(fleet.replicas))
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

func fleetReplicasForDemand(demand int) int {
	return min(fleetMaximumReplicas, max(1, (demand+fleetDemandPerReplica-1)/fleetDemandPerReplica))
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
