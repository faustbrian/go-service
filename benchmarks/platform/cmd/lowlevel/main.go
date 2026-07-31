package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/faustbrian/golib/pkg/service/healthhttp"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	runtime, err := service.New(service.Config{})
	if err != nil {
		return 1
	}
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	businessListener, err := listen("BENCH_BUSINESS_ADDRESS", "127.0.0.1:8080")
	if err != nil {
		return 1
	}
	managementListener, err := listen("BENCH_MANAGEMENT_ADDRESS", "127.0.0.1:8081")
	if err != nil {
		_ = businessListener.Close()

		return 1
	}
	businessOptions := []serverhttp.Option{
		serverhttp.WithBodyLimit(workload.BodyLimit),
		serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
		serverhttp.WithShutdownTimeout(workload.ShutdownTimeout),
		serverhttp.WithMiddleware(workload.SecurityHeaders),
	}
	if options.Logger != nil || options.Trace != nil {
		businessOptions = append(
			businessOptions,
			serverhttp.WithMiddleware(func(next http.Handler) http.Handler {
				return workload.Optional(next, options)
			}),
		)
	}
	business, err := serverhttp.New(
		businessListener,
		workload.NewMux(factory),
		businessOptions...,
	)
	if err != nil {
		_ = businessListener.Close()
		_ = managementListener.Close()

		return 1
	}
	probes, err := healthhttp.New(healthhttp.Config{Lifecycle: runtime})
	if err != nil {
		_ = business.Close()
		_ = managementListener.Close()

		return 1
	}
	managementMux := http.NewServeMux()
	managementMux.Handle("/livez", probes.Liveness())
	managementMux.Handle("/startupz", probes.Startup())
	managementMux.Handle("/readyz", probes.Readiness())
	management, err := serverhttp.New(
		managementListener,
		managementMux,
		serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
		serverhttp.WithShutdownTimeout(workload.ShutdownTimeout),
	)
	if err != nil {
		_ = business.Close()
		_ = managementListener.Close()

		return 1
	}
	if err := runtime.Start(ctx); err != nil {
		_ = business.Close()
		_ = management.Close()

		return 1
	}
	businessContext, cancelBusiness := context.WithCancel(context.Background())
	managementContext, cancelManagement := context.WithCancel(context.Background())
	businessDone := make(chan error, 1)
	managementDone := make(chan error, 1)
	events := make(chan error, 2)
	go func() {
		result := business.Run(businessContext)
		businessDone <- result
		events <- result
	}()
	go func() {
		result := management.Run(managementContext)
		managementDone <- result
		events <- result
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-events:
	}
	_ = runtime.Drain()
	cancelBusiness()
	runErr = errors.Join(runErr, <-businessDone)
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	runErr = errors.Join(runErr, runtime.Shutdown(shutdownContext))
	cancelShutdown()
	cancelManagement()
	runErr = errors.Join(runErr, <-managementDone)

	if runErr != nil {
		return 1
	}
	if ctx.Err() != nil {
		return 143
	}

	return 0
}

//lint:ignore U1000 Used by the build-tag-specific process runner.
func listen(name string, fallback string) (net.Listener, error) { //nolint:unused // Used by tagged runner.
	address := os.Getenv(name)
	if address == "" {
		address = fallback
	}

	return (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
}
