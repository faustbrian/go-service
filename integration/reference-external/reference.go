package referenceexternal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"

	throttle "github.com/faustbrian/golib/pkg/adaptive-throttle"
	"github.com/faustbrian/golib/pkg/bulkhead"
	breaker "github.com/faustbrian/golib/pkg/circuit-breaker"
	concurrencylimit "github.com/faustbrian/golib/pkg/concurrency-limit"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/hedge"
	httpclient "github.com/faustbrian/golib/pkg/http-client"
	ratelimit "github.com/faustbrian/golib/pkg/rate-limit"
	ratelimitmemory "github.com/faustbrian/golib/pkg/rate-limit/memory"
	"github.com/faustbrian/golib/pkg/retry"
	secretenvelope "github.com/faustbrian/golib/pkg/secret-envelope"
	"github.com/faustbrian/golib/pkg/webhook"
)

const maximumPayloadBytes = 1 << 20

var (
	// ErrInvalidConfig identifies a missing or contradictory reference input.
	ErrInvalidConfig = errors.New("reference external config is invalid")
	// ErrClosed identifies work submitted after admission has closed.
	ErrClosed = errors.New("reference external service is closed")
)

// Storage is the minimum streaming filesystem contract used by the reference.
type Storage interface {
	filesystem.Reader
	filesystem.Writer
}

// Config supplies explicit external boundaries. The HTTP client owns its
// default transport; storage, envelope, signer, and endpoint policy are
// borrowed and remain caller-owned.
type Config struct {
	// Storage persists bounded reference payloads and remains caller-owned.
	Storage Storage
	// Envelopes encrypts and decrypts persisted reference payloads.
	Envelopes *secretenvelope.Service
	// WebhookEndpoint receives signed completion callbacks.
	WebhookEndpoint *url.URL
	// WebhookSigner authenticates completion callbacks.
	WebhookSigner *webhook.Signer
	// WebhookPolicy constrains allowed callback destinations.
	WebhookPolicy webhook.EndpointPolicy
}

// FetchResult reports the bounded result and policy state after one logical
// outbound call.
type FetchResult struct {
	// Body contains the bounded dependency response.
	Body []byte
	// Attempts reports the number of outbound attempts.
	Attempts uint
	// Bulkhead reports the admission state after the call.
	Bulkhead bulkhead.Snapshot
	// Breaker reports the circuit state after the call.
	Breaker breaker.Snapshot
	// Concurrency reports the adaptive concurrency state after the call.
	Concurrency concurrencylimit.Snapshot
}

// Reference owns process-local admission and resilience state for one
// maintained outbound composition.
type Reference struct {
	storage         Storage
	envelopes       *secretenvelope.Service
	webhookEndpoint *url.URL
	webhookSigner   *webhook.Signer
	webhookPolicy   webhook.EndpointPolicy
	client          *httpclient.Client
	rateStore       *ratelimitmemory.Store
	ratePolicy      ratelimit.Policy
	rateKey         ratelimit.Key
	bulkhead        *bulkhead.Bulkhead
	breaker         *breaker.Breaker
	limiter         *concurrencylimit.Limiter
	throttler       *throttle.Throttler
	retry           *retry.Policy

	mu     sync.RWMutex
	closed bool
}

// New constructs the complete outbound reference through public Golib APIs.
func New(config Config) (*Reference, error) {
	if isNil(config.Storage) || config.Envelopes == nil || config.WebhookEndpoint == nil ||
		config.WebhookSigner == nil || isNil(config.WebhookPolicy) {
		return nil, ErrInvalidConfig
	}
	client, err := httpclient.New(httpclient.Config{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	reference := &Reference{
		storage: config.Storage, envelopes: config.Envelopes,
		webhookEndpoint: cloneURL(config.WebhookEndpoint), webhookSigner: config.WebhookSigner,
		webhookPolicy: config.WebhookPolicy, client: client,
	}
	if err := reference.buildPolicies(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return reference, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (reference *Reference) buildPolicies() error {
	var err error
	reference.rateStore, err = ratelimitmemory.New(ratelimitmemory.Options{MaxKeys: 1, Shards: 1})
	if err != nil {
		return err
	}
	reference.ratePolicy, err = ratelimit.NewPolicy(ratelimit.PolicySpec{
		ID: "reference-external", Revision: "v1", Algorithm: ratelimit.TokenBucket,
		Capacity: 1_000, Period: time.Second, FailureMode: ratelimit.FailClosed,
	})
	if err != nil {
		return err
	}
	reference.rateKey, err = ratelimit.NewKey(ratelimit.KeySpec{
		Namespace: "service", Version: "v1",
		Subject: ratelimit.Subject{Kind: "dependency", Value: "reference-external"},
	})
	if err != nil {
		return err
	}
	reference.bulkhead, err = bulkhead.New(bulkhead.Config{Resource: "reference-external", Capacity: 8})
	if err != nil {
		return err
	}
	reference.breaker, err = breaker.New(breaker.Config{Name: "reference-external"})
	if err != nil {
		return err
	}
	reference.limiter, err = concurrencylimit.New(concurrencylimit.Config{
		MinLimit: 1, MaxLimit: 8, InitialLimit: 8, Algorithm: concurrencylimit.NewFixedAlgorithm(),
	})
	if err != nil {
		return err
	}
	adaptivePolicy, err := throttle.NewPolicy(throttle.PolicyConfig{
		Revision: "reference-external-v1", MaxRejectionProbability: 0.9,
		MinimumAdmissionProbability: 0.1, MaxResources: 1,
	})
	if err != nil {
		return err
	}
	reference.throttler, err = throttle.New(adaptivePolicy)
	if err != nil {
		return err
	}
	reference.retry, err = retry.NewPolicy(retry.Config{
		Backoff: retry.Constant(0), MaxAttempts: 2, HistoryLimit: 2,
		Clock: retry.SystemClock{}, Sleeper: retry.SystemSleeper{},
		Classifier: retry.RetryableClassifier(),
	})
	return err
}

// Fetch executes one bounded logical call through rate, adaptive, bulkhead,
// retry, breaker, concurrency, timeout, and HTTP-client ownership boundaries.
func (reference *Reference) Fetch(ctx context.Context, endpoint string) (FetchResult, error) {
	if err := reference.admit(ctx); err != nil {
		return FetchResult{}, err
	}
	decision, err := reference.rateStore.Admit(ctx, ratelimit.Request{
		Policy: reference.ratePolicy, Key: reference.rateKey, Cost: 1, Now: time.Now().UTC(),
	})
	if err != nil {
		return FetchResult{}, err
	}
	if !decision.Allowed {
		return FetchResult{}, ratelimit.ErrRejected
	}

	var retryResult retry.Result
	body, executionErr := throttle.Execute(ctx, reference.throttler, "reference-external", func(ctx context.Context) ([]byte, error) {
		value, _, bulkheadErr := bulkhead.Execute(ctx, reference.bulkhead, 1, func(ctx context.Context) ([]byte, error) {
			value, result, retryErr := retry.Do(ctx, reference.retry, func(ctx context.Context) ([]byte, error) {
				return breaker.Execute(ctx, reference.breaker, func(ctx context.Context) ([]byte, error) {
					return concurrencylimit.Execute(ctx, reference.limiter, func(ctx context.Context) ([]byte, error) {
						return reference.fetchOnce(ctx, endpoint)
					})
				})
			})
			retryResult = result
			return value, retryErr
		})
		return value, bulkheadErr
	})
	return FetchResult{
		Body: body, Attempts: retryResult.Attempts, Bulkhead: reference.bulkhead.Snapshot(),
		Breaker: reference.breaker.Snapshot(), Concurrency: reference.limiter.Snapshot(),
	}, executionErr
}

func (reference *Reference) fetchOnce(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := reference.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, retry.Retryable(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 500 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, retry.Retryable(fmt.Errorf("dependency status %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumPayloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maximumPayloadBytes {
		return nil, fmt.Errorf("dependency response exceeds %d bytes", maximumPayloadBytes)
	}
	return body, nil
}

// HedgedFetch executes independently constructed replay-safe requests and
// returns the first healthy result under a finite shared hedge budget.
func (reference *Reference) HedgedFetch(
	ctx context.Context,
	endpoints []string,
	delay time.Duration,
) ([]byte, hedge.Report, error) {
	if err := reference.admit(ctx); err != nil {
		return nil, hedge.Report{}, err
	}
	if len(endpoints) != 2 || delay <= 0 {
		return nil, hedge.Report{}, ErrInvalidConfig
	}
	budget, err := hedge.NewOutstandingBudget(1)
	if err != nil {
		return nil, hedge.Report{}, err
	}
	policy, err := hedge.NewPolicy(hedge.Config[[]byte]{
		MaxHedges: 1, ReplaySafe: true, Delay: delay, TotalTimeout: 2 * time.Second,
		AttemptTimeout: time.Second, CleanupTimeout: time.Second, Clock: hedge.RealClock{}, Budget: budget,
		Classifier: hedge.ClassifyFunc[[]byte](func(_ context.Context, result hedge.AttemptResult[[]byte]) (hedge.Classification, error) {
			if result.Err == nil {
				return hedge.ClassificationSuccess, nil
			}
			return hedge.ClassificationFailure, nil
		}),
		Disposer: hedge.DisposeFunc[[]byte](func(context.Context, []byte) error { return nil }),
		Resource: "reference-external", FactoryFailureMode: hedge.FactoryFailureStop,
	})
	if err != nil {
		return nil, hedge.Report{}, err
	}
	return hedge.Do(ctx, policy, hedge.AttemptFactoryFunc[[]byte](func(info hedge.AttemptInfo) (hedge.Attempt[[]byte], string, error) {
		index := 0
		if info.Hedge {
			index = 1
		}
		endpoint := endpoints[index]
		return func(attemptCtx context.Context) ([]byte, error) {
			result, fetchErr := reference.Fetch(attemptCtx, endpoint)
			return result.Body, fetchErr
		}, endpoint, nil
	}))
}

// StoreSecret encrypts an application payload before streaming its envelope to
// the configured filesystem boundary.
func (reference *Reference) StoreSecret(
	ctx context.Context,
	object filesystem.Path,
	plaintext []byte,
	keyReference string,
	binding secretenvelope.Context,
) error {
	if err := reference.admit(ctx); err != nil {
		return err
	}
	envelope, err := reference.envelopes.Encrypt(ctx, secretenvelope.EncryptRequest{
		Plaintext: plaintext, KeyReference: keyReference, Context: binding,
	})
	if err != nil {
		return err
	}
	encoded, err := envelope.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = reference.storage.Write(ctx, object, bytes.NewReader(encoded), filesystem.WriteOptions{
		ContentType: "application/vnd.golib.secret-envelope",
	})
	return err
}

// LoadSecret streams, parses, and authenticates one stored envelope.
func (reference *Reference) LoadSecret(
	ctx context.Context,
	object filesystem.Path,
	binding secretenvelope.Context,
) ([]byte, error) {
	encoded, err := reference.OpenStored(ctx, object)
	if err != nil {
		return nil, err
	}
	envelope, err := secretenvelope.ParseEnvelope(encoded)
	if err != nil {
		return nil, err
	}
	return reference.envelopes.Decrypt(ctx, secretenvelope.DecryptRequest{Envelope: envelope, Context: binding})
}

// OpenStored returns a bounded caller-owned copy of one stored object.
func (reference *Reference) OpenStored(ctx context.Context, object filesystem.Path) ([]byte, error) {
	if err := reference.admit(ctx); err != nil {
		return nil, err
	}
	reader, err := reference.storage.Open(ctx, object)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	content, err := io.ReadAll(io.LimitReader(reader, maximumPayloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maximumPayloadBytes {
		return nil, fmt.Errorf("stored object exceeds %d bytes", maximumPayloadBytes)
	}
	return content, nil
}

// DeliverWebhook signs and sends one bounded delivery through the shared HTTP
// client and explicit endpoint policy.
func (reference *Reference) DeliverWebhook(
	ctx context.Context,
	payload []byte,
	eventID string,
) (webhook.DeliveryResult, error) {
	if err := reference.admit(ctx); err != nil {
		return webhook.DeliveryResult{}, err
	}
	sequence := 0
	deliverer, err := webhook.NewDeliverer(webhook.DeliveryConfig{
		Client: reference.client, Signer: reference.webhookSigner, EndpointPolicy: reference.webhookPolicy,
		Retry: webhook.RetryPolicy{MaxAttempts: 2, MaxDelay: time.Second},
		IDGenerator: func() (string, error) {
			sequence++
			return fmt.Sprintf("reference-%d", sequence), nil
		},
		MaxRequestBytes: maximumPayloadBytes, MaxResponseBytes: maximumPayloadBytes, MaxFanOut: 1,
		HeaderLimits: webhook.HeaderLimits{MaxSignatures: 2, MaxBytes: 1024},
	})
	if err != nil {
		return webhook.DeliveryResult{}, err
	}
	return deliverer.Deliver(ctx, webhook.DeliveryRequest{
		Endpoint: cloneURL(reference.webhookEndpoint), Body: payload, EventID: eventID,
		IdempotencyKey: eventID,
	})
}

func (reference *Reference) admit(ctx context.Context) error {
	if reference == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reference.mu.RLock()
	closed := reference.closed
	reference.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	return nil
}

// Close stops admission and drains every owned process-local policy.
func (reference *Reference) Close(ctx context.Context) error {
	if reference == nil || ctx == nil {
		return ErrInvalidConfig
	}
	reference.mu.Lock()
	if reference.closed {
		reference.mu.Unlock()
		return nil
	}
	reference.closed = true
	reference.mu.Unlock()
	return errors.Join(
		reference.rateStore.Close(),
		reference.bulkhead.Close(),
		reference.bulkhead.Drain(ctx),
		reference.breaker.Shutdown(ctx),
		reference.client.Close(),
	)
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}
