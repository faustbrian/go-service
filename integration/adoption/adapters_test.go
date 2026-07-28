package adoption_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/faustbrian/golib/pkg/cache/cacheservice"
	"github.com/faustbrian/golib/pkg/config/configservice"
	"github.com/faustbrian/golib/pkg/config/dotenv"
	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/kafka"
	"github.com/faustbrian/golib/pkg/kafka/kafkaservice"
	"github.com/faustbrian/golib/pkg/migrations"
	"github.com/faustbrian/golib/pkg/migrations/migrationsservice"
	"github.com/faustbrian/golib/pkg/postgres/postgresservice"
	"github.com/faustbrian/golib/pkg/queue"
	"github.com/faustbrian/golib/pkg/queue/queueservice"
	"github.com/faustbrian/golib/pkg/scheduler"
	"github.com/faustbrian/golib/pkg/scheduler/memory"
	"github.com/faustbrian/golib/pkg/scheduler/schedulerservice"
	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/integration/adoption"
	"github.com/faustbrian/golib/pkg/telemetry"
	"github.com/faustbrian/golib/pkg/telemetry/telemetryservice"
)

func TestOwningModuleAdaptersComposeIntoReferenceDefinitions(t *testing.T) {
	t.Parallel()

	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	postgres, err := postgresservice.New(postgresservice.Options{
		Name: "postgres", Pool: &poolResource{},
	})
	if err != nil {
		t.Fatalf("postgresservice.New() error = %v", err)
	}
	kafkaAdapter, err := kafkaservice.NewProducer(
		kafkaservice.ProducerOptions[*kafkaResource]{
			Name: "kafka", Resource: &kafkaResource{}, Correlation: factory,
			Readiness: func(context.Context, *kafkaResource) error { return nil },
			Publish: func(
				context.Context,
				*kafkaResource,
				kafka.ProducerRecord,
			) (kafka.DeliveryResult, error) {
				return kafka.DeliveryResult{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("kafkaservice.NewProducer() error = %v", err)
	}
	telemetryAdapter, err := telemetryservice.New(telemetryservice.Options{
		Name: "telemetry", Config: telemetry.Config{},
		Failure: telemetryservice.FailureBestEffort,
	})
	if err != nil {
		t.Fatalf("telemetryservice.New() error = %v", err)
	}
	kafkaReadiness, ok := kafkaAdapter.Readiness()
	if !ok {
		t.Fatal("Kafka readiness missing")
	}
	track := adoption.TrackDefinition(adoption.Track{
		Identity:          service.Identity{Name: "track"},
		Load:              loadValue(adoption.TrackConfig{}),
		Telemetry:         telemetryAdapter.Component(),
		Postgres:          postgres.Component(),
		PostgresReadiness: postgres.Readiness(),
		Kafka:             kafkaAdapter.Component(),
		KafkaReadiness:    kafkaReadiness,
		HTTP:              service.HTTP{Handler: http.NewServeMux()},
		Worker:            service.Task{Name: "carrier-worker", Run: noWork},
	})

	postalLoader, err := configservice.New(
		configservice.Options[adoption.PostalConfig]{
			Local: true,
			Dotenv: &configservice.Dotenv{
				FS:      fstest.MapFS{".env": {Data: []byte("POSTAL_ENVIRONMENT=local\n")}},
				Path:    ".env",
				Options: dotenv.Options{Name: "postal-dotenv", Prefix: "POSTAL_"},
			},
		},
	)
	if err != nil {
		t.Fatalf("configservice.New() error = %v", err)
	}
	postalConfig, err := postalLoader(context.Background(), service.Invocation{})
	if err != nil || postalConfig.Environment != "local" {
		t.Fatalf("Postal local configuration = %#v, %v", postalConfig, err)
	}
	queueRuntime, err := queue.NewQueue(
		queue.WithWorker(queue.NewRing()),
		queue.WithWorkerCount(0),
	)
	if err != nil {
		t.Fatalf("queue.NewQueue() error = %v", err)
	}
	queueAdapter, err := queueservice.NewWorker(queueservice.WorkerOptions{
		Name: "postal-worker", Queue: queueRuntime,
	})
	if err != nil {
		t.Fatalf("queueservice.NewWorker() error = %v", err)
	}
	scheduleAdapter := newScheduleAdapter(t, factory)
	postalMigrations, err := migrationsservice.New(
		migrationsservice.Options[adoption.PostalConfig]{
			Summary: "run Postal schema migrations",
			Load:    migrationsservice.Load[adoption.PostalConfig](postalLoader),
			Prepare: emptyMigration[adoption.PostalConfig],
			Execute: func(context.Context, *migrations.Runner) error { return nil },
		},
	)
	if err != nil {
		t.Fatalf("migrationsservice.New() error = %v", err)
	}
	postal := adoption.PostalDefinition(adoption.Postal{
		Identity: service.Identity{Name: "postal"}, Load: postalLoader,
		Serve: service.Plan{HTTP: &service.HTTP{Handler: http.NewServeMux()}},
		Worker: service.Plan{
			Components: []service.Component{queueAdapter.Component()},
		},
		Schedule: scheduleAdapter.Plan(),
		Migrate:  postalMigrations.Command(),
	})

	cacheAdapter, err := cacheservice.New(cacheservice.Options[*cacheResource]{
		Name: "valkey", Resource: &cacheResource{},
	})
	if err != nil {
		t.Fatalf("cacheservice.New() error = %v", err)
	}
	locationMigrations, err := migrationsservice.New(
		migrationsservice.Options[adoption.LocationConfig]{
			Summary: "run Location schema migrations",
			Load:    loadValue(adoption.LocationConfig{}),
			Prepare: emptyMigration[adoption.LocationConfig],
			Execute: func(context.Context, *migrations.Runner) error { return nil },
		},
	)
	if err != nil {
		t.Fatalf("migrationsservice.New() error = %v", err)
	}
	location := adoption.LocationDefinition(adoption.Location{
		Identity: service.Identity{Name: "location"},
		Load:     loadValue(adoption.LocationConfig{}),
		API: service.Plan{
			Components: []service.Component{
				postgres.Component(),
				cacheAdapter.Component(),
			},
			HTTP: &service.HTTP{Handler: http.NewServeMux()},
		},
		Worker: service.Plan{
			Components: []service.Component{
				postgres.Component(),
				cacheAdapter.Component(),
				queueAdapter.Component(),
			},
		},
		Schedule:      scheduleAdapter.Plan(),
		Migrate:       locationMigrations.Command(),
		OnlineMigrate: service.Task{Name: "online-migrate", Run: noWork},
		Activate:      service.Task{Name: "activate", Run: noWork},
	})

	assertHelp(t, track, "serve", "worker")
	assertHelp(t, postal, "serve", "worker", "schedule", "migrate")
	assertHelp(
		t,
		location,
		"serve",
		"worker",
		"schedule",
		"migrate",
		"online-migrate",
		"activate",
	)
}

func newScheduleAdapter(
	t *testing.T,
	factory *correlation.Factory,
) *schedulerservice.Adapter {
	t.Helper()
	registry, err := scheduler.Compile()
	if err != nil {
		t.Fatalf("scheduler.Compile() error = %v", err)
	}
	adapter, err := schedulerservice.New(schedulerservice.Options{
		Name: "scheduler", Registry: registry, Leases: memory.New(),
		Executor: schedulerExecutor{}, Correlation: factory,
		RunnerOptions: []scheduler.RunnerOption{
			scheduler.WithOwner("adoption-fixture"),
		},
	})
	if err != nil {
		t.Fatalf("schedulerservice.New() error = %v", err)
	}

	return adapter
}

func emptyMigration[C any](
	context.Context,
	service.BuildContext,
	C,
) (migrationsservice.Execution, error) {
	return migrationsservice.Execution{}, nil
}

func assertHelp(
	t *testing.T,
	definition service.Definition,
	commands ...string,
) {
	t.Helper()
	var stdout bytes.Buffer
	code := service.Execute(context.Background(), definition, service.Invocation{
		Args: []string{"--help"}, Stdout: &stdout, Stderr: &bytes.Buffer{},
	})
	if code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	for _, command := range commands {
		if !bytes.Contains(stdout.Bytes(), []byte(command)) {
			t.Errorf("help output missing %q", command)
		}
	}
}

type poolResource struct{}

func (*poolResource) Ping(context.Context) error  { return nil }
func (*poolResource) Close(context.Context) error { return nil }

type kafkaResource struct{}
type cacheResource struct{}

type schedulerExecutor struct{}

func (schedulerExecutor) Execute(context.Context, scheduler.Context) error {
	return nil
}
