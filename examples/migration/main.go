package main

import (
	"context"
	"os"

	"github.com/faustbrian/golib/pkg/service"
)

func main() {
	os.Exit(service.Main(service.Definition{
		Identity: service.Identity{Name: "migration"},
		Commands: service.Commands{Migrate: service.CommandFor(
			service.CommandSpec[struct{}]{
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
						Run:  runMigrations,
					}}}, nil
				},
			},
		)},
	}))
}

func runMigrations(context.Context) error {
	// Compose the application migration command here. A one-shot plan does not
	// open a management listener unless Management is explicitly enabled.
	return nil
}
