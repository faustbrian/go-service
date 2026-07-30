// Package workload defines the behavior shared by process-level framework
// comparisons.
package workload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
)

const (
	// BodyLimit is the equivalent request-body boundary for every candidate.
	BodyLimit = 1024
	// DrainWork is deterministic in-flight work used to measure graceful drain.
	DrainWork = 10 * time.Millisecond
	// ShutdownTimeout is the declared process drain deadline.
	ShutdownTimeout = 50 * time.Millisecond
)

// Trace derives caller-owned trace context without installing a global
// propagator.
type Trace func(context.Context) context.Context

// Options controls equivalent optional logging and tracing behavior.
type Options struct {
	// Logger enables one bounded completion log for each request.
	Logger *slog.Logger
	// Trace enables caller-owned context derivation after correlation.
	Trace Trace
}

// NewMux constructs the standard-library benchmark routes.
func NewMux(factory *correlation.Factory) http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("POST /postal/search", PostalHTTP)
	router.HandleFunc("POST /track/ingest", TrackHTTP(factory))
	router.HandleFunc("POST /track/rpc", TrackRPCHTTP(factory))
	router.HandleFunc("POST /location/lookup", LocationHTTP)
	router.HandleFunc("POST /_benchmark/drain", DrainHTTP)
	router.HandleFunc("GET /panic", PanicHTTP)

	return router
}

// Standard installs equivalent safety and observability behavior around a
// caller-owned router.
func Standard(
	router http.Handler,
	factory *correlation.Factory,
	options Options,
) (http.Handler, error) {
	if factory == nil {
		var err error
		factory, err = correlation.NewFactory(correlation.FactoryOptions{})
		if err != nil {
			return nil, err
		}
	}
	identity, err := httpcorrelation.New(factory, httpcorrelation.Options{})
	if err != nil {
		return nil, err
	}
	handler := LimitBody(router)
	handler = Optional(handler, options)
	handler = identity.Wrap(handler)
	handler = Recover(handler)

	return handler, nil
}

// Optional applies equivalent optional logging and tracing behavior.
func Optional(next http.Handler, options Options) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if options.Trace != nil {
			request = request.WithContext(options.Trace(request.Context()))
		}
		started := time.Now()
		next.ServeHTTP(writer, request)
		if options.Logger != nil {
			options.Logger.InfoContext(
				request.Context(),
				"benchmark request",
				"method",
				request.Method,
				"elapsed",
				time.Since(started),
			)
		}
	})
}

// LimitBody rejects request bodies above the frozen comparison boundary.
func LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength > BodyLimit {
			http.Error(writer, "request body too large", http.StatusRequestEntityTooLarge)

			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, BodyLimit)
		next.ServeHTTP(writer, request)
	})
}

// Recover converts a panic into the equivalent generic response.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				http.Error(
					writer,
					"internal server error",
					http.StatusInternalServerError,
				)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

// PostalHTTP executes the frozen Postal-style request contract.
func PostalHTTP(writer http.ResponseWriter, request *http.Request) {
	status, body := PostalResponse(request.Body)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

// PanicHTTP exercises equivalent panic containment.
func PanicHTTP(http.ResponseWriter, *http.Request) {
	panic("benchmark panic")
}

// PostalResponse decodes and evaluates the frozen deterministic workload.
func PostalResponse(body io.Reader) (int, []byte) {
	var request struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Query string `json:"query"`
		} `json:"params"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&request); err != nil ||
		request.JSONRPC != "2.0" ||
		request.Method != "postal.search" ||
		request.Params.Query == "" {
		return http.StatusBadRequest, []byte(`{"error":"invalid request"}`)
	}

	response, err := json.Marshal(struct {
		JSONRPC string   `json:"jsonrpc"`
		Result  []string `json:"result"`
	}{
		JSONRPC: "2.0",
		Result:  []string{request.Params.Query, "00101", "00102"},
	})
	if err != nil {
		return http.StatusInternalServerError, []byte(`{"error":"encoding failed"}`)
	}

	return http.StatusOK, response
}

// TrackHTTP executes the frozen Track-style ingestion request contract.
func TrackHTTP(factory *correlation.Factory) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		status, body := TrackResponse(
			request.Body,
			request.Context(),
			factory,
			false,
		)
		writeJSON(writer, status, body)
	}
}

// TrackRPCHTTP executes the frozen Track-style JSON-RPC request contract.
func TrackRPCHTTP(factory *correlation.Factory) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		status, body := TrackResponse(
			request.Body,
			request.Context(),
			factory,
			true,
		)
		writeJSON(writer, status, body)
	}
}

// TrackResponse decodes one bounded Track workload and creates one child hop
// for every accepted event.
func TrackResponse(
	body io.Reader,
	ctx context.Context,
	factory *correlation.Factory,
	jsonRPC bool,
) (int, []byte) {
	var request struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			TrackingNumber string   `json:"tracking_number"`
			Events         []string `json:"events"`
		} `json:"params"`
		TrackingNumber string   `json:"tracking_number"`
		Events         []string `json:"events"`
	}
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		return http.StatusBadRequest, []byte(`{"error":"invalid request"}`)
	}
	trackingNumber := request.TrackingNumber
	events := request.Events
	if jsonRPC {
		if request.JSONRPC != "2.0" || request.Method != "track.ingest" {
			return http.StatusBadRequest, []byte(`{"error":"invalid request"}`)
		}
		trackingNumber = request.Params.TrackingNumber
		events = request.Params.Events
	}
	if trackingNumber == "" || len(events) == 0 || len(events) > 32 ||
		factory == nil {
		return http.StatusBadRequest, []byte(`{"error":"invalid request"}`)
	}
	values, ok := correlation.FromContext(ctx)
	if !ok {
		return http.StatusInternalServerError, []byte(`{"error":"missing correlation"}`)
	}
	for range events {
		if _, err := factory.Next(values); err != nil {
			return http.StatusInternalServerError, []byte(`{"error":"correlation failed"}`)
		}
	}
	result := fmt.Sprintf(
		`{"accepted":%d,"child_hops":%d}`,
		len(events),
		len(events),
	)
	if jsonRPC {
		result = `{"jsonrpc":"2.0","result":` + result + `}`
	}

	return http.StatusOK, []byte(result)
}

// LocationHTTP executes the frozen Location-style lookup request contract.
func LocationHTTP(writer http.ResponseWriter, request *http.Request) {
	status, body := LocationResponse(request.Body)
	writeJSON(writer, status, body)
}

// LocationResponse decodes and projects one bounded Location workload.
func LocationResponse(body io.Reader) (int, []byte) {
	var request struct {
		Carrier string   `json:"carrier"`
		Codes   []string `json:"codes"`
	}
	if err := json.NewDecoder(body).Decode(&request); err != nil ||
		request.Carrier == "" ||
		len(request.Codes) == 0 ||
		len(request.Codes) > 64 {
		return http.StatusBadRequest, []byte(`{"error":"invalid request"}`)
	}
	locations := make([]struct {
		Code string `json:"code"`
	}, 0, len(request.Codes))
	for _, code := range request.Codes {
		if code == "" {
			return http.StatusBadRequest, []byte(`{"error":"invalid request"}`)
		}
		locations = append(locations, struct {
			Code string `json:"code"`
		}{Code: code})
	}
	response, err := json.Marshal(struct {
		Locations []struct {
			Code string `json:"code"`
		} `json:"locations"`
	}{Locations: locations})
	if err != nil {
		return http.StatusInternalServerError, []byte(`{"error":"encoding failed"}`)
	}

	return http.StatusOK, response
}

// DrainHTTP flushes a successful response header before completing one
// deterministic in-flight work unit.
func DrainHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = http.NewResponseController(writer).Flush()
	WaitForDrain(request.Context())
	_, _ = writer.Write([]byte(`{"drained":true}`))
}

// WaitForDrain intentionally remains in flight after request cancellation so
// the process harness measures the configured graceful-drain boundary.
func WaitForDrain(_ context.Context) {
	timer := time.NewTimer(DrainWork)
	defer timer.Stop()
	<-timer.C
}

func writeJSON(writer http.ResponseWriter, status int, body []byte) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}
