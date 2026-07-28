package adoption

import (
	"context"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
)

// PostalConfig represents Postal-owned typed configuration at the platform
// seam. The environment remains application data loaded through configservice.
type PostalConfig struct {
	// Environment selects Postal's validated deployment configuration.
	Environment string `config:"environment" env:"ENVIRONMENT"`
}

// Postal keeps each planned role explicit while the platform owns selection,
// probes, signals, lifecycle, and exit behavior.
type Postal struct {
	// Identity is the Postal process identity.
	Identity service.Identity
	// Load resolves Postal configuration, including explicit local dotenv.
	Load func(context.Context, service.Invocation) (PostalConfig, error)
	// Correlation creates Postal's process and request identities.
	Correlation *correlation.Factory
	// Management configures the separate operational listener.
	Management service.Management
	// Serve retains Postal's JSON-RPC handler and required dependencies.
	Serve service.Plan
	// Worker retains Postal's queue and application handler.
	Worker service.Plan
	// Schedule retains Postal's schedules, leases, and executor.
	Schedule service.Plan
	// Migrate is the owning migrations module's standard command.
	Migrate service.Command
}

// PostalDefinition composes Postal's four standard roles.
func PostalDefinition(input Postal) service.Definition {
	return service.Definition{
		Identity:    input.Identity,
		Correlation: input.Correlation,
		Management:  input.Management,
		Commands: service.Commands{
			Serve: service.CommandFor(service.CommandSpec[PostalConfig]{
				Name: "serve", Summary: "serve Postal JSON-RPC search",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					PostalConfig,
				) (service.Plan, error) {
					return input.Serve, nil
				},
			}),
			Worker: service.CommandFor(service.CommandSpec[PostalConfig]{
				Name: "worker", Summary: "run Postal queue work",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					PostalConfig,
				) (service.Plan, error) {
					return input.Worker, nil
				},
			}),
			Schedule: service.CommandFor(service.CommandSpec[PostalConfig]{
				Name: "schedule", Summary: "run Postal schedules",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					PostalConfig,
				) (service.Plan, error) {
					return input.Schedule, nil
				},
			}),
			Migrate: input.Migrate,
		},
	}
}
