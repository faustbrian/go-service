package platform_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/faustbrian/golib/pkg/correlation"
	platform "github.com/faustbrian/golib/pkg/service/benchmarks/platform"
)

type traceMarker struct{}

type response struct {
	statusCode int
	header     http.Header
	body       string
}

const (
	correlationHeader  = "X-Correlation-ID"
	requestHeader      = "X-Request-ID"
	causationHeader    = "X-Causation-ID"
	trackIngestBody    = `{"tracking_number":"JJFI000001","events":["picked-up","in-transit"]}`
	trackRPCBody       = `{"jsonrpc":"2.0","method":"track.ingest","params":{"tracking_number":"JJFI000001","events":["picked-up","in-transit"]}}`
	locationLookupBody = `{"carrier":"posti","codes":["001","002","003"]}`
)

func TestCandidatesExposeTheFrozenComparisonSet(t *testing.T) {
	t.Parallel()

	candidates := platform.Candidates()
	names := make([]string, len(candidates))
	for index, candidate := range candidates {
		names[index] = candidate.Name
		if candidate.IncompatibleRuntime != (candidate.Name == "fiber-fasthttp") {
			t.Errorf(
				"%s incompatible runtime = %t",
				candidate.Name,
				candidate.IncompatibleRuntime,
			)
		}
	}
	const expected = "plain-net-http,low-level-service,cohesive-service," +
		"chi,gin,echo,fiber-fasthttp"
	if strings.Join(names, ",") != expected {
		t.Fatalf("candidate names = %v", names)
	}
}

func TestCandidatesPreserveEquivalentHTTPBehavior(t *testing.T) {
	t.Parallel()

	for _, candidate := range platform.Candidates() {
		t.Run(candidate.Name, func(t *testing.T) {
			endpoint, err := candidate.New(deterministicFactory(t), platform.Options{})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() {
				if closeErr := endpoint.Close(); closeErr != nil {
					t.Errorf("Close() error = %v", closeErr)
				}
			})

			success := perform(
				t,
				endpoint,
				http.MethodPost,
				"/postal/search",
				`{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`,
				map[string]string{
					correlationHeader: "untrusted-workflow",
					requestHeader:     "untrusted-request",
					causationHeader:   "untrusted-parent",
				},
			)
			assertResponse(
				t,
				success,
				http.StatusOK,
				`{"jsonrpc":"2.0","result":["00100","00101","00102"]}`,
			)
			if success.header.Get(correlationHeader) != "benchmark-1" ||
				success.header.Get(requestHeader) != "benchmark-2" ||
				success.header.Get(causationHeader) != "" {
				t.Fatalf("success identity headers = %v", success.header)
			}

			oversized := perform(
				t,
				endpoint,
				http.MethodPost,
				"/postal/search",
				strings.Repeat("x", platform.BodyLimit+1),
				nil,
			)
			if oversized.statusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized status = %d", oversized.statusCode)
			}
			assertFreshIdentity(t, oversized)

			malformed := perform(
				t,
				endpoint,
				http.MethodPost,
				"/postal/search",
				`{"jsonrpc":"2.0","method":"postal.search","params":{}}`,
				nil,
			)
			assertResponse(
				t,
				malformed,
				http.StatusBadRequest,
				`{"error":"invalid request"}`,
			)
			assertFreshIdentity(t, malformed)

			panicked := perform(t, endpoint, http.MethodGet, "/panic", "", nil)
			assertResponse(
				t,
				panicked,
				http.StatusInternalServerError,
				"internal server error",
			)
			assertFreshIdentity(t, panicked)
		})
	}
}

func TestCandidatesPreserveReferenceWorkloadBehavior(t *testing.T) {
	t.Parallel()

	workloads := []struct {
		name     string
		path     string
		body     string
		expected string
	}{
		{
			name:     "track ingestion",
			path:     "/track/ingest",
			body:     trackIngestBody,
			expected: `{"accepted":2,"child_hops":2}`,
		},
		{
			name:     "track JSON-RPC",
			path:     "/track/rpc",
			body:     trackRPCBody,
			expected: `{"jsonrpc":"2.0","result":{"accepted":2,"child_hops":2}}`,
		},
		{
			name:     "location lookup",
			path:     "/location/lookup",
			body:     locationLookupBody,
			expected: `{"locations":[{"code":"001"},{"code":"002"},{"code":"003"}]}`,
		},
		{
			name:     "configured drain work",
			path:     "/_benchmark/drain",
			body:     `{}`,
			expected: `{"drained":true}`,
		},
	}
	for _, candidate := range platform.Candidates() {
		t.Run(candidate.Name, func(t *testing.T) {
			endpoint, err := candidate.New(
				deterministicFactory(t),
				platform.Options{},
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() {
				if closeErr := endpoint.Close(); closeErr != nil {
					t.Errorf("Close() error = %v", closeErr)
				}
			})
			for _, workload := range workloads {
				t.Run(workload.name, func(t *testing.T) {
					actual := perform(
						t,
						endpoint,
						http.MethodPost,
						workload.path,
						workload.body,
						nil,
					)
					assertResponse(
						t,
						actual,
						http.StatusOK,
						workload.expected,
					)
					assertFreshIdentity(t, actual)
				})
			}
		})
	}
}

func TestCandidatesPreserveBehaviorWithOptionalFacilities(t *testing.T) {
	t.Parallel()

	for _, candidate := range platform.Candidates() {
		t.Run(candidate.Name, func(t *testing.T) {
			var traced atomic.Uint64
			endpoint, err := candidate.New(deterministicFactory(t), platform.Options{
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Trace: func(ctx context.Context) context.Context {
					traced.Add(1)

					return context.WithValue(ctx, traceMarker{}, true)
				},
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() {
				if closeErr := endpoint.Close(); closeErr != nil {
					t.Errorf("Close() error = %v", closeErr)
				}
			})
			response := perform(
				t,
				endpoint,
				http.MethodPost,
				"/postal/search",
				`{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`,
				nil,
			)
			assertResponse(
				t,
				response,
				http.StatusOK,
				`{"jsonrpc":"2.0","result":["00100","00101","00102"]}`,
			)
			if traced.Load() != 1 {
				t.Fatalf("trace calls = %d", traced.Load())
			}
		})
	}
}

func TestCandidatesAcceptTheDefaultCorrelationFactory(t *testing.T) {
	t.Parallel()

	for _, candidate := range platform.Candidates() {
		t.Run(candidate.Name, func(t *testing.T) {
			endpoint, err := candidate.New(nil, platform.Options{})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() {
				if closeErr := endpoint.Close(); closeErr != nil {
					t.Errorf("Close() error = %v", closeErr)
				}
			})
			response := perform(
				t,
				endpoint,
				http.MethodPost,
				"/postal/search",
				`{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`,
				nil,
			)
			if response.statusCode != http.StatusOK ||
				response.header.Get(correlationHeader) == "" ||
				response.header.Get(requestHeader) == "" {
				t.Fatalf(
					"default factory response = %d %v",
					response.statusCode,
					response.header,
				)
			}
		})
	}
}

func perform(
	t *testing.T,
	endpoint platform.Endpoint,
	method string,
	path string,
	body string,
	headers map[string]string,
) response {
	t.Helper()
	request, err := http.NewRequest(method, "http://benchmark.local"+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	httpResponse, err := endpoint.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	bodyBytes, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		_ = httpResponse.Body.Close()
		t.Fatalf("read response: %v", err)
	}
	if closeErr := httpResponse.Body.Close(); closeErr != nil {
		t.Fatalf("close response: %v", closeErr)
	}

	return response{
		statusCode: httpResponse.StatusCode,
		header:     httpResponse.Header,
		body:       string(bodyBytes),
	}
}

func assertResponse(
	t *testing.T,
	response response,
	status int,
	expectedBody string,
) {
	t.Helper()
	if response.statusCode != status ||
		strings.TrimSpace(response.body) != expectedBody {
		t.Fatalf("response = %d %q", response.statusCode, response.body)
	}
}

func assertFreshIdentity(t *testing.T, response response) {
	t.Helper()
	if !strings.HasPrefix(response.header.Get(correlationHeader), "benchmark-") ||
		!strings.HasPrefix(response.header.Get(requestHeader), "benchmark-") ||
		response.header.Get(causationHeader) != "" {
		t.Fatalf("identity headers = %v", response.header)
	}
}

func deterministicFactory(t *testing.T) *correlation.Factory {
	t.Helper()
	var sequence atomic.Uint64
	factory, err := correlation.NewFactory(correlation.FactoryOptions{
		Generator: correlation.GeneratorFunc(func() (string, error) {
			return fmt.Sprintf("benchmark-%d", sequence.Add(1)), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}

	return factory
}
