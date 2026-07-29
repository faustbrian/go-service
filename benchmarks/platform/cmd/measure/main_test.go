package main

import (
	"slices"
	"testing"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/measure"
)

func TestAssessAppliesEveryFrozenWorkloadAndDrainBudget(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	if result := assess([]candidateResult{low, cohesive}); !result.Passed {
		t.Fatalf("passing assessment = %v", result.Failures)
	}

	tests := []struct {
		name     string
		change   func(*candidateResult)
		expected string
	}{
		{
			name: "Track ingestion",
			change: func(result *candidateResult) {
				result.Summary.TrackIngestion.RequestsPerSecond = 84_999
			},
			expected: "cohesive-service Track ingestion throughput",
		},
		{
			name: "Track JSON-RPC",
			change: func(result *candidateResult) {
				result.Summary.TrackJSONRPC.P95Microseconds = 501
			},
			expected: "cohesive-service Track JSON-RPC p95",
		},
		{
			name: "Location lookup",
			change: func(result *candidateResult) {
				result.Summary.LocationLookup.P99Microseconds = 801
			},
			expected: "cohesive-service Location lookup p99",
		},
		{
			name: "configured drain",
			change: func(result *candidateResult) {
				result.Summary.ConfiguredDrainP95Milliseconds = 1000
			},
			expected: "cohesive-service configured drain p95",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cohesive
			test.change(&changed)
			result := assess([]candidateResult{low, changed})
			if result.Passed || !slices.Contains(result.Failures, test.expected) {
				t.Fatalf("assessment = %#v, want %q", result, test.expected)
			}
		})
	}
}

func TestMeasurementOrderAlternatesCandidateDirectionBySample(t *testing.T) {
	t.Parallel()

	got := measurementOrder(3, 3)
	want := []measurementStep{
		{CandidateIndex: 0, SampleIndex: 0},
		{CandidateIndex: 1, SampleIndex: 0},
		{CandidateIndex: 2, SampleIndex: 0},
		{CandidateIndex: 2, SampleIndex: 1},
		{CandidateIndex: 1, SampleIndex: 1},
		{CandidateIndex: 0, SampleIndex: 1},
		{CandidateIndex: 0, SampleIndex: 2},
		{CandidateIndex: 1, SampleIndex: 2},
		{CandidateIndex: 2, SampleIndex: 2},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("measurementOrder() = %#v, want %#v", got, want)
	}
}

func passingCandidate(name string) candidateResult {
	jsonRPC := measure.Load{
		SuccessRate:       1,
		RequestsPerSecond: 90_000,
		P50Microseconds:   100,
		P95Microseconds:   200,
		P99Microseconds:   300,
	}
	httpLoad := measure.Load{
		SuccessRate:       1,
		RequestsPerSecond: 90_000,
		P50Microseconds:   100,
		P95Microseconds:   200,
		P99Microseconds:   300,
	}

	return candidateResult{
		Candidate:   name,
		State:       "disabled",
		BinaryBytes: 5 * 1024 * 1024,
		Summary: measure.Summary{
			StartupP95Milliseconds:         10,
			MaximumIdleRSSBytes:            10 * 1024 * 1024,
			JSONRPC:                        jsonRPC,
			TrackIngestion:                 httpLoad,
			TrackJSONRPC:                   jsonRPC,
			LocationLookup:                 httpLoad,
			Probe:                          measure.Load{SuccessRate: 1, P95Microseconds: 100},
			ShutdownP95Milliseconds:        5,
			ConfiguredDrainP95Milliseconds: 20,
			ConfiguredDrainSupported:       true,
		},
	}
}
