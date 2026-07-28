package adoption_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/integration/adoption"
)

func TestReferenceWorkloadsRunThroughCanonicalHTTPBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		definition func(
			*correlation.Factory,
			*readyListener,
			net.Listener,
		) service.Definition
		body     string
		expected string
	}{
		{
			name: "postal search",
			definition: func(
				factory *correlation.Factory,
				business *readyListener,
				management net.Listener,
			) service.Definition {
				return adoption.PostalDefinition(adoption.Postal{
					Identity:    service.Identity{Name: "postal"},
					Load:        loadValue(adoption.PostalConfig{}),
					Correlation: factory,
					Management:  service.Management{Listener: management},
					Serve: service.Plan{HTTP: &service.HTTP{
						Listener: business,
						Handler:  postalSearchHandler(),
					}},
				})
			},
			body:     `{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`,
			expected: `{"jsonrpc":"2.0","result":["00100","00101","00102"]}`,
		},
		{
			name: "track ingestion fanout",
			definition: func(
				factory *correlation.Factory,
				business *readyListener,
				management net.Listener,
			) service.Definition {
				return adoption.TrackDefinition(adoption.Track{
					Identity:    service.Identity{Name: "track"},
					Load:        loadValue(adoption.TrackConfig{}),
					Correlation: factory,
					Management:  service.Management{Listener: management},
					Telemetry:   inertComponent("telemetry"),
					Postgres:    inertComponent("postgres"),
					Kafka:       inertComponent("kafka"),
					HTTP: service.HTTP{
						Listener: business,
						Handler:  trackIngestionHandler(factory),
					},
					Worker: service.Task{Name: "carrier-worker", Run: noWork},
				})
			},
			body:     `{"tracking_number":"JJFI000001","events":["picked-up","in-transit"]}`,
			expected: `{"accepted":2,"child_hops":2}`,
		},
		{
			name: "location lookup",
			definition: func(
				factory *correlation.Factory,
				business *readyListener,
				management net.Listener,
			) service.Definition {
				return adoption.LocationDefinition(adoption.Location{
					Identity:    service.Identity{Name: "location"},
					Load:        loadValue(adoption.LocationConfig{}),
					Correlation: factory,
					Management:  service.Management{Listener: management},
					API: service.Plan{HTTP: &service.HTTP{
						Listener: business,
						Handler:  locationLookupHandler(),
					}},
				})
			},
			body:     `{"carrier":"posti","codes":["001","002","003"]}`,
			expected: `{"locations":[{"code":"001"},{"code":"002"},{"code":"003"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory := deterministicFactory(t)
			response := requestThroughPlatform(
				t,
				func(
					business *readyListener,
					management net.Listener,
				) service.Definition {
					return test.definition(factory, business, management)
				},
				test.body,
			)
			if response.StatusCode != http.StatusOK ||
				string(bytes.TrimSpace(response.Body)) != test.expected {
				t.Fatalf("response = %d %s", response.StatusCode, response.Body)
			}
			if !strings.HasPrefix(
				response.Header.Get("X-Correlation-ID"),
				"adoption-",
			) ||
				!strings.HasPrefix(
					response.Header.Get("X-Request-ID"),
					"adoption-",
				) ||
				response.Header.Get("X-Causation-ID") != "" {
				t.Fatalf("correlation headers = %v", response.Header)
			}
		})
	}
}

func BenchmarkPostalSearchWorkload(b *testing.B) {
	benchmarkHandler(b, postalSearchHandler(), `{
		"jsonrpc":"2.0","method":"postal.search",
		"params":{"query":"00100"}
	}`)
}

func BenchmarkTrackIngestionWorkload(b *testing.B) {
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		b.Fatal(err)
	}
	values, err := factory.Start()
	if err != nil {
		b.Fatal(err)
	}
	benchmarkHandlerWithContext(
		b,
		trackIngestionHandler(factory),
		`{"tracking_number":"JJFI000001","events":["picked-up","in-transit"]}`,
		correlation.WithValues(context.Background(), values),
	)
}

func BenchmarkLocationLookupWorkload(b *testing.B) {
	benchmarkHandler(
		b,
		locationLookupHandler(),
		`{"carrier":"posti","codes":["001","002","003"]}`,
	)
}

func requestThroughPlatform(
	t *testing.T,
	definition func(*readyListener, net.Listener) service.Definition,
	body string,
) platformResponse {
	t.Helper()
	business := newReadyListener(t)
	managementListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management listener: %v", err)
	}
	runtime := definition(business, managementListener)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, runtime, invocation("serve"))
	}()
	select {
	case <-business.accepting:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("business server did not begin accepting")
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"http://"+business.Addr().String()+"/",
		bytes.NewBufferString(body),
	)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		cancel()
		<-result
		t.Fatalf("request: %v", err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	cancel()
	if code := <-result; code != 0 {
		t.Fatalf("serve exit code = %d", code)
	}
	if readErr != nil || closeErr != nil {
		t.Fatalf("response body read = %v, close = %v", readErr, closeErr)
	}

	return platformResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       responseBody,
	}
}

func deterministicFactory(t *testing.T) *correlation.Factory {
	t.Helper()
	var sequence atomic.Uint64
	factory, err := correlation.NewFactory(correlation.FactoryOptions{
		Generator: correlation.GeneratorFunc(func() (string, error) {
			return fmt.Sprintf("adoption-%d", sequence.Add(1)), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}

	return factory
}

func postalSearchHandler() http.Handler {
	type request struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Query string `json:"query"`
		} `json:"params"`
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		var payload request
		if json.NewDecoder(incoming.Body).Decode(&payload) != nil ||
			payload.JSONRPC != "2.0" ||
			payload.Method != "postal.search" ||
			payload.Params.Query == "" {
			http.Error(writer, "invalid request", http.StatusBadRequest)

			return
		}
		_, _ = fmt.Fprintf(
			writer,
			`{"jsonrpc":"2.0","result":["%s","00101","00102"]}`,
			payload.Params.Query,
		)
	})
}

func trackIngestionHandler(factory *correlation.Factory) http.Handler {
	type request struct {
		TrackingNumber string   `json:"tracking_number"`
		Events         []string `json:"events"`
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		values, ok := correlation.FromContext(incoming.Context())
		if !ok {
			http.Error(writer, "missing correlation", http.StatusInternalServerError)

			return
		}
		var payload request
		if json.NewDecoder(incoming.Body).Decode(&payload) != nil ||
			payload.TrackingNumber == "" ||
			len(payload.Events) > 32 {
			http.Error(writer, "invalid request", http.StatusBadRequest)

			return
		}
		children := 0
		for range payload.Events {
			if _, err := factory.Next(values); err != nil {
				http.Error(writer, "correlation failed", http.StatusInternalServerError)

				return
			}
			children++
		}
		_, _ = fmt.Fprintf(
			writer,
			`{"accepted":%d,"child_hops":%d}`,
			len(payload.Events),
			children,
		)
	})
}

func locationLookupHandler() http.Handler {
	type request struct {
		Carrier string   `json:"carrier"`
		Codes   []string `json:"codes"`
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		var payload request
		if json.NewDecoder(incoming.Body).Decode(&payload) != nil ||
			payload.Carrier == "" ||
			len(payload.Codes) > 64 {
			http.Error(writer, "invalid request", http.StatusBadRequest)

			return
		}
		_, _ = writer.Write([]byte(`{"locations":[`))
		for index, code := range payload.Codes {
			if index > 0 {
				_, _ = writer.Write([]byte(","))
			}
			_, _ = fmt.Fprintf(writer, `{"code":"%s"}`, code)
		}
		_, _ = writer.Write([]byte(`]}`))
	})
}

func benchmarkHandler(b *testing.B, handler http.Handler, body string) {
	b.Helper()
	benchmarkHandlerWithContext(b, handler, body, context.Background())
}

func benchmarkHandlerWithContext(
	b *testing.B,
	handler http.Handler,
	body string,
	ctx context.Context,
) {
	b.Helper()
	payload := []byte(body)
	b.ReportAllocs()
	for b.Loop() {
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload)).
			WithContext(ctx)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatalf("status = %d", response.Code)
		}
	}
}

type readyListener struct {
	net.Listener
	accepting chan struct{}
	once      sync.Once
}

type platformResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func newReadyListener(t *testing.T) *readyListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("business listener: %v", err)
	}

	return &readyListener{Listener: listener, accepting: make(chan struct{})}
}

func (listener *readyListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.accepting) })

	return listener.Listener.Accept()
}
