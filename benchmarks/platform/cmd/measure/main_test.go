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

func TestAssessRequiresSignificantRelativeRegressions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		noisy   func(*candidateResult)
		stable  func(*candidateResult)
		failure string
	}{
		{
			name: "startup",
			noisy: func(result *candidateResult) {
				result.Summary.StartupP95Milliseconds = 11
				setSampleValues(result, func(sample *measure.Sample, index int) {
					sample.StartupMilliseconds = []float64{11, 11, 11, 9, 9}[index]
				})
			},
			stable: func(result *candidateResult) {
				result.Summary.StartupP95Milliseconds = 11
				setSampleValues(result, func(sample *measure.Sample, _ int) {
					sample.StartupMilliseconds = 11
				})
			},
			failure: "cohesive relative startup",
		},
		{
			name: "shutdown",
			noisy: func(result *candidateResult) {
				result.Summary.ShutdownP95Milliseconds = 5.5
				setSampleValues(result, func(sample *measure.Sample, index int) {
					sample.ShutdownMilliseconds = []float64{5.5, 5.5, 5.5, 4.5, 4.5}[index]
				})
			},
			stable: func(result *candidateResult) {
				result.Summary.ShutdownP95Milliseconds = 5.5
				setSampleValues(result, func(sample *measure.Sample, _ int) {
					sample.ShutdownMilliseconds = 5.5
				})
			},
			failure: "cohesive relative shutdown",
		},
		{
			name: "request latency",
			noisy: func(result *candidateResult) {
				result.Summary.JSONRPC.P50Microseconds = 110
				setSampleValues(result, func(sample *measure.Sample, index int) {
					sample.JSONRPC.P50Microseconds = []float64{110, 110, 110, 90, 90}[index]
				})
			},
			stable: func(result *candidateResult) {
				result.Summary.JSONRPC.P50Microseconds = 110
				setSampleValues(result, func(sample *measure.Sample, _ int) {
					sample.JSONRPC.P50Microseconds = 110
				})
			},
			failure: "cohesive relative Postal JSON-RPC p50",
		},
		{
			name: "request throughput",
			noisy: func(result *candidateResult) {
				result.Summary.JSONRPC.RequestsPerSecond = 80_000
				setSampleValues(result, func(sample *measure.Sample, index int) {
					sample.JSONRPC.RequestsPerSecond = []float64{
						80_000, 80_000, 80_000, 100_000, 100_000,
					}[index]
				})
			},
			stable: func(result *candidateResult) {
				result.Summary.JSONRPC.RequestsPerSecond = 80_000
				setSampleValues(result, func(sample *measure.Sample, _ int) {
					sample.JSONRPC.RequestsPerSecond = 80_000
				})
			},
			failure: "cohesive relative Postal JSON-RPC throughput",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			low := passingCandidate("low-level-service")
			cohesive := passingCandidate("cohesive-service")
			test.noisy(&cohesive)
			result := assess([]candidateResult{low, cohesive})
			if !result.Passed {
				t.Fatalf("noisy relative assessment = %v", result.Failures)
			}

			cohesive = passingCandidate("cohesive-service")
			test.stable(&cohesive)
			result = assess([]candidateResult{low, cohesive})
			if result.Passed || !slices.Contains(result.Failures, test.failure) {
				t.Fatalf("consistent relative assessment = %#v", result)
			}
		})
	}
}

func TestAssessRejectsMissingRelativeSampleEvidence(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	cohesive.Samples = nil

	result := assess([]candidateResult{low, cohesive})
	if result.Passed ||
		!slices.Contains(
			result.Failures,
			"cohesive relative startup sample evidence",
		) {
		t.Fatalf("assessment = %#v", result)
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

func setSampleValues(
	result *candidateResult,
	set func(*measure.Sample, int),
) {
	for index := range result.Samples {
		set(&result.Samples[index], index)
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

	result := candidateResult{
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
	sample := measure.Sample{
		StartupMilliseconds:         10,
		IdleRSSBytes:                10 * 1024 * 1024,
		JSONRPC:                     jsonRPC,
		TrackIngestion:              httpLoad,
		TrackJSONRPC:                jsonRPC,
		LocationLookup:              httpLoad,
		Probe:                       measure.Load{SuccessRate: 1, P95Microseconds: 100},
		ShutdownMilliseconds:        5,
		ConfiguredDrainMilliseconds: 20,
		ConfiguredDrainSupported:    true,
	}
	result.Samples = []measure.Sample{sample, sample, sample, sample, sample}

	return result
}
