package platform_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/faustbrian/golib/pkg/correlation"
	platform "github.com/faustbrian/golib/pkg/service/benchmarks/platform"
)

const postalSearchBody = `{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`

func BenchmarkEquivalentPostalSearch(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/postal/search", postalSearchBody)
}

func BenchmarkEquivalentTrackIngestion(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/track/ingest", trackIngestBody)
}

func BenchmarkEquivalentTrackJSONRPC(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/track/rpc", trackRPCBody)
}

func BenchmarkEquivalentLocationLookup(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/location/lookup", locationLookupBody)
}

func benchmarkEquivalentWorkload(b *testing.B, path string, body string) {
	b.Helper()
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		b.Fatal(err)
	}
	states := []struct {
		name    string
		options platform.Options
	}{
		{name: "disabled"},
		{
			name: "logging",
			options: platform.Options{
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			},
		},
		{
			name: "tracing",
			options: platform.Options{
				Trace: func(ctx context.Context) context.Context {
					return context.WithValue(ctx, traceMarker{}, true)
				},
			},
		},
	}
	for _, state := range states {
		b.Run(state.name, func(b *testing.B) {
			for _, candidate := range platform.Candidates() {
				b.Run(candidate.Name, func(b *testing.B) {
					b.StopTimer()
					endpoint, buildErr := candidate.New(factory, state.options)
					if buildErr != nil {
						b.Fatal(buildErr)
					}
					b.Cleanup(func() {
						if closeErr := endpoint.Close(); closeErr != nil {
							b.Error(closeErr)
						}
					})
					b.ReportAllocs()
					b.StartTimer()
					for b.Loop() {
						request, requestErr := http.NewRequest(
							http.MethodPost,
							"http://benchmark.local"+path,
							strings.NewReader(body),
						)
						if requestErr != nil {
							b.Fatal(requestErr)
						}
						request.Header.Set("Content-Type", "application/json")
						response, requestErr := endpoint.Do(request)
						if requestErr != nil {
							b.Fatal(requestErr)
						}
						_, requestErr = io.Copy(io.Discard, response.Body)
						closeErr := response.Body.Close()
						if requestErr != nil || closeErr != nil ||
							response.StatusCode != http.StatusOK {
							b.Fatalf(
								"response = %d, copy = %v, close = %v",
								response.StatusCode,
								requestErr,
								closeErr,
							)
						}
					}
				})
			}
		})
	}
}

func BenchmarkEquivalentPipelineConstruction(b *testing.B) {
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		b.Fatal(err)
	}
	for _, candidate := range platform.Candidates() {
		b.Run(candidate.Name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				endpoint, buildErr := candidate.New(factory, platform.Options{})
				if buildErr != nil {
					b.Fatal(buildErr)
				}
				if closeErr := endpoint.Close(); closeErr != nil {
					b.Fatal(closeErr)
				}
			}
		})
	}
}

func BenchmarkWorkerDispatchAndSupervision(b *testing.B) {
	benchmarkEquivalentWorkerCandidates(b)
}
