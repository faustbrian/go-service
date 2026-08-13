// Package referencehttp provides a maintained non-production service that
// proves Golib's public HTTP composition contracts together.
package referencehttp

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/faustbrian/golib/pkg/audit"
	"github.com/faustbrian/golib/pkg/audit/memory"
	"github.com/faustbrian/golib/pkg/authentication"
	authenticationhttp "github.com/faustbrian/golib/pkg/authentication/authhttp"
	"github.com/faustbrian/golib/pkg/authentication/bearer"
	"github.com/faustbrian/golib/pkg/authorization"
	"github.com/faustbrian/golib/pkg/authorization/authhttp"
	"github.com/faustbrian/golib/pkg/authorization/authn"
	"github.com/faustbrian/golib/pkg/authorization/rbac"
	"github.com/faustbrian/golib/pkg/capability"
	"github.com/faustbrian/golib/pkg/capability/caphttp"
	"github.com/faustbrian/golib/pkg/config"
	"github.com/faustbrian/golib/pkg/config/configservice"
	"github.com/faustbrian/golib/pkg/config/programmatic"
	"github.com/faustbrian/golib/pkg/correlation"
	middleware "github.com/faustbrian/golib/pkg/http-middleware"
	httpsignature "github.com/faustbrian/golib/pkg/http-signature"
	"github.com/faustbrian/golib/pkg/jsonrpc"
	"github.com/faustbrian/golib/pkg/router"
	"github.com/faustbrian/golib/pkg/service"
	telemetryhttp "github.com/faustbrian/golib/pkg/telemetry/instrumentation/nethttp"
	"github.com/faustbrian/golib/pkg/telemetry/testtelemetry"
	"github.com/faustbrian/golib/pkg/tenancy"
	tenanthttp "github.com/faustbrian/golib/pkg/tenancy/http"
	"github.com/faustbrian/golib/pkg/validation"
	"github.com/faustbrian/golib/pkg/validation/rules"
)

var (
	// ErrDependencyUnavailable is returned by the reference readiness check.
	ErrDependencyUnavailable = errors.New("reference HTTP dependency unavailable")
	// ErrInvalidConfig identifies incomplete reference-service construction.
	ErrInvalidConfig = errors.New("reference HTTP config is invalid")
)

// Config contains the explicit process and trust boundaries for one reference
// service instance. Listener ownership transfers to service.Execute.
type Config struct {
	// ServiceName identifies the reference service in telemetry and health output.
	ServiceName string
	// Version reports the deployed reference-service version.
	Version string
	// Environment identifies the deployment environment.
	Environment string
	// BearerToken authenticates requests to the business listener.
	BearerToken string
	// PrincipalID is the authenticated identity represented by BearerToken.
	PrincipalID string
	// TenantID scopes the authenticated reference requests.
	TenantID string
	// BusinessListener accepts application traffic and transfers ownership to Execute.
	BusinessListener net.Listener
	// ManagementListener accepts health and telemetry traffic and transfers ownership to Execute.
	ManagementListener net.Listener
	// TrustTenant validates the request's tenant boundary.
	TrustTenant func(*http.Request) bool
	// Readiness verifies dependencies required to serve business traffic.
	Readiness func(context.Context) error
}

type runtimeConfig struct {
	ServiceName string `config:"service_name"`
	Version     string `config:"version"`
	Environment string `config:"environment"`
}

// Reference owns the observable in-memory adapters exposed for assurance.
type Reference struct {
	definition service.Definition
	audit      *memory.Store
	telemetry  *testtelemetry.Harness
	client     *http.Client
	capability capability.Signer
	capProfile capability.URLProfile
	capSerial  atomic.Uint64
}

// Definition returns the complete service process definition.
func (reference *Reference) Definition() service.Definition { return reference.definition }

// AuditStore returns the in-memory audit adapter used by this instance.
func (reference *Reference) AuditStore() *memory.Store { return reference.audit }

// Telemetry returns the deterministic telemetry harness used by this instance.
func (reference *Reference) Telemetry() *testtelemetry.Harness { return reference.telemetry }

// Client returns a shallow copy of the HTTP client that signs request content
// and RFC 9421 message metadata before transport.
func (reference *Reference) Client() *http.Client {
	client := *reference.client
	return &client
}

// PrepareRequest binds a short-lived capability to the request method and
// relative target before the HTTP-signature client authenticates the message.
func (reference *Reference) PrepareRequest(request *http.Request) error {
	if reference == nil || request == nil || request.URL == nil {
		return ErrInvalidConfig
	}
	now := time.Now().UTC()
	signed, err := capability.SignURL(request.Context(), capability.Payload{
		Version: 1, Issuer: "reference-http", Audiences: []string{reference.definition.Identity.Name}, Bearer: true,
		IssuedAt: now, NotBefore: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
		ID: fmt.Sprintf("reference-%d-%d", now.UnixNano(), reference.capSerial.Add(1)),
	}, capability.URLRequest{
		Method: request.Method, RawURL: request.URL.RequestURI(),
	}, reference.capProfile, reference.capability, capability.DefaultLimits())
	if err != nil {
		return err
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		return err
	}
	request.URL.RawQuery = parsed.RawQuery
	return nil
}

// New constructs a reference service exclusively through public Golib APIs.
func New(input Config) (*Reference, error) {
	if err := validateConfig(input); err != nil {
		return nil, err
	}

	loader, err := newConfigLoader(input)
	if err != nil {
		return nil, err
	}
	auditStore, recorder, builder, err := newAudit()
	if err != nil {
		return nil, err
	}
	telemetry := testtelemetry.New()
	security, err := newRequestSecurity(input)
	if err != nil {
		_ = telemetry.Shutdown(context.Background())
		return nil, err
	}
	handler, err := newHandler(input, recorder, builder, telemetry, security)
	if err != nil {
		_ = telemetry.Shutdown(context.Background())
		return nil, err
	}
	correlationFactory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		_ = telemetry.Shutdown(context.Background())
		return nil, err
	}

	definition := service.Definition{
		Identity: service.Identity{
			Name: input.ServiceName, Version: input.Version, Environment: input.Environment,
		},
		Correlation: correlationFactory,
		Management: service.Management{
			Listener: input.ManagementListener,
			Details:  true,
		},
	}
	definition.Commands.Serve = service.CommandFor(service.CommandSpec[runtimeConfig]{
		Name: "serve", Summary: "run the reference HTTP service", Kind: service.CommandKindLongRunning,
		Load: loader,
		Build: func(_ context.Context, _ service.BuildContext, loaded runtimeConfig) (service.Plan, error) {
			if loaded.ServiceName != input.ServiceName || loaded.Version != input.Version || loaded.Environment != input.Environment {
				return service.Plan{}, fmt.Errorf("%w: loaded identity differs", ErrInvalidConfig)
			}
			return service.Plan{
				HTTP:      &service.HTTP{Listener: input.BusinessListener, Handler: handler},
				Readiness: []service.ReadinessCheck{{Name: "reference-dependency", Run: input.Readiness}},
			}, nil
		},
	})

	return &Reference{
		definition: definition, audit: auditStore, telemetry: telemetry,
		client: security.client, capability: security.capabilitySigner, capProfile: security.capabilityProfile,
	}, nil
}

func validateConfig(input Config) error {
	if input.ServiceName == "" || input.Version == "" || input.Environment == "" ||
		input.BearerToken == "" || input.PrincipalID == "" || input.TenantID == "" ||
		input.BusinessListener == nil || input.ManagementListener == nil ||
		input.TrustTenant == nil || input.Readiness == nil {
		return ErrInvalidConfig
	}
	if _, err := tenancy.ParseTenantID(input.TenantID); err != nil {
		return fmt.Errorf("%w: tenant", ErrInvalidConfig)
	}
	return nil
}

func newConfigLoader(input Config) (configservice.Loader[runtimeConfig], error) {
	defaults, err := programmatic.Defaults("reference-defaults", map[string]any{
		"service_name": input.ServiceName,
		"version":      input.Version,
		"environment":  input.Environment,
	})
	if err != nil {
		return nil, err
	}
	return configservice.New(configservice.Options[runtimeConfig]{
		Sources: config.DefaultSources{Defaults: []config.Source{defaults}},
	})
}

func newAudit() (*memory.Store, *audit.Recorder, *audit.Builder, error) {
	store, err := memory.New(memory.Config{MaxRecords: 100, MaxBytes: 1 << 20, MaxBatchRecords: 100})
	if err != nil {
		return nil, nil, nil, err
	}
	redactor, err := audit.NewRedactor(audit.RedactionRules{})
	if err != nil {
		return nil, nil, nil, err
	}
	recorder, err := audit.NewRecorder(audit.RecorderConfig{
		Sink: store, Redactor: redactor, Mode: audit.DeliveryFailClosed,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	builder, err := audit.NewBuilder(audit.BuilderConfig{})
	if err != nil {
		return nil, nil, nil, err
	}
	return store, recorder, builder, nil
}

func newHandler(
	input Config,
	recorder *audit.Recorder,
	builder *audit.Builder,
	telemetry *testtelemetry.Harness,
	security requestSecurity,
) (http.Handler, error) {
	tenantAdapter, err := tenanthttp.New(tenanthttp.Options{Trust: input.TrustTenant})
	if err != nil {
		return nil, err
	}
	extractor, err := authenticationhttp.NewExtractor(authenticationhttp.BearerAuthorization())
	if err != nil {
		return nil, err
	}
	authenticator, err := bearer.NewStatic([]bearer.Entry{{
		Token: input.BearerToken,
		Principal: authentication.PrincipalSpec{
			Subject: input.PrincipalID, Method: "bearer", TenantHints: []string{input.TenantID},
		},
	}})
	if err != nil {
		return nil, err
	}
	challenge, err := authentication.NewChallenge("Bearer", nil)
	if err != nil {
		return nil, err
	}
	authenticationMiddleware, err := authenticationhttp.NewMiddleware(
		extractor, authenticator, authenticationhttp.WithChallenges(challenge),
	)
	if err != nil {
		return nil, err
	}

	authorizer, err := newAuthorizer(input)
	if err != nil {
		return nil, err
	}
	registry := jsonrpc.NewRegistry()
	if err := registry.Register("app.echo", echoHandler(input, recorder, builder)); err != nil {
		return nil, err
	}
	rpc := jsonrpc.NewHTTPHandler(jsonrpc.NewDispatcher(registry))
	authorized, err := authhttp.NewHandler(authorizer, mapAuthorizationRequest, rpc)
	if err != nil {
		return nil, err
	}
	chain, err := middleware.New(
		security.digestVerification,
		security.signatureVerification,
		security.capabilityVerification,
		capabilityAuthorization(input.ServiceName),
		tenantAdapter.Wrap,
		authenticationMiddleware,
	)
	if err != nil {
		return nil, err
	}
	composed, err := chain.Handler(authorized)
	if err != nil {
		return nil, err
	}
	instrumented, err := telemetryhttp.NewHandler(composed, telemetryhttp.ServerConfig{
		Operation: "reference.rpc", Route: "/rpc",
		TracerProvider: telemetry.TracerProvider(), MeterProvider: telemetry.MeterProvider(),
	})
	if err != nil {
		return nil, err
	}
	routes := router.New()
	if err := routes.Register(router.Route{
		Name: "reference.rpc", Methods: []string{http.MethodPost}, Path: "/rpc", Handler: instrumented,
		Operation: "app.echo", Source: "reference-http",
	}); err != nil {
		return nil, err
	}
	return routes.Compile()
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type signatureKeyProvider struct{ key httpsignature.SigningKey }

func (provider signatureKeyProvider) SigningKey(context.Context) (httpsignature.SigningKey, error) {
	key := provider.key
	now := time.Now()
	key.NotBefore = now.Add(-time.Hour)
	key.NotAfter = now.Add(time.Hour)
	return key, nil
}

type signatureKeyResolver struct {
	id  string
	key httpsignature.ResolvedKey
}

func (resolver signatureKeyResolver) Resolve(_ context.Context, keyID string) (httpsignature.ResolvedKey, error) {
	if keyID != resolver.id {
		return httpsignature.ResolvedKey{}, httpsignature.ErrKeyNotFound
	}
	key := resolver.key
	now := time.Now()
	key.NotBefore = now.Add(-time.Hour)
	key.NotAfter = now.Add(time.Hour)
	key.FreshUntil = now.Add(time.Minute)
	return key, nil
}

type requestSecurity struct {
	client                 *http.Client
	capabilitySigner       capability.Signer
	capabilityProfile      capability.URLProfile
	capabilityVerification middleware.Middleware
	digestVerification     middleware.Middleware
	signatureVerification  middleware.Middleware
}

func newRequestSecurity(input Config) (requestSecurity, error) {
	capabilityMaterial := make([]byte, 32)
	signatureMaterial := make([]byte, 32)
	if _, err := cryptorand.Read(capabilityMaterial); err != nil {
		return requestSecurity{}, err
	}
	if _, err := cryptorand.Read(signatureMaterial); err != nil {
		return requestSecurity{}, err
	}

	capabilitySigner, err := capability.NewHMACSHA256Signer("reference-capability", capabilityMaterial)
	if err != nil {
		return requestSecurity{}, err
	}
	capabilityVerifier, err := capability.NewHMACSHA256Verifier(capabilityMaterial)
	if err != nil {
		return requestSecurity{}, err
	}
	capabilityKeys, err := capability.NewKeySet([]capability.Key{{
		ID: "reference-capability", Verifier: capabilityVerifier,
	}})
	if err != nil {
		return requestSecurity{}, err
	}
	capabilityProfile := capability.URLProfile{
		Name: "reference-rpc-v1", SignatureParameter: "cap", AllowRelative: true,
	}
	capabilityHTTP, err := caphttp.NewVerifier(caphttp.VerifierOptions{
		Profile: capabilityProfile, Resolver: capabilityKeys, Clock: systemClock{},
		Skew: time.Second, Limits: capability.DefaultLimits(),
	})
	if err != nil {
		return requestSecurity{}, err
	}

	signatureKey, err := httpsignature.NewHMACKey(signatureMaterial)
	if err != nil {
		return requestSecurity{}, err
	}
	components := []httpsignature.ComponentIdentifier{
		{Name: "@method"}, {Name: "@authority"}, {Name: "content-digest"},
	}
	signingProfile, err := httpsignature.NewSigningProfile(httpsignature.SigningProfileConfig{
		AllowedAlgorithms: []httpsignature.Algorithm{httpsignature.HMACSHA256}, CoveredComponents: components,
		Expires: httpsignature.ParameterRequired, AlgorithmParameter: httpsignature.ParameterRequired,
		Nonce: httpsignature.ParameterForbidden, Tag: httpsignature.ParameterForbidden,
		Lifetime: time.Minute, ResolveTimeout: time.Second, Now: time.Now,
		Provider: signatureKeyProvider{key: httpsignature.SigningKey{
			KeyID: "reference-signature", Algorithm: httpsignature.HMACSHA256, Key: signatureKey,
		}},
	})
	if err != nil {
		return requestSecurity{}, err
	}
	verificationProfile, err := httpsignature.NewVerificationProfile(httpsignature.VerificationProfileConfig{
		AllowedAlgorithms: []httpsignature.Algorithm{httpsignature.HMACSHA256}, RequiredComponents: components,
		Created: httpsignature.ParameterRequired, Expires: httpsignature.ParameterRequired,
		AlgorithmParameter: httpsignature.ParameterRequired, Nonce: httpsignature.ParameterForbidden,
		Tag: httpsignature.ParameterForbidden, MaxAge: time.Minute, ClockSkew: time.Second,
		ResolveTimeout: time.Second, Now: time.Now,
		Resolver: signatureKeyResolver{id: "reference-signature", key: httpsignature.ResolvedKey{
			Algorithm: httpsignature.HMACSHA256, Key: signatureKey,
		}},
	})
	if err != nil {
		return requestSecurity{}, err
	}
	mapSignatureError := func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "invalid signed request", http.StatusUnauthorized)
	}
	verifySignature, err := httpsignature.NewRequestVerificationMiddleware(
		httpsignature.RequestVerificationMiddlewareConfig{
			Verifier: httpsignature.NewVerifier(verificationProfile),
			SelectLabel: func(*http.Request, httpsignature.SignatureInputs, httpsignature.Signatures) (string, error) {
				return "reference", nil
			},
			MapError: mapSignatureError,
		},
	)
	if err != nil {
		return requestSecurity{}, err
	}
	verifyDigest, err := httpsignature.NewBufferedContentDigestVerificationMiddleware(
		httpsignature.BufferedContentDigestVerificationMiddlewareConfig{
			RequiredAlgorithms: []httpsignature.DigestAlgorithm{httpsignature.SHA256},
			MaxBytes:           1 << 20, MapError: mapSignatureError,
		},
	)
	if err != nil {
		return requestSecurity{}, err
	}
	signingTransport, err := httpsignature.NewSigningRoundTripper(httpsignature.SigningRoundTripperConfig{
		Transport: http.DefaultTransport, Signer: httpsignature.NewSigner(signingProfile), Label: "reference",
		Existing: httpsignature.ExistingSignaturesReject,
		Options: func(context.Context, *http.Request) (httpsignature.SigningOptions, error) {
			return httpsignature.SigningOptions{}, nil
		},
	})
	if err != nil {
		return requestSecurity{}, err
	}
	digestTransport, err := httpsignature.NewBufferedContentDigestRoundTripper(
		httpsignature.BufferedContentDigestRoundTripperConfig{
			Transport: signingTransport, Algorithms: []httpsignature.DigestAlgorithm{httpsignature.SHA256}, MaxBytes: 1 << 20,
		},
	)
	if err != nil {
		return requestSecurity{}, err
	}
	return requestSecurity{
		client:           &http.Client{Transport: digestTransport, Timeout: 5 * time.Second},
		capabilitySigner: capabilitySigner, capabilityProfile: capabilityProfile,
		capabilityVerification: capabilityHTTP.Middleware,
		digestVerification:     middleware.Middleware(verifyDigest),
		signatureVerification:  middleware.Middleware(verifySignature),
	}, nil
}

func capabilityAuthorization(audience string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			grant, ok := caphttp.GrantFromContext(request.Context())
			if !ok || grant.Authorize(capability.Use{
				Audience: audience, Resource: request.URL.Path, Operation: request.Method,
			}) != nil {
				http.Error(writer, "capability denied", http.StatusForbidden)
				return
			}
			next.ServeHTTP(writer, request)
		})
	}
}

func newAuthorizer(input Config) (*authorization.Engine, error) {
	tenant := authorization.TenantID(input.TenantID)
	role := rbac.Role{ID: "reference-caller", Tenant: tenant}
	permission := rbac.Permission{
		ID: "reference-echo", RoleID: role.ID, Tenant: tenant,
		Action: "reference.echo", ResourceType: "reference-service", Effect: authorization.Allow,
	}
	assignment := rbac.Assignment{
		ID: "reference-assignment", RoleID: role.ID, Tenant: tenant,
		Subject: authorization.Subject{Kind: authorization.SubjectServiceAccount, ID: authorization.SubjectID(input.PrincipalID)},
	}
	evaluator, err := rbac.New([]rbac.Role{role}, []rbac.Permission{permission}, []rbac.Assignment{assignment})
	if err != nil {
		return nil, err
	}
	snapshot, err := authorization.NewSnapshot(1, authorization.DenyOverrides, authorization.PolicyDefinition{
		ID: "reference-rbac", Evaluator: evaluator,
	})
	if err != nil {
		return nil, err
	}
	return authorization.NewEngine(snapshot)
}

func mapAuthorizationRequest(request *http.Request) (authorization.Request, error) {
	principal, ok := authentication.PrincipalFromContext(request.Context())
	if !ok {
		return authorization.Request{}, authentication.ErrInvalidPrincipal
	}
	subject, err := authn.Subject(principal, authn.Config{Kind: authorization.SubjectServiceAccount})
	if err != nil {
		return authorization.Request{}, err
	}
	tenant, err := tenancy.RequireTenant(request.Context())
	if err != nil {
		return authorization.Request{}, err
	}
	return authorization.Request{
		Subject: subject, Action: "reference.echo",
		Resource: authorization.Resource{Type: "reference-service"},
		Tenant:   authorization.TenantID(tenant.Value()),
	}, nil
}

type echoParams struct {
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
}

type echoResult struct {
	Message       string `json:"message"`
	Principal     string `json:"principal"`
	Tenant        string `json:"tenant"`
	CorrelationID string `json:"correlation_id"`
	RequestID     string `json:"request_id"`
}

func echoHandler(input Config, recorder *audit.Recorder, builder *audit.Builder) jsonrpc.Handler {
	validationContext, _ := validation.NewContext(
		validation.DefaultLimits(), validation.WithOperation("app.echo"),
	)
	messageValidator := rules.RuneLength(1, 128)
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		params, rpcError := jsonrpc.DecodeParams[echoParams](raw)
		if rpcError != nil {
			return nil, rpcError
		}
		if report := messageValidator.Validate(validationContext, params.Data.Message); report.HasErrors() {
			return nil, jsonrpc.InvalidParams().WithCause(report.Err())
		}
		principal, ok := authentication.PrincipalFromContext(ctx)
		if !ok {
			return nil, jsonrpc.InternalError()
		}
		tenant, err := tenancy.RequireTenant(ctx)
		if err != nil {
			return nil, jsonrpc.InternalError().WithCause(err)
		}
		identifiers, ok := correlation.FromContext(ctx)
		if !ok {
			return nil, jsonrpc.InternalError()
		}
		record, err := builder.Build(audit.RecordInput{
			OccurredAt: time.Now().UTC(), Action: "reference.echo", Outcome: audit.OutcomeSucceeded,
			Actor:   audit.ActorInput{Kind: audit.ActorService, ID: principal.Subject()},
			Subject: audit.SubjectInput{Type: "reference-message", ID: "echo"},
			Context: audit.ContextInput{
				TenantID: tenant.Value(), CorrelationID: identifiers.CorrelationID.String(), RequestID: identifiers.RequestID.String(),
				SourceService: input.ServiceName, SourceVersion: input.Version, Environment: input.Environment,
			},
			Changes: audit.ChangeSetInput{NoChange: true},
		})
		if err != nil {
			return nil, jsonrpc.InternalError().WithCause(err)
		}
		if _, err := recorder.Submit(ctx, record); err != nil {
			return nil, jsonrpc.InternalError().WithCause(err)
		}
		return echoResult{
			Message: params.Data.Message, Principal: principal.Subject(), Tenant: tenant.Value(),
			CorrelationID: identifiers.CorrelationID.String(), RequestID: identifiers.RequestID.String(),
		}, nil
	}
}
