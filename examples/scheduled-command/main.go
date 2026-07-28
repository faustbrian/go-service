package main

import (
	"context"
	"os"
	"strings"

	"github.com/faustbrian/golib/pkg/service"
)

type configuration struct {
	managementAddress string
}

func main() {
	os.Exit(service.Main(service.Definition{
		Identity: service.Identity{Name: "scheduler"},
		Commands: service.Commands{Schedule: service.CommandFor(
			service.CommandSpec[configuration]{
				Name:    "schedule",
				Summary: "run application schedules",
				Kind:    service.CommandKindLongRunning,
				Load: func(
					_ context.Context,
					invocation service.Invocation,
				) (configuration, error) {
					return configuration{managementAddress: environment(
						invocation.Environment,
						"MANAGEMENT_ADDRESS",
						"",
					)}, nil
				},
				Build: func(
					_ context.Context,
					_ service.BuildContext,
					configuration configuration,
				) (service.Plan, error) {
					return service.Plan{
						Tasks: []service.Task{{
							Name: "scheduler",
							Run:  waitForCancellation,
						}},
						ManagementConfig: &service.Management{
							Address: configuration.managementAddress,
						},
					}, nil
				},
			},
		)},
	}))
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
