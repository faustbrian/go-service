package platform_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/faustbrian/go-correlation"
	platform "github.com/faustbrian/go-service/benchmarks/platform"
)

const postalSearchBody = `{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`

// BenchmarkEquivalentPostalSearch compares one fixed synthetic Postal request
// across candidates and middleware modes, not production search or network IO.
func BenchmarkEquivalentPostalSearch(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/postal/search", postalSearchBody)
}

// BenchmarkEquivalentTrackIngestion compares the fixed ingestion fan-out shape
// across candidates; handler setup is outside timed request/response work.
func BenchmarkEquivalentTrackIngestion(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/track/ingest", trackIngestBody)
}

// BenchmarkEquivalentTrackJSONRPC compares the fixed JSON-RPC-shaped fan-out
// request, not a complete JSON-RPC implementation or live carrier pipeline.
func BenchmarkEquivalentTrackJSONRPC(b *testing.B) {
	benchmarkEquivalentWorkload(b, "/track/rpc", trackRPCBody)
}

// BenchmarkEquivalentLocationLookup compares the fixed projection shape across
// candidate adapters, retaining their disclosed net/http versus fasthttp differences.
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

// BenchmarkEquivalentPipelineConstruction includes fresh endpoint construction
// and Close, but excludes correlation-factory setup and request execution.
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

// BenchmarkWorkerDispatchAndSupervision compares the synthetic worker fixture
// and lifecycle supervision; it is not broker persistence/delivery qualification.
func BenchmarkWorkerDispatchAndSupervision(b *testing.B) {
	benchmarkEquivalentWorkerCandidates(b)
}
