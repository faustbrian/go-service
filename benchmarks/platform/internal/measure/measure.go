// Package measure parses and summarizes raw process benchmark observations.
package measure

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
)

// Load is one parsed oha request workload.
type Load struct {
	// SuccessRate is the fraction of requests with the expected response.
	SuccessRate float64 `json:"success_rate"`
	// RequestsPerSecond is the observed request throughput.
	RequestsPerSecond float64 `json:"requests_per_second"`
	// P50Microseconds is the median request latency.
	P50Microseconds float64 `json:"p50_microseconds"`
	// P95Microseconds is the 95th-percentile request latency.
	P95Microseconds float64 `json:"p95_microseconds"`
	// P99Microseconds is the 99th-percentile request latency.
	P99Microseconds float64 `json:"p99_microseconds"`
}

// Cancellation records the independent server-side native-context observation,
// not graceful drain. Unobserved contexts retain their bounded wait duration
// and have no cancellation latency; zero is not a supported latency result.
type Cancellation struct {
	// Mode is the client trigger: explicit disconnect or client deadline.
	Mode string `json:"mode"`
	// Observed reports native request-context cancellation, not timer completion.
	Observed bool `json:"observed"`
	// LatencyMilliseconds is trigger-to-native-observation time; unsupported results have none.
	LatencyMilliseconds float64 `json:"latency_milliseconds"`
	// WaitMilliseconds is handler-start-to-observation or bounded cleanup time.
	WaitMilliseconds float64 `json:"wait_milliseconds"`
}

// CancellationSummary retains worst observed wait and latency across samples.
// Observed is true only when every independently started sample observed the
// native context cancellation.
type CancellationSummary struct {
	// Mode is the client trigger: explicit disconnect or client deadline.
	Mode string `json:"mode"`
	// Observed reports native request-context cancellation, not timer completion.
	Observed bool `json:"observed"`
	// LatencyP95Milliseconds is the sampled p95 only when all samples observe cancellation.
	LatencyP95Milliseconds float64 `json:"latency_p95_milliseconds"`
	// WaitP95Milliseconds is the p95 wait including unsupported native-context waits.
	WaitP95Milliseconds float64 `json:"wait_p95_milliseconds"`
}

// SaturationStep is one fixed, bounded concurrency point for Track ingestion.
// These closed-loop observations do not claim open-loop maximum capacity.
type SaturationStep struct {
	// Concurrency is the predetermined number of closed-loop client connections.
	Concurrency int `json:"concurrency"`
	// Load contains the matched Track ingestion result at this concurrency.
	Load Load `json:"load"`
}

// Observations contains the additional process metrics retained as a raw
// artifact. Checkpoint restoration compares it with the report, preventing
// changed metrics from being accepted merely because load files still match.
type Observations struct {
	// PeakRSSBytes is a sampled under-load maximum, not an OS high-water mark.
	PeakRSSBytes int64 `json:"peak_rss_bytes"`
	// RSSReadings counts successful resident-memory samples during load.
	RSSReadings int `json:"rss_readings"`
	// Cancellation retains independent native-context observations for both triggers.
	Cancellation []Cancellation `json:"cancellation"`
	// Saturation retains the full bounded closed-loop concurrency sweep.
	Saturation []SaturationStep `json:"saturation"`
}

// Sample is one independently started process observation.
type Sample struct {
	// Observations adds sampled peak memory, saturation and cancellation evidence.
	Observations
	// ObservationsRaw identifies the checksummed additional process observations.
	ObservationsRaw string `json:"observations_raw"`
	// SaturationRaw retains one checksummed raw oha artifact per sweep point.
	SaturationRaw []string `json:"saturation_raw"`
	// StartupMilliseconds is process start to successful startup probe.
	StartupMilliseconds float64 `json:"startup_milliseconds"`
	// IdleRSSBytes is the resident set size after startup.
	IdleRSSBytes int64 `json:"idle_rss_bytes"`
	// JSONRPC records the Postal JSON-RPC workload.
	JSONRPC Load `json:"json_rpc"`
	// TrackIngestion records the Track ingestion workload.
	TrackIngestion Load `json:"track_ingestion"`
	// TrackJSONRPC records the Track JSON-RPC fan-out workload.
	TrackJSONRPC Load `json:"track_json_rpc"`
	// LocationLookup records the Location projection workload.
	LocationLookup Load `json:"location_lookup"`
	// Probe records the readiness workload.
	Probe Load `json:"probe"`
	// ShutdownMilliseconds is signal delivery to process exit without work.
	ShutdownMilliseconds float64 `json:"shutdown_milliseconds"`
	// ConfiguredDrainMilliseconds is signal delivery to joined in-flight work.
	ConfiguredDrainMilliseconds float64 `json:"configured_drain_milliseconds"`
	// ConfiguredDrainSupported reports support for the required drain contract.
	ConfiguredDrainSupported bool `json:"configured_drain_supported"`
	// JSONRPCRaw identifies the retained Postal JSON-RPC load artifact.
	JSONRPCRaw string `json:"json_rpc_raw"`
	// TrackIngestionRaw identifies the retained Track ingestion load artifact.
	TrackIngestionRaw string `json:"track_ingestion_raw"`
	// TrackJSONRPCRaw identifies the retained Track JSON-RPC load artifact.
	TrackJSONRPCRaw string `json:"track_json_rpc_raw"`
	// LocationLookupRaw identifies the retained Location load artifact.
	LocationLookupRaw string `json:"location_lookup_raw"`
	// ProbeRaw identifies the retained readiness load artifact.
	ProbeRaw string `json:"probe_raw"`
}

// Summary applies the frozen repeated-sample decision rules.
type Summary struct {
	// MaximumPeakRSSBytes is the largest sampled RSS during business/probe load.
	MaximumPeakRSSBytes int64 `json:"maximum_peak_rss_bytes"`
	// Cancellation reports native-context responsiveness independently of drain.
	Cancellation []CancellationSummary `json:"cancellation"`
	// SaturationPlateauConcurrency is the first median sweep point with at
	// most 10% throughput gain and at least 50% higher p95 than its predecessor.
	// Zero means no such plateau was observed, not unlimited capacity.
	SaturationPlateauConcurrency int `json:"saturation_plateau_concurrency"`
	// SaturationBestConcurrency identifies the maximum observed median throughput.
	SaturationBestConcurrency int `json:"saturation_best_concurrency"`
	// SaturationBestRPS is a closed-loop observed maximum, not open-loop capacity.
	SaturationBestRPS float64 `json:"saturation_best_rps"`
	// Saturation retains median load observations at each fixed concurrency.
	Saturation []SaturationStep `json:"saturation"`
	// StartupP95Milliseconds is the 95th-percentile process startup latency.
	StartupP95Milliseconds float64 `json:"startup_p95_milliseconds"`
	// MaximumIdleRSSBytes is the largest resident set size across samples.
	MaximumIdleRSSBytes int64 `json:"maximum_idle_rss_bytes"`
	// JSONRPC summarizes the Postal JSON-RPC samples.
	JSONRPC Load `json:"json_rpc"`
	// TrackIngestion summarizes the Track ingestion samples.
	TrackIngestion Load `json:"track_ingestion"`
	// TrackJSONRPC summarizes the Track JSON-RPC fan-out samples.
	TrackJSONRPC Load `json:"track_json_rpc"`
	// LocationLookup summarizes the Location projection samples.
	LocationLookup Load `json:"location_lookup"`
	// Probe summarizes the readiness samples.
	Probe Load `json:"probe"`
	// ShutdownP95Milliseconds is the 95th-percentile no-work shutdown latency.
	ShutdownP95Milliseconds float64 `json:"shutdown_p95_milliseconds"`
	// ConfiguredDrainP95Milliseconds is the 95th-percentile drain latency.
	ConfiguredDrainP95Milliseconds float64 `json:"configured_drain_p95_milliseconds"`
	// ConfiguredDrainSupported reports whether every sample supports draining.
	ConfiguredDrainSupported bool `json:"configured_drain_supported"`
}

// ParseOHA extracts the stable metrics used by the frozen budgets while the
// complete oha document remains a separate raw artifact. Latencies convert
// seconds to microseconds. Inputs are trusted tool output: document size and
// general metric ranges/schema are not validated here.
func ParseOHA(document []byte) (Load, error) {
	var report struct {
		Summary struct {
			SuccessRate       float64 `json:"successRate"`
			RequestsPerSecond float64 `json:"requestsPerSec"`
		} `json:"summary"`
		Latency     map[string]float64 `json:"latencyPercentiles"`
		StatusCodes map[string]int64   `json:"statusCodeDistribution"`
	}
	if err := json.Unmarshal(document, &report); err != nil {
		return Load{}, err
	}
	p50, p50OK := report.Latency["p50"]
	p95, p95OK := report.Latency["p95"]
	p99, p99OK := report.Latency["p99"]
	if !p50OK || !p95OK || !p99OK ||
		math.IsNaN(report.Summary.SuccessRate) ||
		math.IsNaN(report.Summary.RequestsPerSecond) {
		return Load{}, errors.New("oha output lacks required metrics")
	}
	var responses float64
	for _, count := range report.StatusCodes {
		if count < 0 {
			return Load{}, errors.New("oha output has negative response counts")
		}
		responses += float64(count)
	}
	if responses == 0 {
		return Load{}, errors.New("oha output lacks response status counts")
	}
	// oha's successRate counts completed connections, including HTTP errors.
	// Frozen workload success requires the expected 200 response as well.
	successRate := report.Summary.SuccessRate * float64(report.StatusCodes["200"]) / responses

	return Load{
		SuccessRate:       successRate,
		RequestsPerSecond: report.Summary.RequestsPerSecond,
		P50Microseconds:   p50 * 1_000_000,
		P95Microseconds:   p95 * 1_000_000,
		P99Microseconds:   p99 * 1_000_000,
	}, nil
}

// Summarize calculates medians for noisy metrics and worst observed values for
// absolute resource and success boundaries.
func Summarize(samples []Sample) Summary {
	if len(samples) == 0 {
		return Summary{}
	}
	startup := make([]float64, len(samples))
	shutdown := make([]float64, len(samples))
	configuredDrain := make([]float64, len(samples))
	jsonRPC := make([]Load, len(samples))
	trackIngestion := make([]Load, len(samples))
	trackJSONRPC := make([]Load, len(samples))
	locationLookup := make([]Load, len(samples))
	probe := make([]Load, len(samples))
	maximumRSS := samples[0].IdleRSSBytes
	maximumPeakRSS := samples[0].PeakRSSBytes
	configuredDrainSupported := true
	for index, sample := range samples {
		startup[index] = sample.StartupMilliseconds
		shutdown[index] = sample.ShutdownMilliseconds
		configuredDrain[index] = sample.ConfiguredDrainMilliseconds
		jsonRPC[index] = sample.JSONRPC
		trackIngestion[index] = sample.TrackIngestion
		trackJSONRPC[index] = sample.TrackJSONRPC
		locationLookup[index] = sample.LocationLookup
		probe[index] = sample.Probe
		if sample.IdleRSSBytes > maximumRSS {
			maximumRSS = sample.IdleRSSBytes
		}
		maximumPeakRSS = max(maximumPeakRSS, sample.PeakRSSBytes)
		configuredDrainSupported = configuredDrainSupported &&
			sample.ConfiguredDrainSupported
	}

	saturation := summarizeSaturation(samples)
	plateau, bestConcurrency, bestRPS := saturationAssessment(saturation)
	return Summary{
		SaturationPlateauConcurrency:   plateau,
		SaturationBestConcurrency:      bestConcurrency,
		SaturationBestRPS:              bestRPS,
		MaximumPeakRSSBytes:            maximumPeakRSS,
		Cancellation:                   summarizeCancellation(samples),
		Saturation:                     saturation,
		StartupP95Milliseconds:         percentile(startup, 0.95),
		MaximumIdleRSSBytes:            maximumRSS,
		JSONRPC:                        summarizeLoad(jsonRPC),
		TrackIngestion:                 summarizeLoad(trackIngestion),
		TrackJSONRPC:                   summarizeLoad(trackJSONRPC),
		LocationLookup:                 summarizeLoad(locationLookup),
		Probe:                          summarizeLoad(probe),
		ShutdownP95Milliseconds:        percentile(shutdown, 0.95),
		ConfiguredDrainP95Milliseconds: percentile(configuredDrain, 0.95),
		ConfiguredDrainSupported:       configuredDrainSupported,
	}
}

func summarizeCancellation(samples []Sample) []CancellationSummary {
	var result []CancellationSummary
	for index, first := range samples[0].Cancellation {
		latencies := make([]float64, 0, len(samples))
		waits := make([]float64, 0, len(samples))
		observed := true
		for _, sample := range samples {
			if index >= len(sample.Cancellation) || sample.Cancellation[index].Mode != first.Mode {
				observed = false
				continue
			}
			value := sample.Cancellation[index]
			observed = observed && value.Observed
			waits = append(waits, value.WaitMilliseconds)
			if value.Observed {
				latencies = append(latencies, value.LatencyMilliseconds)
			}
		}
		latency := 0.0
		if observed && len(latencies) == len(samples) {
			latency = percentile(latencies, 0.95)
		}
		result = append(result, CancellationSummary{Mode: first.Mode, Observed: observed, LatencyP95Milliseconds: latency, WaitP95Milliseconds: percentile(waits, 0.95)})
	}
	return result
}

func summarizeSaturation(samples []Sample) []SaturationStep {
	var result []SaturationStep
	for index, first := range samples[0].Saturation {
		var loads []Load
		for _, sample := range samples {
			if index < len(sample.Saturation) && sample.Saturation[index].Concurrency == first.Concurrency {
				loads = append(loads, sample.Saturation[index].Load)
			}
		}
		if len(loads) == len(samples) {
			result = append(result, SaturationStep{Concurrency: first.Concurrency, Load: summarizeLoad(loads)})
		}
	}
	return result
}

func summarizeLoad(loads []Load) Load {
	success := make([]float64, len(loads))
	rps := make([]float64, len(loads))
	p50 := make([]float64, len(loads))
	p95 := make([]float64, len(loads))
	p99 := make([]float64, len(loads))
	for index, load := range loads {
		success[index] = load.SuccessRate
		rps[index] = load.RequestsPerSecond
		p50[index] = load.P50Microseconds
		p95[index] = load.P95Microseconds
		p99[index] = load.P99Microseconds
	}

	return Load{
		SuccessRate:       slices.Min(success),
		RequestsPerSecond: percentile(rps, 0.5),
		P50Microseconds:   percentile(p50, 0.5),
		P95Microseconds:   percentile(p95, 0.5),
		P99Microseconds:   percentile(p99, 0.5),
	}
}

func percentile(values []float64, quantile float64) float64 {
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	if quantile == 0.5 && len(ordered)%2 == 0 {
		middle := len(ordered) / 2

		return (ordered[middle-1] + ordered[middle]) / 2
	}
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}

	return ordered[index]
}

// A diagnostic plateau requires simultaneous throughput flattening and rising
// queue latency. It is not a release budget or a transport-bottleneck diagnosis.
func saturationAssessment(steps []SaturationStep) (int, int, float64) {
	plateau, bestConcurrency, bestRPS := 0, 0, 0.0
	for index, step := range steps {
		if step.Load.RequestsPerSecond > bestRPS {
			bestConcurrency, bestRPS = step.Concurrency, step.Load.RequestsPerSecond
		}
		if index > 0 && plateau == 0 {
			previous := steps[index-1].Load
			if previous.RequestsPerSecond > 0 && previous.P95Microseconds > 0 && step.Load.RequestsPerSecond <= previous.RequestsPerSecond*1.10 && step.Load.P95Microseconds >= previous.P95Microseconds*1.50 {
				plateau = step.Concurrency
			}
		}
	}
	return plateau, bestConcurrency, bestRPS
}
