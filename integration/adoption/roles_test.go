package adoption_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/integration/adoption"
)

func TestTrackServeComposesBusinessAndManagementHTTP(t *testing.T) {
	t.Parallel()

	business := newReadyListener(t)
	definition := adoption.TrackDefinition(adoption.Track{
		Identity:   service.Identity{Name: "track"},
		Load:       loadValue(adoption.TrackConfig{}),
		Management: management(t),
		Telemetry:  inertComponent("telemetry"),
		Postgres:   inertComponent("postgres"),
		PostgresReadiness: service.ReadinessCheck{
			Name: "postgres", Run: noWork,
		},
		Kafka: inertComponent("kafka"),
		KafkaReadiness: service.ReadinessCheck{
			Name: "kafka", Run: noWork,
		},
		HTTP: service.HTTP{
			Listener: business,
			Handler:  http.HandlerFunc(okHandler),
		},
		Worker: service.Task{Name: "worker", Run: canceledTask(nil)},
	})

	if code := executeLongRunning(t, definition, "serve", business.accepting); code != 0 {
		t.Fatalf("serve exit code = %d", code)
	}
}

func TestPostalLongRunningRolesRemainIndependent(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"serve", "worker", "schedule"} {
		t.Run(role, func(t *testing.T) {
			started := make(chan struct{})
			ran := make(chan string, 1)
			definition := adoption.PostalDefinition(adoption.Postal{
				Identity:   service.Identity{Name: "postal"},
				Load:       loadValue(adoption.PostalConfig{}),
				Management: management(t),
				Serve: service.Plan{
					Tasks: []service.Task{{
						Name: "serve",
						Run:  namedCanceledTask("serve", started, ran),
					}},
				},
				Worker: service.Plan{
					Tasks: []service.Task{{
						Name: "worker",
						Run:  namedCanceledTask("worker", started, ran),
					}},
				},
				Schedule: service.Plan{
					Tasks: []service.Task{{
						Name: "schedule",
						Run:  namedCanceledTask("schedule", started, ran),
					}},
				},
				Migrate: oneShotCommand("migrate", noWork),
			})

			if code := executeLongRunning(t, definition, role, started); code != 0 {
				t.Fatalf("%s exit code = %d", role, code)
			}
			if selected := <-ran; selected != role {
				t.Fatalf("%s executed %s plan", role, selected)
			}
		})
	}
}

func TestLocationExecutesEveryDistinctRole(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"serve", "worker", "schedule"} {
		t.Run(role, func(t *testing.T) {
			started := make(chan struct{})
			ran := make(chan string, 1)
			definition := locationDefinition(t, started, ran)
			if code := executeLongRunning(t, definition, role, started); code != 0 {
				t.Fatalf("%s exit code = %d", role, code)
			}
			if selected := <-ran; selected != role {
				t.Fatalf("%s executed %s plan", role, selected)
			}
		})
	}
	for _, role := range []string{"migrate", "online-migrate", "activate"} {
		t.Run(role, func(t *testing.T) {
			if code := execute(t, locationDefinition(t, nil, nil), role); code != 0 {
				t.Fatalf("%s exit code = %d", role, code)
			}
		})
	}
}

func locationDefinition(
	t *testing.T,
	started chan struct{},
	ran chan string,
) service.Definition {
	t.Helper()
	managementConfig := service.Management{}
	if started != nil {
		managementConfig = management(t)
	}

	return adoption.LocationDefinition(adoption.Location{
		Identity:   service.Identity{Name: "location"},
		Load:       loadValue(adoption.LocationConfig{}),
		Management: managementConfig,
		API: service.Plan{
			Tasks: []service.Task{{
				Name: "api",
				Run:  namedCanceledTask("serve", started, ran),
			}},
		},
		Worker: service.Plan{
			Tasks: []service.Task{{
				Name: "worker",
				Run:  namedCanceledTask("worker", started, ran),
			}},
		},
		Schedule: service.Plan{
			Tasks: []service.Task{{
				Name: "schedule",
				Run:  namedCanceledTask("schedule", started, ran),
			}},
		},
		Migrate:       oneShotCommand("migrate", noWork),
		OnlineMigrate: service.Task{Name: "online-migrate", Run: noWork},
		Activate:      service.Task{Name: "activate", Run: noWork},
	})
}

func canceledTask(started chan struct{}) func(context.Context) error {
	return func(ctx context.Context) error {
		if started != nil {
			close(started)
		}
		<-ctx.Done()

		return ctx.Err()
	}
}

func namedCanceledTask(
	name string,
	started chan struct{},
	ran chan<- string,
) func(context.Context) error {
	return func(ctx context.Context) error {
		ran <- name
		close(started)
		<-ctx.Done()

		return ctx.Err()
	}
}

func inertComponent(name string) service.Component {
	return service.Component{
		Name:  name,
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { return nil },
	}
}

func okHandler(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)
}
