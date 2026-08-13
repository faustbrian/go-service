package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/measure"
)

func TestAssessAppliesEveryProcessWorkloadAndDrainBudget(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	if result := assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive}); !result.Passed {
		t.Fatalf("passing assessment = %v", result.Failures)
	}

	tests := []struct {
		name     string
		change   func(*candidateResult)
		expected string
	}{
		{
			name: "Postal JSON-RPC throughput",
			change: func(result *candidateResult) {
				result.Summary.JSONRPC.RequestsPerSecond = 6_999
			},
			expected: "cohesive-service Postal JSON-RPC throughput",
		},
		{
			name: "Postal JSON-RPC p99",
			change: func(result *candidateResult) {
				result.Summary.JSONRPC.P99Microseconds = 19_501
			},
			expected: "cohesive-service Postal JSON-RPC p99",
		},
		{
			name: "Track ingestion",
			change: func(result *candidateResult) {
				result.Summary.TrackIngestion.RequestsPerSecond = 8_999
			},
			expected: "cohesive-service Track ingestion throughput",
		},
		{
			name: "Track ingestion p95",
			change: func(result *candidateResult) {
				result.Summary.TrackIngestion.P95Microseconds = 6_001
			},
			expected: "cohesive-service Track ingestion p95",
		},
		{
			name: "Track JSON-RPC",
			change: func(result *candidateResult) {
				result.Summary.TrackJSONRPC.P95Microseconds = 7_251
			},
			expected: "cohesive-service Track JSON-RPC p95",
		},
		{
			name: "Location lookup",
			change: func(result *candidateResult) {
				result.Summary.LocationLookup.P99Microseconds = 17_501
			},
			expected: "cohesive-service Location lookup p99",
		},
		{
			name: "probe",
			change: func(result *candidateResult) {
				result.Summary.Probe.P95Microseconds = 6_501
			},
			expected: "cohesive-service probe p95",
		},
		{
			name: "startup",
			change: func(result *candidateResult) {
				result.Summary.StartupP95Milliseconds = 200.001
			},
			expected: "cohesive-service startup p95",
		},
		{
			name: "relative idle RSS",
			change: func(result *candidateResult) {
				result.Summary.MaximumIdleRSSBytes = 11*1024*1024 + 1
			},
			expected: "cohesive relative idle RSS",
		},
		{
			name: "shutdown",
			change: func(result *candidateResult) {
				result.Summary.ShutdownP95Milliseconds = 30.001
			},
			expected: "cohesive-service shutdown p95",
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
			result := assess(referenceBudgetEnvironment(), []candidateResult{low, changed})
			if result.Passed || !slices.Contains(result.Failures, test.expected) {
				t.Fatalf("assessment = %#v, want %q", result, test.expected)
			}
		})
	}
}

func TestAssessAcceptsReviewedReferenceBudgets(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	for _, result := range []*candidateResult{&low, &cohesive} {
		for _, load := range []*measure.Load{
			&result.Summary.JSONRPC,
			&result.Summary.TrackJSONRPC,
		} {
			load.RequestsPerSecond = 7_000
			load.P95Microseconds = 7_250
			load.P99Microseconds = 19_500
		}
		for _, load := range []*measure.Load{
			&result.Summary.TrackIngestion,
			&result.Summary.LocationLookup,
		} {
			load.RequestsPerSecond = 9_000
			load.P95Microseconds = 6_000
			load.P99Microseconds = 17_500
		}
		result.Summary.Probe.P95Microseconds = 6_500
		result.Summary.StartupP95Milliseconds = 200
		result.Summary.ShutdownP95Milliseconds = 30
	}
	low.Summary.MaximumIdleRSSBytes = 10 * 1024 * 1024
	cohesive.Summary.MaximumIdleRSSBytes = 11 * 1024 * 1024

	if result := assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive}); !result.Passed {
		t.Fatalf("reviewed boundary assessment = %v", result.Failures)
	}
}

func TestAssessAppliesCohesiveBinaryOverheadBoundary(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	cohesive.BinaryBytes = low.BinaryBytes + 384*1024
	if result := assess(
		referenceBudgetEnvironment(),
		[]candidateResult{low, cohesive},
	); !result.Passed {
		t.Fatalf("exact cohesive binary boundary = %v", result.Failures)
	}

	cohesive.BinaryBytes++
	result := assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive})
	if result.Passed || !slices.Contains(result.Failures, "cohesive relative binary size") {
		t.Fatalf("over-limit cohesive binary assessment = %#v", result)
	}
}

func TestAssessAppliesReferenceBinarySizeBoundary(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	cohesive.BinaryBytes = 25 * 1024 * 1024 / 4
	low.BinaryBytes = cohesive.BinaryBytes - 384*1024
	if result := assess(
		referenceBudgetEnvironment(),
		[]candidateResult{low, cohesive},
	); !result.Passed {
		t.Fatalf("exact cohesive binary boundary = %v", result.Failures)
	}

	cohesive.BinaryBytes++
	result := assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive})
	if result.Passed || !slices.Contains(result.Failures, "cohesive-service binary size") {
		t.Fatalf("over-limit cohesive binary assessment = %#v", result)
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
			result := assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive})
			if !result.Passed {
				t.Fatalf("noisy relative assessment = %v", result.Failures)
			}

			cohesive = passingCandidate("cohesive-service")
			test.stable(&cohesive)
			result = assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive})
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

	result := assess(referenceBudgetEnvironment(), []candidateResult{low, cohesive})
	if result.Passed ||
		!slices.Contains(
			result.Failures,
			"cohesive relative startup sample evidence",
		) {
		t.Fatalf("assessment = %#v", result)
	}
}

func TestAssessAppliesOnlyPortableAndRelativeBudgetsOutsideReference(t *testing.T) {
	t.Parallel()

	low := passingCandidate("low-level-service")
	cohesive := passingCandidate("cohesive-service")
	for _, result := range []*candidateResult{&low, &cohesive} {
		result.BinaryBytes = 10 * 1024 * 1024
		result.Summary.StartupP95Milliseconds = 100
		result.Summary.MaximumIdleRSSBytes = 20 * 1024 * 1024
		result.Summary.ShutdownP95Milliseconds = 100
		result.Summary.Probe.P95Microseconds = 10_000
		for _, load := range []*measure.Load{
			&result.Summary.JSONRPC,
			&result.Summary.TrackIngestion,
			&result.Summary.TrackJSONRPC,
			&result.Summary.LocationLookup,
		} {
			load.RequestsPerSecond = 1
			load.P95Microseconds = 10_000
			load.P99Microseconds = 20_000
		}
	}

	result := assess(environment{
		OS:           "linux",
		Architecture: "arm64",
	}, []candidateResult{low, cohesive})
	if !result.Passed {
		t.Fatalf("Linux assessment = %v, want relative-only pass", result.Failures)
	}

	tests := []struct {
		name     string
		change   func(*candidateResult)
		expected string
	}{
		{
			name: "request success",
			change: func(result *candidateResult) {
				result.Summary.JSONRPC.SuccessRate = 0.99
			},
			expected: "cohesive-service Postal JSON-RPC success",
		},
		{
			name: "configured drain support",
			change: func(result *candidateResult) {
				result.Summary.ConfiguredDrainSupported = false
			},
			expected: "cohesive-service configured drain support",
		},
		{
			name: "configured drain deadline",
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
			result := assess(environment{
				OS:           "linux",
				Architecture: "arm64",
			}, []candidateResult{low, changed})
			if result.Passed || !slices.Contains(result.Failures, test.expected) {
				t.Fatalf("Linux assessment = %#v, want %q", result, test.expected)
			}
		})
	}
}

func referenceBudgetEnvironment() environment {
	return environment{
		OS:           "darwin",
		Architecture: "arm64",
		LogicalCPUs:  16,
		GoMaxProcs:   16,
		GoVersion:    "go1.26.6",
	}
}

func TestAppliesAbsoluteBudgetsOnlyInReferenceEnvironment(t *testing.T) {
	t.Parallel()

	reference := referenceBudgetEnvironment()
	tests := []struct {
		name        string
		environment environment
		want        bool
	}{
		{
			name:        "reference",
			environment: reference,
			want:        true,
		},
		{
			name: "different operating system",
			environment: environment{
				OS:           "linux",
				Architecture: reference.Architecture,
				LogicalCPUs:  reference.LogicalCPUs,
				GoVersion:    reference.GoVersion,
			},
		},
		{
			name: "different architecture",
			environment: environment{
				OS:           reference.OS,
				Architecture: "amd64",
				LogicalCPUs:  reference.LogicalCPUs,
				GoVersion:    reference.GoVersion,
			},
		},
		{
			name: "different logical CPU count",
			environment: environment{
				OS:           reference.OS,
				Architecture: reference.Architecture,
				LogicalCPUs:  8,
				GoVersion:    reference.GoVersion,
			},
		},
		{
			name: "different Go toolchain",
			environment: environment{
				OS:           reference.OS,
				Architecture: reference.Architecture,
				LogicalCPUs:  reference.LogicalCPUs,
				GoVersion:    "go1.27.0",
			},
		},
		{
			name: "throttled Go scheduler",
			environment: environment{
				OS:           reference.OS,
				Architecture: reference.Architecture,
				LogicalCPUs:  reference.LogicalCPUs,
				GoMaxProcs:   2,
				GoVersion:    reference.GoVersion,
			},
		},
		{
			name: "custom garbage collector",
			environment: environment{
				OS:           reference.OS,
				Architecture: reference.Architecture,
				LogicalCPUs:  reference.LogicalCPUs,
				GoMaxProcs:   reference.GoMaxProcs,
				GoVersion:    reference.GoVersion,
				GOGC:         "off",
			},
		},
		{
			name: "custom memory limit",
			environment: environment{
				OS:            reference.OS,
				Architecture:  reference.Architecture,
				LogicalCPUs:   reference.LogicalCPUs,
				GoMaxProcs:    reference.GoMaxProcs,
				GoVersion:     reference.GoVersion,
				GoMemoryLimit: "512MiB",
			},
		},
		{
			name: "custom runtime diagnostics",
			environment: environment{
				OS:           reference.OS,
				Architecture: reference.Architecture,
				LogicalCPUs:  reference.LogicalCPUs,
				GoMaxProcs:   reference.GoMaxProcs,
				GoVersion:    reference.GoVersion,
				GODEBUG:      "madvdontneed=0",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := appliesAbsoluteBudgets(test.environment); got != test.want {
				t.Fatalf("appliesAbsoluteBudgets() = %t, want %t", got, test.want)
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

func TestRestoreCheckpointReusesVerifiedSamplePrefix(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	rawDirectory := filepath.Join(directory, "raw")
	if err := os.Mkdir(rawDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	current, prepared := checkpointFixture(2)
	current.Environment.SourceRevision = "revalidated-revision"
	current.Environment.RevalidatedRevision = "revalidated-revision"
	checkpoint := current
	checkpoint.Environment.SourceRevision = "original-revision"
	checkpoint.Environment.RevalidatedRevision = "original-revision"
	checkpoint.Environment.ExecutionStarted = "original-start"
	checkpoint.Results = append([]candidateResult(nil), current.Results...)
	checkpoint.Results[0].Samples = []measure.Sample{
		checkpointSample(t, rawDirectory, "lowlevel-disabled-01"),
	}
	checkpoint.Results[0].Summary = measure.Summary{MaximumIdleRSSBytes: 1}
	checkpoint.Budgets = budgetResult{Passed: true}
	if err := writeReport(directory, checkpoint); err != nil {
		t.Fatal(err)
	}

	restored, err := restoreCheckpoint(directory, current, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Environment.ExecutionStarted != "original-start" ||
		restored.Environment.SourceRevision != "original-revision" ||
		restored.Environment.RevalidatedRevision != "revalidated-revision" ||
		len(restored.Results[0].Samples) != 1 ||
		restored.Results[0].Summary.MaximumIdleRSSBytes != 42 ||
		restored.Budgets.Passed {
		t.Fatalf("restored checkpoint = %#v", restored)
	}
}

func TestRestoreCheckpointRejectsUnverifiableState(t *testing.T) {
	t.Parallel()

	t.Run("absent", func(t *testing.T) {
		current, prepared := checkpointFixture(1)
		restored, err := restoreCheckpoint(t.TempDir(), current, prepared)
		if err != nil || !reflect.DeepEqual(restored.Results, current.Results) {
			t.Fatalf("absent checkpoint = %#v, %v", restored, err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Mkdir(filepath.Join(directory, "report.json"), 0o750); err != nil {
			t.Fatal(err)
		}
		current, prepared := checkpointFixture(1)
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("unreadable checkpoint was accepted")
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "report.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		current, prepared := checkpointFixture(1)
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("invalid checkpoint was accepted")
		}
	})
	t.Run("different inputs", func(t *testing.T) {
		directory := t.TempDir()
		current, prepared := checkpointFixture(1)
		checkpoint := current
		checkpoint.Config.Requests++
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("input-mismatched checkpoint was accepted")
		}
	})
	t.Run("different runtime environment", func(t *testing.T) {
		directory := t.TempDir()
		current, prepared := checkpointFixture(1)
		checkpoint := current
		checkpoint.Environment.GoMaxProcs--
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("runtime-environment-mismatched checkpoint was accepted")
		}
	})
	for _, test := range []struct {
		name   string
		remove func(*environment)
	}{
		{
			name: "missing execution revision",
			remove: func(value *environment) {
				value.SourceRevision = ""
			},
		},
		{
			name: "missing revalidation revision",
			remove: func(value *environment) {
				value.RevalidatedRevision = ""
			},
		},
		{
			name: "missing execution start",
			remove: func(value *environment) {
				value.ExecutionStarted = ""
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			current, prepared := checkpointFixture(1)
			checkpoint := current
			test.remove(&checkpoint.Environment)
			if err := writeReport(directory, checkpoint); err != nil {
				t.Fatal(err)
			}
			if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
				t.Fatal("checkpoint with incomplete provenance was accepted")
			}
		})
	}
	t.Run("different binary", func(t *testing.T) {
		directory := t.TempDir()
		current, prepared := checkpointFixture(1)
		checkpoint := current
		checkpoint.Results = append([]candidateResult(nil), current.Results...)
		checkpoint.Results[0].BinarySHA256 = "different"
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("binary-mismatched checkpoint was accepted")
		}
	})
	t.Run("different result count", func(t *testing.T) {
		directory := t.TempDir()
		current, prepared := checkpointFixture(1)
		checkpoint := current
		checkpoint.Results = nil
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("result-count-mismatched checkpoint was accepted")
		}
	})
	t.Run("missing raw artifact", func(t *testing.T) {
		directory := t.TempDir()
		current, prepared := checkpointFixture(1)
		checkpoint := current
		checkpoint.Results = append([]candidateResult(nil), current.Results...)
		checkpoint.Results[0].Samples = []measure.Sample{{
			JSONRPCRaw: filepath.Join(directory, "raw", "lowlevel-disabled-01-postal-json-rpc.json") + "#sha256=missing",
		}}
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("checkpoint with missing raw artifact was accepted")
		}
	})
	t.Run("raw digest mismatch", func(t *testing.T) {
		directory := t.TempDir()
		rawDirectory := filepath.Join(directory, "raw")
		if err := os.Mkdir(rawDirectory, 0o750); err != nil {
			t.Fatal(err)
		}
		current, prepared := checkpointFixture(1)
		checkpoint := current
		checkpoint.Results = append([]candidateResult(nil), current.Results...)
		checkpoint.Results[0].Samples = []measure.Sample{
			checkpointSample(t, rawDirectory, "lowlevel-disabled-01"),
		}
		checkpoint.Results[0].Samples[0].JSONRPCRaw += "changed"
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("checkpoint with raw digest mismatch was accepted")
		}
	})
	t.Run("non-prefix samples", func(t *testing.T) {
		directory := t.TempDir()
		rawDirectory := filepath.Join(directory, "raw")
		if err := os.Mkdir(rawDirectory, 0o750); err != nil {
			t.Fatal(err)
		}
		current, prepared := checkpointFixture(1)
		cohesive := current.Results[0]
		cohesive.Candidate = "cohesive-service"
		cohesive.BinarySHA256 = "cohesive-digest"
		current.Results = append(current.Results, cohesive)
		prepared["disabled"] = append(prepared["disabled"], preparedCandidate{
			item:  candidate{name: "cohesive-service", command: "cohesive"},
			state: "disabled", binary: "cohesive", resultIndex: 1,
		})
		checkpoint := current
		checkpoint.Results = append([]candidateResult(nil), current.Results...)
		checkpoint.Results[1].Samples = []measure.Sample{
			checkpointSample(t, rawDirectory, "cohesive-disabled-01"),
		}
		if err := writeReport(directory, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := restoreCheckpoint(directory, current, prepared); err == nil {
			t.Fatal("non-prefix checkpoint was accepted")
		}
	})
}

func checkpointFixture(samples int) (report, map[string][]preparedCandidate) {
	current := report{
		Schema: "service-platform-process-benchmark/v3",
		Environment: environment{
			OS: "darwin", Architecture: "arm64", LogicalCPUs: 16,
			GoMaxProcs: 16, GoVersion: "go1.26.6", OHAVersion: "oha 1.15.0",
			Kernel: "kernel", SourceRevision: "revision", RevalidatedRevision: "revision",
			GateInputDigest: "gate-input", ExecutionStarted: "new-start",
		},
		Config: configuration{
			Samples: samples, Requests: 100, ProbeRequests: 10, Concurrency: 4,
			Candidates: []string{"low-level-service"}, States: []string{"disabled"},
		},
		Results: []candidateResult{{
			Candidate: "low-level-service", State: "disabled",
			BinaryBytes: 10, BinarySHA256: "lowlevel-digest",
		}},
	}
	prepared := map[string][]preparedCandidate{
		"disabled": {{
			item:  candidate{name: "low-level-service", command: "lowlevel"},
			state: "disabled", binary: "lowlevel", resultIndex: 0,
		}},
	}

	return current, prepared
}

func checkpointSample(t *testing.T, directory string, prefix string) measure.Sample {
	t.Helper()

	reference := func(suffix string) string {
		path, digest, err := writeRaw(directory, prefix+suffix, []byte(suffix))
		if err != nil {
			t.Fatal(err)
		}

		return path + "#sha256=" + digest
	}

	return measure.Sample{
		IdleRSSBytes:      42,
		JSONRPCRaw:        reference("-postal-json-rpc.json"),
		TrackIngestionRaw: reference("-track-ingestion.json"),
		TrackJSONRPCRaw:   reference("-track-json-rpc.json"),
		LocationLookupRaw: reference("-location-lookup.json"),
		ProbeRaw:          reference("-probe.json"),
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
