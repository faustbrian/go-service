package adoption_test

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/integration/adoption"
)

func TestTrackWorkerUsesExplicitInfrastructureAndLifecycle(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	started := make(chan struct{})
	definition := adoption.TrackDefinition(adoption.Track{
		Identity:   service.Identity{Name: "track"},
		Load:       loadValue(adoption.TrackConfig{}),
		Management: management(t),
		Telemetry:  events.component("telemetry"),
		Postgres:   events.component("postgres"),
		Kafka:      events.component("kafka"),
		Worker: service.Task{
			Name: "carrier-worker",
			Run: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()

				return ctx.Err()
			},
		},
	})

	code := executeLongRunning(t, definition, "worker", started)
	if code != 0 {
		t.Fatalf("worker exit code = %d", code)
	}
	if got := events.snapshot(); strings.Join(got, ",") !=
		"start:telemetry,start:postgres,start:kafka,"+
			"stop:kafka,stop:postgres,stop:telemetry" {
		t.Fatalf("worker lifecycle = %v", got)
	}
}

func TestPostalMigrateDoesNotInitializeLongRunningFacilities(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	migrated := false
	definition := adoption.PostalDefinition(adoption.Postal{
		Identity: service.Identity{Name: "postal"},
		Load:     loadValue(adoption.PostalConfig{Environment: "local"}),
		Serve:    service.Plan{Components: []service.Component{events.component("serve")}},
		Worker:   service.Plan{Components: []service.Component{events.component("queue")}},
		Schedule: service.Plan{Components: []service.Component{events.component("scheduler")}},
		Migrate: oneShotCommand("migrate", func(context.Context) error {
			migrated = true

			return nil
		}),
	})

	if code := execute(t, definition, "migrate"); code != 0 {
		t.Fatalf("migrate exit code = %d", code)
	}
	if !migrated || len(events.snapshot()) != 0 {
		t.Fatalf("migrated = %t, unrelated lifecycle = %v", migrated, events.snapshot())
	}
}

func TestLocationKeepsDomainCommandsSeparateFromRuntimeFacilities(t *testing.T) {
	t.Parallel()

	events := &eventLog{}
	onlineMigrated := false
	activated := false
	definition := adoption.LocationDefinition(adoption.Location{
		Identity: service.Identity{Name: "location"},
		Load:     loadValue(adoption.LocationConfig{}),
		API:      service.Plan{Components: []service.Component{events.component("api")}},
		Worker:   service.Plan{Components: []service.Component{events.component("worker")}},
		Schedule: service.Plan{Components: []service.Component{events.component("scheduler")}},
		Migrate:  oneShotCommand("migrate", noWork),
		OnlineMigrate: service.Task{Name: "online-migration", Run: func(context.Context) error {
			onlineMigrated = true

			return nil
		}},
		Activate: service.Task{Name: "catalogue-activation", Run: func(context.Context) error {
			activated = true

			return nil
		}},
	})

	if code := execute(t, definition, "online-migrate"); code != 0 {
		t.Fatalf("online-migrate exit code = %d", code)
	}
	if !onlineMigrated || activated || len(events.snapshot()) != 0 {
		t.Fatalf(
			"online = %t, activated = %t, runtime lifecycle = %v",
			onlineMigrated,
			activated,
			events.snapshot(),
		)
	}
}

func executeLongRunning(
	t *testing.T,
	definition service.Definition,
	command string,
	started <-chan struct{},
) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, definition, invocation(command))
	}()
	<-started
	cancel()

	return <-result
}

func execute(t *testing.T, definition service.Definition, command string) int {
	t.Helper()

	return service.Execute(context.Background(), definition, invocation(command))
}

func invocation(command string) service.Invocation {
	return service.Invocation{
		Args:   []string{command},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
}

func management(t *testing.T) service.Management {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management listener: %v", err)
	}

	return service.Management{Listener: listener}
}

func loadValue[T any](value T) func(
	context.Context,
	service.Invocation,
) (T, error) {
	return func(context.Context, service.Invocation) (T, error) {
		return value, nil
	}
}

func noWork(context.Context) error { return nil }

func oneShotCommand(name string, run func(context.Context) error) service.Command {
	return service.CommandFor(service.CommandSpec[struct{}]{
		Name: name, Summary: "run " + name, Kind: service.CommandKindOneShot,
		Load: loadValue(struct{}{}),
		Build: func(
			context.Context,
			service.BuildContext,
			struct{},
		) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{Name: name, Run: run}}}, nil
		},
	})
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *eventLog) component(name string) service.Component {
	return service.Component{
		Name: name,
		Start: func(context.Context) error {
			log.record("start:" + name)

			return nil
		},
		Stop: func(context.Context) error {
			log.record("stop:" + name)

			return nil
		},
	}
}

func (log *eventLog) record(event string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *eventLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()

	return append([]string(nil), log.events...)
}
