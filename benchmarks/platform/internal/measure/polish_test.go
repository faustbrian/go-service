package measure_test

import (
	"encoding/json"
	"testing"

	"github.com/faustbrian/go-service/benchmarks/platform/internal/measure"
)

func TestSummarizeRetainsPeakSaturationAndCancellationObservations(t *testing.T) {
	var samples []measure.Sample
	if err := json.Unmarshal([]byte(`[{
		"peak_rss_bytes":100,"rss_readings":2,
		"cancellation":[{"mode":"disconnect","observed":true,"latency_milliseconds":3,"wait_milliseconds":3}],
		"saturation":[{"concurrency":1,"load":{"success_rate":1,"requests_per_second":100,"p95_microseconds":10}},{"concurrency":16,"load":{"success_rate":1,"requests_per_second":105,"p95_microseconds":20}}]
	},{
		"peak_rss_bytes":200,"rss_readings":4,
		"cancellation":[{"mode":"disconnect","observed":false,"wait_milliseconds":200}],
		"saturation":[{"concurrency":1,"load":{"success_rate":1,"requests_per_second":120,"p95_microseconds":12}},{"concurrency":16,"load":{"success_rate":1,"requests_per_second":125,"p95_microseconds":24}}]
	}]`), &samples); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(measure.Summarize(samples))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Peak         int64 `json:"maximum_peak_rss_bytes"`
		Cancellation []struct {
			Observed bool    `json:"observed"`
			Wait     float64 `json:"wait_p95_milliseconds"`
		} `json:"cancellation"`
		Saturation []struct {
			Concurrency int          `json:"concurrency"`
			Load        measure.Load `json:"load"`
		} `json:"saturation"`
	}
	if err := json.Unmarshal(document, &got); err != nil {
		t.Fatal(err)
	}
	if got.Peak != 200 || len(got.Cancellation) != 1 || got.Cancellation[0].Observed || got.Cancellation[0].Wait != 200 || len(got.Saturation) != 2 || got.Saturation[1].Load.RequestsPerSecond != 115 {
		t.Fatalf("lost named comparative observations: %s", document)
	}
}

func TestParseOHACountsHTTPFailuresNotOnlyCompletedConnections(t *testing.T) {
	load, err := measure.ParseOHA([]byte(`{"summary":{"successRate":1,"requestsPerSec":100},"latencyPercentiles":{"p50":0.0001,"p95":0.0002,"p99":0.0003},"statusCodeDistribution":{"200":90,"500":10}}`))
	if err != nil {
		t.Fatal(err)
	}
	if load.SuccessRate != 0.9 {
		t.Fatalf("HTTP failures hidden by successful connections: success=%g", load.SuccessRate)
	}
}
