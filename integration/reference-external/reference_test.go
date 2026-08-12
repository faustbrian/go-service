package referenceexternal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	filesystemmemory "github.com/faustbrian/golib/pkg/filesystem/memory"
	filesystemS3 "github.com/faustbrian/golib/pkg/filesystem/s3"
	"github.com/faustbrian/golib/pkg/hedge"
	secretenvelope "github.com/faustbrian/golib/pkg/secret-envelope"
	"github.com/faustbrian/golib/pkg/secret-envelope/adapters/keyring"
	referenceexternal "github.com/faustbrian/golib/pkg/service/integration/reference-external"
	"github.com/faustbrian/golib/pkg/webhook"
)

func TestReferenceComposesOutboundPoliciesAndSecretStorage(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte("vendor-ok"))
	}))
	t.Cleanup(upstream.Close)

	receiverCalls := atomic.Int64{}
	receiver := newWebhookReceiver(t, &receiverCalls)
	t.Cleanup(receiver.Close)

	reference := newReference(t, receiver.URL, newS3Storage(t))
	t.Cleanup(func() {
		if err := reference.Close(context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	result, err := reference.Fetch(context.Background(), upstream.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(result.Body) != "vendor-ok" || result.Attempts != 2 || attempts.Load() != 2 {
		t.Fatalf("Fetch() = %+v, attempts = %d", result, attempts.Load())
	}
	completed := result.Concurrency.Outcomes.Success + result.Concurrency.Outcomes.DependencyFailure
	if result.Bulkhead.Executions != 1 || result.Breaker.Completed != 2 || completed != 2 {
		t.Fatalf("policy snapshots = bulkhead:%+v breaker:%+v concurrency:%+v", result.Bulkhead, result.Breaker, result.Concurrency)
	}

	object := filesystem.MustParsePath("secrets/vendor.bin")
	binding, err := secretenvelope.NewContext(map[string]string{
		"service": "reference-external", "record": "vendor",
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("credential-value")
	if err := reference.StoreSecret(context.Background(), object, plaintext, "key-v1", binding); err != nil {
		t.Fatalf("StoreSecret() error = %v", err)
	}
	loaded, err := reference.LoadSecret(context.Background(), object, binding)
	if err != nil {
		t.Fatalf("LoadSecret() error = %v", err)
	}
	if !bytes.Equal(loaded, plaintext) {
		t.Fatalf("LoadSecret() = %q", loaded)
	}
	raw, err := reference.OpenStored(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, plaintext) {
		t.Fatal("stored envelope contains plaintext")
	}

	delivery, err := reference.DeliverWebhook(context.Background(), []byte(`{"event":"ready"}`), "event-1")
	if err != nil {
		t.Fatalf("DeliverWebhook() error = %v", err)
	}
	if len(delivery.Attempts) != 1 || delivery.Attempts[0].StatusCode != http.StatusNoContent || receiverCalls.Load() != 1 {
		t.Fatalf("delivery = %+v, receiver calls = %d", delivery, receiverCalls.Load())
	}
}

func TestReferenceHedgeSelectsAHealthyReplicaAndBoundsTimeouts(t *testing.T) {
	t.Parallel()

	blocked := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(blocked.Close)
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("healthy"))
	}))
	t.Cleanup(healthy.Close)

	reference := newReference(t, healthy.URL, filesystemmemory.New())
	t.Cleanup(func() { _ = reference.Close(context.Background()) })

	value, report, err := reference.HedgedFetch(context.Background(), []string{blocked.URL, healthy.URL}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("HedgedFetch() error = %v", err)
	}
	if string(value) != "healthy" || report.HedgesStarted != 1 || report.Reason != hedge.ReasonWinnerSelected {
		t.Fatalf("HedgedFetch() = %q, %+v", value, report)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = reference.Fetch(ctx, blocked.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fetch(timeout) error = %v", err)
	}
}

func TestReferenceRejectsInvalidConfigurationAndClosedAdmission(t *testing.T) {
	t.Parallel()

	if _, err := referenceexternal.New(referenceexternal.Config{}); !errors.Is(err, referenceexternal.ErrInvalidConfig) {
		t.Fatalf("New(zero) error = %v", err)
	}
	var nilStorage *nilReferenceStorage
	var nilPolicy webhook.EndpointPolicyFunc
	for name, config := range map[string]referenceexternal.Config{
		"typed nil storage": validConfig(t, "http://127.0.0.1", nilStorage),
		"typed nil policy":  validConfigWithPolicy(t, "http://127.0.0.1", filesystemmemory.New(), nilPolicy),
	} {
		if _, err := referenceexternal.New(config); !errors.Is(err, referenceexternal.ErrInvalidConfig) {
			t.Fatalf("New(%s) error = %v", name, err)
		}
	}
	reference := newReference(t, "http://127.0.0.1", filesystemmemory.New())
	if err := reference.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := reference.Fetch(context.Background(), "http://127.0.0.1"); !errors.Is(err, referenceexternal.ErrClosed) {
		t.Fatalf("Fetch(closed) error = %v", err)
	}
}

type nilReferenceStorage struct {
	filesystemmemory.Adapter
}

func newReference(t *testing.T, webhookEndpoint string, storage referenceexternal.Storage) *referenceexternal.Reference {
	t.Helper()
	reference, err := referenceexternal.New(validConfig(t, webhookEndpoint, storage))
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func validConfig(t *testing.T, webhookEndpoint string, storage referenceexternal.Storage) referenceexternal.Config {
	t.Helper()
	return validConfigWithPolicy(t, webhookEndpoint, storage, webhook.EndpointPolicyFunc(func(context.Context, *url.URL) error { return nil }))
}

func validConfigWithPolicy(
	t *testing.T,
	webhookEndpoint string,
	storage referenceexternal.Storage,
	policy webhook.EndpointPolicy,
) referenceexternal.Config {
	t.Helper()
	provider, err := keyring.New(map[string][]byte{"key-v1": bytes.Repeat([]byte{0x42}, secretenvelope.DataKeySize)})
	if err != nil {
		t.Fatal(err)
	}
	envelopes, err := secretenvelope.NewService(provider)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(webhookEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := webhook.NewSigner(webhook.SignerConfig{
		Algorithm: webhook.SHA256,
		Keys:      []webhook.SigningKey{{ID: "reference", Secret: []byte("reference-webhook-key")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return referenceexternal.Config{
		Storage: storage, Envelopes: envelopes, WebhookEndpoint: endpoint,
		WebhookSigner: signer,
		WebhookPolicy: policy,
	}
}

func newS3Storage(t *testing.T) referenceexternal.Storage {
	t.Helper()
	objects := &s3Objects{values: make(map[string][]byte)}
	server := httptest.NewServer(objects)
	t.Cleanup(server.Close)
	client := awss3.New(awss3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(server.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("reference", "reference", ""),
	})
	adapter, err := filesystemS3.New(client, "reference")
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

type s3Objects struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func (objects *s3Objects) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(request.Body, 2<<20))
		if err != nil {
			http.Error(writer, "read failed", http.StatusBadRequest)
			return
		}
		objects.mu.Lock()
		objects.values[request.URL.Path] = append([]byte(nil), body...)
		objects.mu.Unlock()
		writer.Header().Set("ETag", `"reference"`)
		writer.WriteHeader(http.StatusOK)
	case http.MethodHead:
		objects.mu.RLock()
		body, ok := objects.values[request.URL.Path]
		objects.mu.RUnlock()
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
		writer.Header().Set("Content-Type", "application/vnd.golib.secret-envelope")
		writer.Header().Set("ETag", `"reference"`)
		writer.WriteHeader(http.StatusOK)
	case http.MethodGet:
		objects.mu.RLock()
		body, ok := objects.values[request.URL.Path]
		objects.mu.RUnlock()
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = writer.Write(body)
	default:
		http.Error(writer, "unsupported", http.StatusMethodNotAllowed)
	}
}

func newWebhookReceiver(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	verifier, err := webhook.NewVerifier(webhook.VerifierConfig{
		Algorithm: webhook.SHA256,
		Keys:      []webhook.VerificationKey{{ID: "reference", Secret: []byte("reference-webhook-key")}},
		Tolerance: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := verifier.Middleware(webhook.MiddlewareConfig{Request: webhook.RequestOptions{
		MaxBodyBytes: 1 << 20, HeaderLimits: webhook.HeaderLimits{MaxSignatures: 1, MaxBytes: 1024},
	}}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, ok := webhook.VerifiedBodyFromContext(request.Context())
		if !ok || len(body) == 0 {
			http.Error(writer, "missing verified body", http.StatusBadRequest)
			return
		}
		calls.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(handler)
}
