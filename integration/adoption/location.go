package adoption

import (
	"context"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
)

// LocationConfig represents Location-owned typed configuration at the
// platform seam.
type LocationConfig struct{}

// Location preserves distinct API, worker, scheduler, schema migration,
// online migration, and activation dependency plans.
type Location struct {
	// Identity is the Location process identity.
	Identity service.Identity
	// Load resolves Location-owned typed configuration.
	Load func(context.Context, service.Invocation) (LocationConfig, error)
	// Correlation creates Location's process and request identities.
	Correlation *correlation.Factory
	// Management configures the separate operational listener.
	Management service.Management
	// API retains Location lookup handlers and dependencies.
	API service.Plan
	// Worker retains catalogue import dependencies and handlers.
	Worker service.Plan
	// Schedule retains Location schedules, leases, and executor.
	Schedule service.Plan
	// Migrate is the owning migrations module's schema command.
	Migrate service.Command
	// OnlineMigrate retains online reconciliation and reporting behavior.
	OnlineMigrate service.Task
	// Activate retains the application-owned catalogue activation behavior.
	Activate service.Task
}

// LocationDefinition composes Location's standard and domain-specific roles.
func LocationDefinition(input Location) service.Definition {
	return service.Definition{
		Identity:    input.Identity,
		Correlation: input.Correlation,
		Management:  input.Management,
		Commands: service.Commands{
			Serve: service.CommandFor(service.CommandSpec[LocationConfig]{
				Name: "serve", Summary: "serve Location lookup traffic",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					LocationConfig,
				) (service.Plan, error) {
					return input.API, nil
				},
			}),
			Worker: service.CommandFor(service.CommandSpec[LocationConfig]{
				Name: "worker", Summary: "run Location catalogue work",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					LocationConfig,
				) (service.Plan, error) {
					return input.Worker, nil
				},
			}),
			Schedule: service.CommandFor(service.CommandSpec[LocationConfig]{
				Name: "schedule", Summary: "run Location schedules",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					LocationConfig,
				) (service.Plan, error) {
					return input.Schedule, nil
				},
			}),
			Migrate: input.Migrate,
			Custom: []service.Command{
				service.CommandFor(service.CommandSpec[LocationConfig]{
					Name:    "online-migrate",
					Summary: "reconcile Location data online",
					Kind:    service.CommandKindOneShot, Load: input.Load,
					Build: func(
						context.Context,
						service.BuildContext,
						LocationConfig,
					) (service.Plan, error) {
						return service.Plan{
							Tasks: []service.Task{input.OnlineMigrate},
						}, nil
					},
				}),
				service.CommandFor(service.CommandSpec[LocationConfig]{
					Name:    "activate",
					Summary: "activate a reconciled Location catalogue",
					Kind:    service.CommandKindOneShot, Load: input.Load,
					Build: func(
						context.Context,
						service.BuildContext,
						LocationConfig,
					) (service.Plan, error) {
						return service.Plan{
							Tasks: []service.Task{input.Activate},
						}, nil
					},
				}),
			},
		},
	}
}
