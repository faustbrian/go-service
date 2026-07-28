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

// Sample is one independently started process observation.
type Sample struct {
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
// complete oha document remains a separate raw artifact.
func ParseOHA(document []byte) (Load, error) {
	var report struct {
		Summary struct {
			SuccessRate       float64 `json:"successRate"`
			RequestsPerSecond float64 `json:"requestsPerSec"`
		} `json:"summary"`
		Latency map[string]float64 `json:"latencyPercentiles"`
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

	return Load{
		SuccessRate:       report.Summary.SuccessRate,
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
		configuredDrainSupported = configuredDrainSupported &&
			sample.ConfiguredDrainSupported
	}

	return Summary{
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
