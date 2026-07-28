// Package processcore provides the behavior-matched process boundary used by
// framework comparison binaries.
package processcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 1 << 20
)

// Config defines one equivalent business and management process.
type Config struct {
	// Business is the caller-bound business listener.
	Business net.Listener
	// Management is the caller-bound management listener.
	Management net.Listener
	// Handler serves the equivalent business contract.
	Handler http.Handler
	// ShutdownTimeout bounds each listener's graceful shutdown.
	ShutdownTimeout time.Duration
}

// Main binds benchmark addresses, follows SIGINT and SIGTERM, and returns a
// process exit code without hiding runtime ownership from the candidate.
func Main(handler http.Handler) int {
	business, err := listen(environment("BENCH_BUSINESS_ADDRESS", "127.0.0.1:8080"))
	if err != nil {
		return 1
	}
	management, err := listen(environment("BENCH_MANAGEMENT_ADDRESS", "127.0.0.1:8081"))
	if err != nil {
		_ = business.Close()

		return 1
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	if err := Run(ctx, Config{
		Business:        business,
		Management:      management,
		Handler:         handler,
		ShutdownTimeout: workload.ShutdownTimeout,
	}); err != nil {
		return 1
	}
	if ctx.Err() != nil {
		return 143
	}

	return 0
}

type state struct {
	started atomic.Bool
	ready   atomic.Bool
}

// Run serves both listeners until cancellation or an unexpected server error.
func Run(ctx context.Context, config Config) error {
	if ctx == nil || config.Business == nil || config.Management == nil ||
		config.Handler == nil || config.ShutdownTimeout <= 0 {
		return errors.New("invalid process benchmark configuration")
	}
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return fmt.Errorf("construct correlation: %w", err)
	}
	identity, err := httpcorrelation.New(factory, httpcorrelation.Options{})
	if err != nil {
		return fmt.Errorf("construct HTTP identity: %w", err)
	}
	processState := &state{}
	business := server(config.Business, identity.Wrap(config.Handler))
	management := server(
		config.Management,
		identity.Wrap(managementHandler(processState)),
	)
	failures := make(chan error, 2)
	go serve(business, config.Business, failures)
	go serve(management, config.Management, failures)
	processState.started.Store(true)
	processState.ready.Store(true)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-failures:
	}
	processState.ready.Store(false)
	shutdownContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		config.ShutdownTimeout,
	)
	defer cancel()
	businessErr := business.Shutdown(shutdownContext)
	processState.started.Store(false)
	managementErr := management.Shutdown(shutdownContext)

	return errors.Join(runErr, businessErr, managementErr)
}

func server(listener net.Listener, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func serve(server *http.Server, listener net.Listener, failures chan<- error) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		failures <- err
	}
}

func managementHandler(processState *state) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /livez", probeHandler("liveness", func() bool { return true }))
	mux.Handle("HEAD /livez", probeHandler("liveness", func() bool { return true }))
	mux.Handle("GET /startupz", probeHandler("startup", processState.started.Load))
	mux.Handle("HEAD /startupz", probeHandler("startup", processState.started.Load))
	mux.Handle("GET /readyz", probeHandler("readiness", processState.ready.Load))
	mux.Handle("HEAD /readyz", probeHandler("readiness", processState.ready.Load))

	return mux
}

func probeHandler(probe string, available func() bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		status := http.StatusOK
		state := "ok"
		if !available() {
			status = http.StatusServiceUnavailable
			state = "unavailable"
		}
		writer.WriteHeader(status)
		if request.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"status": state,
			"probe":  probe,
		})
	})
}

func listen(address string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
}

func environment(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}
