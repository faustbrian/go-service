package adoption

import (
	"context"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
)

// TrackConfig represents Track-owned typed configuration at the platform seam.
type TrackConfig struct{}

// Track keeps Track's explicit infrastructure and business work visible while
// delegating generic command, probe, lifecycle, and shutdown behavior.
type Track struct {
	// Identity is the Track process identity.
	Identity service.Identity
	// Load resolves Track-owned typed configuration.
	Load func(context.Context, service.Invocation) (TrackConfig, error)
	// Correlation creates Track's process and request identities.
	Correlation *correlation.Factory
	// Management configures the separate operational listener.
	Management service.Management
	// Telemetry owns Track's telemetry runtime lifecycle.
	Telemetry service.Component
	// Postgres owns Track's required pool lifecycle.
	Postgres service.Component
	// PostgresReadiness checks only the selected Track pool.
	PostgresReadiness service.ReadinessCheck
	// Kafka owns Track's producer or consumer lifecycle.
	Kafka service.Component
	// KafkaReadiness checks only the selected Kafka dependency.
	KafkaReadiness service.ReadinessCheck
	// HTTP contains Track's business handler and listener.
	HTTP service.HTTP
	// Worker runs application-owned carrier work.
	Worker service.Task
}

// TrackDefinition composes Track's serve and worker roles.
func TrackDefinition(input Track) service.Definition {
	components := []service.Component{
		input.Telemetry,
		input.Postgres,
		input.Kafka,
	}
	readiness := make([]service.ReadinessCheck, 0, 2)
	if input.PostgresReadiness.Run != nil {
		readiness = append(readiness, input.PostgresReadiness)
	}
	if input.KafkaReadiness.Run != nil {
		readiness = append(readiness, input.KafkaReadiness)
	}

	return service.Definition{
		Identity:    input.Identity,
		Correlation: input.Correlation,
		Management:  input.Management,
		Commands: service.Commands{
			Serve: service.CommandFor(service.CommandSpec[TrackConfig]{
				Name: "serve", Summary: "serve Track HTTP and RPC traffic",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					TrackConfig,
				) (service.Plan, error) {
					return service.Plan{
						Components: components,
						HTTP:       &input.HTTP,
						Readiness:  readiness,
					}, nil
				},
			}),
			Worker: service.CommandFor(service.CommandSpec[TrackConfig]{
				Name: "worker", Summary: "run Track carrier work",
				Kind: service.CommandKindLongRunning, Load: input.Load,
				Build: func(
					context.Context,
					service.BuildContext,
					TrackConfig,
				) (service.Plan, error) {
					return service.Plan{
						Components: components,
						Tasks:      []service.Task{input.Worker},
						Readiness:  readiness,
					}, nil
				},
			}),
		},
	}
}
