package referencehttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/audit"
	"github.com/faustbrian/golib/pkg/service"
	referencehttp "github.com/faustbrian/golib/pkg/service/integration/reference-http"
)

const (
	referenceBearer = "reference-test-bearer"
	referenceTenant = "tenant-reference"
)

func TestReferenceHTTPServiceLifecycleAndRequestContract(t *testing.T) {
	t.Parallel()

	var dependencyReady atomic.Bool
	business := listen(t)
	management := listen(t)
	reference, err := referencehttp.New(referencehttp.Config{
		ServiceName:        "reference-http",
		Version:            "1.0.0",
		Environment:        "test",
		BearerToken:        referenceBearer,
		PrincipalID:        "reference-client",
		TenantID:           referenceTenant,
		BusinessListener:   business,
		ManagementListener: management,
		TrustTenant:        func(*http.Request) bool { return true },
		Readiness: func(context.Context) error {
			if !dependencyReady.Load() {
				return referencehttp.ErrDependencyUnavailable
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("referencehttp.New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, reference.Definition(), service.Invocation{
			Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard,
		})
	}()

	managementURL := "http://" + management.Addr().String()
	awaitStatus(t, managementURL+"/startupz", http.StatusOK)
	awaitStatus(t, managementURL+"/readyz", http.StatusServiceUnavailable)
	dependencyReady.Store(true)
	awaitStatus(t, managementURL+"/readyz", http.StatusOK)

	response := callEcho(t, reference, business, referenceBearer, referenceTenant, "parcel ready")
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("RPC status = %d", response.StatusCode)
	}
	correlationID := response.Header.Get("X-Correlation-ID")
	requestID := response.Header.Get("X-Request-ID")
	if correlationID == "" || requestID == "" || correlationID == requestID {
		t.Fatalf("correlation headers = (%q, %q)", correlationID, requestID)
	}
	var payload struct {
		Result struct {
			Message       string `json:"message"`
			Principal     string `json:"principal"`
			Tenant        string `json:"tenant"`
			CorrelationID string `json:"correlation_id"`
			RequestID     string `json:"request_id"`
		} `json:"result"`
	}
	decodeBody(t, response, &payload)
	if payload.Result.Message != "parcel ready" ||
		payload.Result.Principal != "reference-client" ||
		payload.Result.Tenant != referenceTenant ||
		payload.Result.CorrelationID != correlationID ||
		payload.Result.RequestID != requestID {
		t.Fatalf("RPC result = %#v", payload.Result)
	}

	queryTenant, err := audit.Tenant(referenceTenant)
	if err != nil {
		t.Fatal(err)
	}
	query, err := audit.NewQuery(audit.QueryInput{
		Tenant: queryTenant, Action: "reference.echo", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := reference.AuditStore().Query(context.Background(), query)
	if err != nil {
		t.Fatalf("AuditStore().Query() error = %v", err)
	}
	if len(page.Records) != 1 ||
		page.Records[0].Actor().ID() != "reference-client" ||
		page.Records[0].Context().CorrelationID() != correlationID {
		t.Fatalf("audit records = %#v", page.Records)
	}
	if spans := reference.Telemetry().Spans(); len(spans) != 1 || spans[0].Name != "reference.rpc" {
		t.Fatalf("telemetry spans = %#v", spans)
	}

	unsignedRequest := echoRequest(t, business, referenceBearer, referenceTenant, "unsigned")
	if err := reference.PrepareRequest(unsignedRequest); err != nil {
		t.Fatal(err)
	}
	unsigned, err := http.DefaultClient.Do(unsignedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if unsigned.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned response status = %d", unsigned.StatusCode)
	}
	_ = unsigned.Body.Close()

	tamperedRequest := echoRequest(t, business, referenceBearer, referenceTenant, "tampered")
	if err := reference.PrepareRequest(tamperedRequest); err != nil {
		t.Fatal(err)
	}
	urlQuery := tamperedRequest.URL.Query()
	urlQuery.Set("cap", urlQuery.Get("cap")+"x")
	tamperedRequest.URL.RawQuery = urlQuery.Encode()
	tampered, err := reference.Client().Do(tamperedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if tampered.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered capability status = %d", tampered.StatusCode)
	}
	_ = tampered.Body.Close()

	unauthorized := callEcho(t, reference, business, "wrong", referenceTenant, "blocked")
	if unauthorized.StatusCode != http.StatusUnauthorized ||
		unauthorized.Header.Get("X-Correlation-ID") == "" {
		t.Fatalf("unauthorized response = (%d, %q)", unauthorized.StatusCode, unauthorized.Header.Get("X-Correlation-ID"))
	}
	_ = unauthorized.Body.Close()

	wrongTenant := callEcho(t, reference, business, referenceBearer, "tenant-other", "blocked")
	if wrongTenant.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong-tenant response status = %d", wrongTenant.StatusCode)
	}
	_ = wrongTenant.Body.Close()

	invalid := callEcho(t, reference, business, referenceBearer, referenceTenant, "")
	t.Cleanup(func() { _ = invalid.Body.Close() })
	var invalidPayload struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, invalid, &invalidPayload)
	if invalidPayload.Error.Code != -32602 {
		t.Fatalf("invalid params response = %#v", invalidPayload)
	}

	cancel()
	select {
	case code := <-result:
		if code != 0 {
			t.Fatalf("service exit code = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop within five seconds")
	}
	if err := reference.Telemetry().Shutdown(context.Background()); err != nil {
		t.Fatalf("telemetry shutdown error = %v", err)
	}
}

func TestReferenceHTTPRejectsInvalidConstructionAndPreparation(t *testing.T) {
	t.Parallel()

	if _, err := referencehttp.New(referencehttp.Config{}); !errors.Is(err, referencehttp.ErrInvalidConfig) {
		t.Fatalf("New(zero) error = %v", err)
	}
	var missing *referencehttp.Reference
	if err := missing.PrepareRequest(nil); !errors.Is(err, referencehttp.ErrInvalidConfig) {
		t.Fatalf("nil PrepareRequest error = %v", err)
	}

	business := listen(t)
	management := listen(t)
	defer func() { _ = business.Close() }()
	defer func() { _ = management.Close() }()
	_, err := referencehttp.New(referencehttp.Config{
		ServiceName: "reference-http", Version: "1.0.0", Environment: "test",
		BearerToken: referenceBearer, PrincipalID: "reference-client", TenantID: "invalid tenant",
		BusinessListener: business, ManagementListener: management,
		TrustTenant: func(*http.Request) bool { return true },
		Readiness:   func(context.Context) error { return nil },
	})
	if !errors.Is(err, referencehttp.ErrInvalidConfig) {
		t.Fatalf("New(invalid tenant) error = %v", err)
	}
}

func TestReferenceHTTPServiceCanRestartWithFreshListeners(t *testing.T) {
	t.Parallel()

	for cycle := 0; cycle < 2; cycle++ {
		business := listen(t)
		management := listen(t)
		reference, err := referencehttp.New(referencehttp.Config{
			ServiceName: "reference-http", Version: "1.0.0", Environment: "test",
			BearerToken: referenceBearer, PrincipalID: "reference-client",
			TenantID: referenceTenant, BusinessListener: business,
			ManagementListener: management,
			TrustTenant:        func(*http.Request) bool { return true },
			Readiness:          func(context.Context) error { return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan int, 1)
		go func() {
			result <- service.Execute(ctx, reference.Definition(), service.Invocation{
				Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard,
			})
		}()
		awaitStatus(t, "http://"+management.Addr().String()+"/readyz", http.StatusOK)
		response := callEcho(t, reference, business, referenceBearer, referenceTenant, "restart")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("cycle %d status = %d", cycle, response.StatusCode)
		}
		_ = response.Body.Close()
		cancel()
		if code := <-result; code != 0 {
			t.Fatalf("cycle %d exit code = %d", cycle, code)
		}
		_ = reference.Telemetry().Shutdown(context.Background())
	}
}

func callEcho(
	t *testing.T,
	reference *referencehttp.Reference,
	listener net.Listener,
	token, tenant, message string,
) *http.Response {
	t.Helper()
	request := echoRequest(t, listener, token, tenant, message)
	if err := reference.PrepareRequest(request); err != nil {
		t.Fatalf("prepare request: %v", err)
	}
	response, err := reference.Client().Do(request)
	if err != nil {
		t.Fatalf("RPC request error = %v", err)
	}
	return response
}

func echoRequest(t *testing.T, listener net.Listener, token, tenant, message string) *http.Request {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "app.echo",
		"params": map[string]any{"data": map[string]string{"message": message}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+listener.Addr().String()+"/rpc",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Tenant-ID", tenant)
	return request
}

func decodeBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func awaitStatus(t *testing.T, endpoint string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d", endpoint, want)
}
