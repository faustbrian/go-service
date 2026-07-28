package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/faustbrian/golib/pkg/service"
)

type configuration struct {
	businessAddress   string
	managementAddress string
}

func main() {
	os.Exit(service.Main(service.Definition{
		Identity: service.Identity{Name: "mixed-command-service"},
		Commands: service.Commands{
			Serve:    serveCommand(),
			Worker:   longRunningCommand("worker", "consume application work"),
			Schedule: longRunningCommand("schedule", "run application schedules"),
			Migrate:  migrateCommand(),
		},
	}))
}

func serveCommand() service.Command {
	return service.CommandFor(service.CommandSpec[configuration]{
		Name:    "serve",
		Summary: "serve application requests",
		Kind:    service.CommandKindLongRunning,
		Load:    load,
		Build: func(
			_ context.Context,
			_ service.BuildContext,
			configuration configuration,
		) (service.Plan, error) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /", func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				_, _ = writer.Write([]byte("mixed-command-service\n"))
			})

			return service.Plan{
				HTTP: &service.HTTP{
					Address: configuration.businessAddress,
					Handler: mux,
				},
				ManagementConfig: &service.Management{
					Address: configuration.managementAddress,
				},
			}, nil
		},
	})
}

func longRunningCommand(name string, summary string) service.Command {
	return service.CommandFor(service.CommandSpec[configuration]{
		Name:    name,
		Summary: summary,
		Kind:    service.CommandKindLongRunning,
		Load:    load,
		Build: func(
			_ context.Context,
			_ service.BuildContext,
			configuration configuration,
		) (service.Plan, error) {
			return service.Plan{
				Tasks: []service.Task{{
					Name: name,
					Run:  waitForCancellation,
				}},
				ManagementConfig: &service.Management{
					Address: configuration.managementAddress,
				},
			}, nil
		},
	})
}

func migrateCommand() service.Command {
	return service.CommandFor(service.CommandSpec[struct{}]{
		Name:    "migrate",
		Summary: "run application migrations",
		Kind:    service.CommandKindOneShot,
		Load: func(
			context.Context,
			service.Invocation,
		) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "migrations",
				Run:  func(context.Context) error { return nil },
			}}}, nil
		},
	})
}

func load(
	_ context.Context,
	invocation service.Invocation,
) (configuration, error) {
	return configuration{
		businessAddress: environment(
			invocation.Environment,
			"LISTEN_ADDRESS",
			"127.0.0.1:8080",
		),
		managementAddress: environment(
			invocation.Environment,
			"MANAGEMENT_ADDRESS",
			"",
		),
	}, nil
}

func waitForCancellation(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

func environment(values []string, name string, fallback string) string {
	prefix := name + "="
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}

	return fallback
}
