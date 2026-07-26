package compatibility_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authentication "github.com/faustbrian/golib/pkg/authentication"
	authenticationhttp "github.com/faustbrian/golib/pkg/authentication/authhttp"
	authorization "github.com/faustbrian/golib/pkg/authorization"
	authorizationhttp "github.com/faustbrian/golib/pkg/authorization/httpauth"
	config "github.com/faustbrian/golib/pkg/config"
	log "github.com/faustbrian/golib/pkg/log"
	queue "github.com/faustbrian/golib/pkg/queue"
	scheduler "github.com/faustbrian/golib/pkg/scheduler"
	schedulermemory "github.com/faustbrian/golib/pkg/scheduler/memory"
	"github.com/faustbrian/golib/pkg/service/integration"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
	"github.com/faustbrian/golib/pkg/service/service"
	telemetry "github.com/faustbrian/golib/pkg/telemetry"
)

type absentExtractor struct{}

func (absentExtractor) Extract(*http.Request) (authentication.Credential, error) {
	return nil, authentication.ErrCredentialsAbsent
}

type unusedAuthenticator struct{}

func (unusedAuthenticator) Authenticate(
	context.Context,
	authentication.Credential,
) (authentication.Result, error) {
	return authentication.AnonymousResult(), nil
}

type allowAuthorizer struct{}

func (allowAuthorizer) Decide(
	context.Context,
	authorization.Request,
) (authorization.Decision, error) {
	return authorization.Decision{Outcome: authorization.Allow}, nil
}

type executorFunc func(context.Context, scheduler.Context) error

func (execute executorFunc) Execute(ctx context.Context, scheduled scheduler.Context) error {
	return execute(ctx, scheduled)
}

type failingConfigSource struct {
	err error
}

func (source failingConfigSource) Info() config.SourceInfo {
	return config.SourceInfo{
		Name:      "secret-provider",
		Priority:  config.PriorityEnvironment,
		Sensitive: true,
	}
}

func (source failingConfigSource) Load(context.Context) (config.Document, error) {
	return config.Document{}, source.err
}

func TestActualHTTPMiddlewareContracts(t *testing.T) {
	authenticate, err := authenticationhttp.NewMiddleware(
		absentExtractor{},
		unusedAuthenticator{},
		authenticationhttp.WithOptionalAnonymous(),
	)
	if err != nil {
		t.Fatalf("authentication middleware error = %v", err)
	}
	authorized, err := authorizationhttp.NewHandler(
		allowAuthorizer{},
		func(request *http.Request) (authorization.Request, error) {
			principal, ok := authentication.PrincipalFromContext(request.Context())
			if !ok || !principal.IsAnonymous() {
				t.Fatal("authorization ran before authentication")
			}

			return authorization.Request{
				Subject: authorization.Subject{
					Kind: authorization.SubjectUser,
					ID:   "anonymous",
				},
				Action:   "read",
				Resource: authorization.Resource{Type: "status"},
			}, nil
		},
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
	)
	if err != nil {
		t.Fatalf("authorization handler error = %v", err)
	}
	handler, err := serverhttp.Chain(authorized, authenticate)
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestActualLifecycleIntegrationContracts(t *testing.T) {
	plan, err := config.NewPlan()
	if err != nil {
		t.Fatalf("config.NewPlan() error = %v", err)
	}
	configuration, err := integration.New("configuration", integration.Hooks{
		Start: func(ctx context.Context) error {
			_, loadErr := config.Load[struct{}](ctx, plan)

			return loadErr
		},
	})
	if err != nil {
		t.Fatalf("configuration integration error = %v", err)
	}

	logger := log.Text(io.Discard, nil)
	telemetryConfig := telemetry.DefaultConfig("compatibility", "v0.0.0")
	telemetryConfig.Traces.Enabled = false
	telemetryConfig.Metrics.Enabled = false
	telemetryConfig.RegisterGlobal = false
	var telemetryRuntime *telemetry.Runtime
	telemetryComponent, err := integration.New(
		"telemetry",
		integration.Hooks{
			Start: func(ctx context.Context) error {
				var initializeErr error
				telemetryRuntime, initializeErr = telemetry.Init(ctx, telemetryConfig)

				return initializeErr
			},
			Stop: func(ctx context.Context) error {
				return telemetryRuntime.Shutdown(ctx)
			},
		},
		integration.WithSlog(logger),
	)
	if err != nil {
		t.Fatalf("telemetry integration error = %v", err)
	}

	queueRuntime, err := queue.NewQueue(
		queue.WithWorker(queue.NewRing()),
		queue.WithWorkerCount(1),
		queue.WithLogger(queue.NewEmptyLogger()),
	)
	if err != nil {
		t.Fatalf("queue.NewQueue() error = %v", err)
	}
	queueComponent, err := integration.New("queue", integration.Hooks{
		Start: func(context.Context) error {
			queueRuntime.Start()

			return nil
		},
		Stop: func(context.Context) error {
			queueRuntime.Release()

			return nil
		},
	})
	if err != nil {
		t.Fatalf("queue integration error = %v", err)
	}

	registry, err := scheduler.Compile()
	if err != nil {
		t.Fatalf("scheduler.Compile() error = %v", err)
	}
	schedulerRuntime, err := scheduler.NewRunner(
		registry,
		schedulermemory.New(),
		executorFunc(func(context.Context, scheduler.Context) error { return nil }),
		scheduler.WithOwner("compatibility"),
	)
	if err != nil {
		t.Fatalf("scheduler.NewRunner() error = %v", err)
	}
	schedulerComponent, err := integration.New("scheduler", integration.Hooks{
		Stop: schedulerRuntime.Drain,
	})
	if err != nil {
		t.Fatalf("scheduler integration error = %v", err)
	}

	runtime, err := service.New(service.Config{Components: []service.Component{
		configuration,
		telemetryComponent,
		queueComponent,
		schedulerComponent,
	}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Go("scheduler", schedulerRuntime.Run); err != nil {
		t.Fatalf("Go() error = %v", err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestActualConfigurationFailureIsRedactedAndPreventsStartup(t *testing.T) {
	const secret = "secret-provider-token"
	secretFailure := errors.New(secret)
	plan, err := config.NewPlan(failingConfigSource{err: secretFailure})
	if err != nil {
		t.Fatalf("config.NewPlan() error = %v", err)
	}
	configuration, err := integration.New("configuration", integration.Hooks{
		Start: func(ctx context.Context) error {
			_, loadErr := config.Load[struct{}](ctx, plan)

			return loadErr
		},
	})
	if err != nil {
		t.Fatalf("configuration integration error = %v", err)
	}
	laterStarted := false
	runtime, err := service.New(service.Config{Components: []service.Component{
		configuration,
		{
			Name: "later",
			Start: func(context.Context) error {
				laterStarted = true

				return nil
			},
		},
	}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}

	err = runtime.Start(context.Background())
	if !errors.Is(err, secretFailure) {
		t.Fatalf("Start() error = %v, want source cause identity", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Start() error leaked source cause: %q", err)
	}
	if laterStarted {
		t.Fatal("component after failed configuration load started")
	}
}

func TestActualTelemetryDuplicateRegistrationRollsBackStartup(t *testing.T) {
	telemetryConfig := telemetry.DefaultConfig("compatibility-duplicate", "v0.0.0")
	telemetryConfig.Traces.Enabled = false
	telemetryConfig.Metrics.Enabled = false
	telemetryConfig.RegisterGlobal = true

	var firstRuntime *telemetry.Runtime
	first, err := integration.New("telemetry-primary", integration.Hooks{
		Start: func(ctx context.Context) error {
			var initializeErr error
			firstRuntime, initializeErr = telemetry.Init(ctx, telemetryConfig)

			return initializeErr
		},
		Stop: func(ctx context.Context) error {
			return firstRuntime.Shutdown(ctx)
		},
	})
	if err != nil {
		t.Fatalf("primary integration error = %v", err)
	}
	duplicate, err := integration.New("telemetry-duplicate", integration.Hooks{
		Start: func(ctx context.Context) error {
			_, initializeErr := telemetry.Init(ctx, telemetryConfig)

			return initializeErr
		},
	})
	if err != nil {
		t.Fatalf("duplicate integration error = %v", err)
	}
	laterStarted := false
	runtime, err := service.New(service.Config{Components: []service.Component{
		first,
		duplicate,
		{
			Name: "later",
			Start: func(context.Context) error {
				laterStarted = true

				return nil
			},
		},
	}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, telemetry.ErrAlreadyInitialized) {
		t.Fatalf("Start() error = %v, want ErrAlreadyInitialized", err)
	}
	if laterStarted {
		t.Fatal("component after duplicate telemetry registration started")
	}

	reinitialized, err := telemetry.Init(context.Background(), telemetryConfig)
	if err != nil {
		t.Fatalf("telemetry was not released by rollback: %v", err)
	}
	if err := reinitialized.Shutdown(context.Background()); err != nil {
		t.Fatalf("reinitialized Shutdown() error = %v", err)
	}
}
