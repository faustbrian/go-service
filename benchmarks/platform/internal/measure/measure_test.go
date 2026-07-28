package measure_test

import (
	"testing"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/measure"
)

func TestParseOHAExtractsFrozenMetrics(t *testing.T) {
	t.Parallel()

	load, err := measure.ParseOHA([]byte(`{
		"summary":{"successRate":1,"requestsPerSec":81234.5},
		"latencyPercentiles":{"p50":0.0001,"p95":0.0002,"p99":0.0003}
	}`))
	if err != nil {
		t.Fatalf("ParseOHA() error = %v", err)
	}
	if load.SuccessRate != 1 || load.RequestsPerSecond != 81234.5 ||
		load.P50Microseconds != 100 || load.P95Microseconds != 200 ||
		load.P99Microseconds != 300 {
		t.Fatalf("ParseOHA() = %#v", load)
	}
}

func TestSummarizeUsesMediansAndObservedExtremes(t *testing.T) {
	t.Parallel()

	summary := measure.Summarize([]measure.Sample{
		{
			StartupMilliseconds:         10,
			IdleRSSBytes:                10,
			ShutdownMilliseconds:        3,
			ConfiguredDrainMilliseconds: 7,
			ConfiguredDrainSupported:    true,
			JSONRPC: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 80,
				P50Microseconds:   10,
				P95Microseconds:   20,
				P99Microseconds:   30,
			},
			TrackIngestion: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 70,
				P95Microseconds:   22,
			},
			TrackJSONRPC: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 60,
				P95Microseconds:   24,
			},
			LocationLookup: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 90,
				P95Microseconds:   18,
			},
			Probe: measure.Load{SuccessRate: 1, P95Microseconds: 15},
		},
		{
			StartupMilliseconds:         20,
			IdleRSSBytes:                12,
			ShutdownMilliseconds:        5,
			ConfiguredDrainMilliseconds: 9,
			ConfiguredDrainSupported:    true,
			JSONRPC: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 100,
				P50Microseconds:   20,
				P95Microseconds:   30,
				P99Microseconds:   40,
			},
			TrackIngestion: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 90,
				P95Microseconds:   32,
			},
			TrackJSONRPC: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 80,
				P95Microseconds:   34,
			},
			LocationLookup: measure.Load{
				SuccessRate:       1,
				RequestsPerSecond: 110,
				P95Microseconds:   28,
			},
			Probe: measure.Load{SuccessRate: 1, P95Microseconds: 25},
		},
	})
	if summary.StartupP95Milliseconds != 20 ||
		summary.MaximumIdleRSSBytes != 12 ||
		summary.JSONRPC.RequestsPerSecond != 90 ||
		summary.JSONRPC.P95Microseconds != 25 ||
		summary.TrackIngestion.RequestsPerSecond != 80 ||
		summary.TrackIngestion.P95Microseconds != 27 ||
		summary.TrackJSONRPC.RequestsPerSecond != 70 ||
		summary.TrackJSONRPC.P95Microseconds != 29 ||
		summary.LocationLookup.RequestsPerSecond != 100 ||
		summary.LocationLookup.P95Microseconds != 23 ||
		summary.Probe.P95Microseconds != 20 ||
		summary.ShutdownP95Milliseconds != 5 ||
		summary.ConfiguredDrainP95Milliseconds != 9 ||
		!summary.ConfiguredDrainSupported {
		t.Fatalf("Summarize() = %#v", summary)
	}
}
