package main

import (
	"context"
	"net/http"
	"os"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	definition := service.Definition{
		Identity:    service.Identity{Name: "cohesive-benchmark"},
		Correlation: factory,
		Commands: service.Commands{Serve: service.CommandFor(service.CommandSpec[struct{}]{
			Name:    "serve",
			Summary: "run the equivalent benchmark process",
			Kind:    service.CommandKindLongRunning,
			Load: func(context.Context, service.Invocation) (struct{}, error) {
				return struct{}{}, nil
			},
			Build: func(
				context.Context,
				service.BuildContext,
				struct{},
			) (service.Plan, error) {
				httpOptions := []serverhttp.Option{
					serverhttp.WithBodyLimit(workload.BodyLimit),
					serverhttp.WithShutdownTimeout(workload.ShutdownTimeout),
				}
				if options.Logger != nil {
					httpOptions = append(
						httpOptions,
						serverhttp.WithMiddleware(func(next http.Handler) http.Handler {
							return workload.Optional(
								next,
								workload.Options{Logger: options.Logger},
							)
						}),
					)
				}

				return service.Plan{HTTP: &service.HTTP{
					Address: address("BENCH_BUSINESS_ADDRESS", "127.0.0.1:8080"),
					Handler: workload.NewMux(factory),
					Options: httpOptions,
				}}, nil
			},
		})},
		Management: service.Management{
			Address: address("BENCH_MANAGEMENT_ADDRESS", "127.0.0.1:8081"),
		},
	}
	if options.Logger != nil {
		definition.Logger = options.Logger
	}
	if options.Trace != nil {
		definition.TracePropagation = func(next http.Handler) http.Handler {
			return workload.Optional(next, workload.Options{Trace: options.Trace})
		}
	}
	return service.Main(definition)
}

//lint:ignore U1000 Used by the build-tag-specific process runner.
func address(name string, fallback string) string { //nolint:unused // Used by the tagged runner.
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}
