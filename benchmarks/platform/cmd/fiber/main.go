package main

import (
	"bytes"
	"context"
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
	"github.com/gofiber/fiber/v3"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	business := fiber.New(fiber.Config{
		BodyLimit:   workload.BodyLimit + 1,
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	})
	business.Use(identity(factory, options))
	business.Post("/postal/search", func(ctx fiber.Ctx) error {
		status, body := workload.PostalResponse(bytes.NewReader(ctx.Req().BodyRaw()))
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	business.Post("/track/ingest", func(ctx fiber.Ctx) error {
		status, body := workload.TrackResponse(
			bytes.NewReader(ctx.Req().BodyRaw()),
			ctx.Context(),
			factory,
			false,
		)
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	business.Post("/track/rpc", func(ctx fiber.Ctx) error {
		status, body := workload.TrackResponse(
			bytes.NewReader(ctx.Req().BodyRaw()),
			ctx.Context(),
			factory,
			true,
		)
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	business.Post("/location/lookup", func(ctx fiber.Ctx) error {
		status, body := workload.LocationResponse(bytes.NewReader(ctx.Req().BodyRaw()))
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	business.Post("/_benchmark/drain", func(ctx fiber.Ctx) error {
		workload.WaitForDrain(ctx.Context())

		return ctx.Status(http.StatusOK).JSON(map[string]bool{"drained": true})
	})
	business.Get("/panic", func(fiber.Ctx) error { panic("benchmark panic") })

	var started atomic.Bool
	var ready atomic.Bool
	management := fiber.New(fiber.Config{
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	})
	management.Use(identity(factory, workload.Options{}))
	management.All("/livez", probe("liveness", func() bool { return true }))
	management.All("/startupz", probe("startup", started.Load))
	management.All("/readyz", probe("readiness", ready.Load))

	businessListener, err := listen("BENCH_BUSINESS_ADDRESS", "127.0.0.1:8080")
	if err != nil {
		return 1
	}
	managementListener, err := listen("BENCH_MANAGEMENT_ADDRESS", "127.0.0.1:8081")
	if err != nil {
		_ = businessListener.Close()

		return 1
	}
	businessDone := make(chan error, 1)
	managementDone := make(chan error, 1)
	go func() {
		businessDone <- business.Listener(
			businessListener,
			fiber.ListenConfig{DisableStartupMessage: true},
		)
	}()
	go func() {
		managementDone <- management.Listener(
			managementListener,
			fiber.ListenConfig{DisableStartupMessage: true},
		)
	}()
	started.Store(true)
	ready.Store(true)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	var runErr error
	signaled := false
	select {
	case <-signals:
		signaled = true
	case runErr = <-businessDone:
	case runErr = <-managementDone:
	}
	ready.Store(false)
	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		time.Second,
	)
	defer cancel()
	if err := business.ShutdownWithContext(shutdownContext); err != nil {
		runErr = err
	}
	started.Store(false)
	if err := management.ShutdownWithContext(shutdownContext); err != nil && runErr == nil {
		runErr = err
	}

	if runErr != nil {
		return 1
	}
	if signaled {
		return 143
	}

	return 0
}

//lint:ignore U1000 Used by the build-tag-specific process runner.
//nolint:unused // Used by the tagged runner.
func identity(
	factory *correlation.Factory,
	options workload.Options,
) fiber.Handler {
	return func(ctx fiber.Ctx) (err error) {
		values, startErr := factory.Start()
		if startErr != nil {
			return startErr
		}
		ctx.Set(httpcorrelation.CorrelationHeader, values.CorrelationID.String())
		ctx.Set(httpcorrelation.RequestHeader, values.RequestID.String())
		ctx.SetContext(correlation.WithValues(ctx.Context(), values))
		if options.Trace != nil {
			ctx.SetContext(options.Trace(ctx.Context()))
		}
		started := time.Now()
		defer func() {
			if recover() != nil {
				err = ctx.Status(http.StatusInternalServerError).
					SendString("internal server error")
			}
			if options.Logger != nil {
				options.Logger.InfoContext(
					ctx.Context(),
					"benchmark request",
					"method",
					ctx.Method(),
					"elapsed",
					time.Since(started),
				)
			}
		}()
		if len(ctx.Req().BodyRaw()) > workload.BodyLimit {
			return ctx.Status(http.StatusRequestEntityTooLarge).
				SendString("request body too large")
		}
		ctx.Set("X-Content-Type-Options", "nosniff")

		return ctx.Next()
	}
}

//lint:ignore U1000 Used by the build-tag-specific process runner.
func probe(name string, available func() bool) fiber.Handler { //nolint:unused // Used by tagged runner.
	return func(ctx fiber.Ctx) error {
		ctx.Set("Content-Type", "application/json")
		ctx.Set("Cache-Control", "no-store")
		ctx.Set("X-Content-Type-Options", "nosniff")
		if ctx.Method() != http.MethodGet && ctx.Method() != http.MethodHead {
			ctx.Set("Allow", "GET, HEAD")

			return ctx.SendStatus(http.StatusMethodNotAllowed)
		}
		status := http.StatusOK
		value := "ok"
		if !available() {
			status = http.StatusServiceUnavailable
			value = "unavailable"
		}
		if ctx.Method() == http.MethodHead {
			return ctx.SendStatus(status)
		}

		return ctx.Status(status).JSON(map[string]string{
			"status": value,
			"probe":  name,
		})
	}
}

//lint:ignore U1000 Used by the build-tag-specific process runner.
func listen(name string, fallback string) (net.Listener, error) { //nolint:unused // Used by tagged runner.
	address := os.Getenv(name)
	if address == "" {
		address = fallback
	}

	return (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
}
