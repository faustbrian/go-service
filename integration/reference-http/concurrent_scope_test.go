package referencehttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/faustbrian/go-audit"
	referencehttp "github.com/faustbrian/go-service/integration/reference-http"
)

type tenantTestRoute struct{}

type tenantTestTransport struct{ trusted, untrusted *http.Transport }

func (transport tenantTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if untrusted, _ := request.Context().Value(tenantTestRoute{}).(bool); untrusted {
		return transport.untrusted.RoundTrip(request)
	}
	return transport.trusted.RoundTrip(request)
}

// One reference still has one configured tenant/principal. The two caller-owned
// pools model admitted and non-admitted peers; no HTTP header grants trust.
func TestReferenceHTTPSignedConcurrentTenantScopesRemainIsolated(t *testing.T) {
	t.Parallel()
	const perClass, requests = 6, 18 // Accepted audit records stay below capacity.
	trusted, probeClient := ownedClient(t)
	untrusted, _ := ownedClient(t)
	business, management := listen(t), listen(t)
	var trustedPeers sync.Map
	dial := trusted.DialContext
	trusted.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := dial(ctx, network, address)
		if err == nil && address == business.Addr().String() {
			// Register the real source socket before any request bytes are sent.
			trustedPeers.Store(connection.LocalAddr().String(), true)
		}
		return connection, err
	}
	var arrived, refused atomic.Int64
	allArrived := make(chan struct{})
	reference, err := referencehttp.New(referencehttp.Config{
		ServiceName: "reference-http", Version: "1.0.0", Environment: "test",
		BearerToken: referenceBearer, PrincipalID: "reference-client", TenantID: referenceTenant,
		BusinessListener: business, ManagementListener: management,
		ClientTransport: tenantTestTransport{trusted: trusted, untrusted: untrusted},
		Readiness:       func(context.Context) error { return nil },
		TrustTenant: func(request *http.Request) bool {
			_, admitted := trustedPeers.Load(request.RemoteAddr)
			if !admitted {
				refused.Add(1)
			}
			if arrived.Add(1) == requests {
				close(allArrived)
			}
			// Hold all tenant decisions concurrently, without timing sleeps.
			select {
			case <-allArrived:
				return admitted
			case <-request.Context().Done():
				return false
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reference.Telemetry().Shutdown(context.Background()) })
	runReference(t, reference, trusted)
	// Release the second borrowed pool before service shutdown as well.
	t.Cleanup(untrusted.CloseIdleConnections)
	awaitStatus(t, probeClient, "http://"+management.Addr().String()+"/readyz", http.StatusOK)

	type outcome struct {
		index    int
		response *http.Response
		wire     []byte
		err      error
	}
	completed := make(chan outcome, requests)
	start := make(chan struct{})
	for index := range requests {
		tenant := referenceTenant
		if index >= perClass && index < 2*perClass {
			tenant = "tenant-other"
		}
		request := echoRequest(t, business, referenceBearer, tenant, fmt.Sprintf("scope-%02d", index))
		request = request.WithContext(t.Context())
		request.Header.Set("X-Correlation-ID", fmt.Sprintf("untrusted-flow-%02d", index))
		request.Header.Set("X-Request-ID", fmt.Sprintf("untrusted-hop-%02d", index))
		request.Header.Set("X-Actor-ID", fmt.Sprintf("untrusted-actor-%02d", index))
		if index >= 2*perClass {
			request = request.WithContext(context.WithValue(request.Context(), tenantTestRoute{}, true))
		}
		if err := reference.PrepareRequest(request); err != nil {
			t.Fatal(err)
		}
		go func() {
			<-start
			response, err := reference.Client().Do(request)
			result := outcome{index: index, response: response, err: err}
			if err == nil {
				result.wire, err = io.ReadAll(io.LimitReader(response.Body, 4097))
				result.err = errors.Join(err, response.Body.Close())
			}
			completed <- result
		}()
	}
	close(start)
	accepted := make(map[string]string, perClass)
	seenCorrelations, seenRequests := make(map[string]bool), make(map[string]bool)
	for range requests {
		result := <-completed
		if result.err != nil {
			t.Fatalf("signed RPC %d failed: %v", result.index, result.err)
		}
		response := result.response
		wire := result.wire
		if len(wire) > 4096 {
			t.Fatal("RPC response exceeded its bounded read")
		}
		correlation, requestID := response.Header.Get("X-Correlation-ID"), response.Header.Get("X-Request-ID")
		if correlation == "" || requestID == "" || correlation == requestID || seenCorrelations[correlation] || seenRequests[requestID] {
			t.Fatal("concurrent RPCs shared correlation/request identity")
		}
		for index := range requests {
			if correlation == fmt.Sprintf("untrusted-flow-%02d", index) || requestID == fmt.Sprintf("untrusted-hop-%02d", index) {
				t.Fatal("untrusted correlation headers became request identity")
			}
		}
		seenCorrelations[correlation], seenRequests[requestID] = true, true
		if result.index >= perClass {
			want := http.StatusForbidden
			if result.index >= 2*perClass {
				want = http.StatusBadRequest
			}
			if response.StatusCode != want || bytes.Contains(wire, []byte(`"result"`)) || bytes.Contains(wire, []byte(referenceBearer)) {
				t.Fatalf("rejected tenant RPC %d status=%d, want=%d without success/credential disclosure", result.index, response.StatusCode, want)
			}
			continue
		}
		var payload struct {
			Result struct {
				Message       string `json:"message"`
				Principal     string `json:"principal"`
				Tenant        string `json:"tenant"`
				CorrelationID string `json:"correlation_id"`
				RequestID     string `json:"request_id"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(wire, &payload); err != nil {
			t.Fatal(err)
		}
		value := payload.Result
		if response.StatusCode != http.StatusOK || len(payload.Error) != 0 || value.Message != fmt.Sprintf("scope-%02d", result.index) ||
			value.Principal != "reference-client" || value.Tenant != referenceTenant || value.CorrelationID != correlation || value.RequestID != requestID {
			t.Fatal("accepted RPC lost its own authenticated scope")
		}
		accepted[requestID] = correlation
	}
	if arrived.Load() != requests || refused.Load() != perClass || len(accepted) != perClass {
		t.Fatal("traffic did not exercise both peer trust decisions and the accepted tenant")
	}
	tenant, err := audit.Tenant(referenceTenant)
	if err != nil {
		t.Fatal(err)
	}
	query, err := audit.NewQuery(audit.QueryInput{Tenant: tenant, Action: "reference.echo", Limit: 99})
	if err != nil {
		t.Fatal(err)
	}
	page, err := reference.AuditStore().Query(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != perClass || !page.Next.IsZero() {
		t.Fatal("rejected traffic contaminated accepted audit records")
	}
	for _, record := range page.Records {
		requestID := record.Context().RequestID()
		correlation, exists := accepted[requestID]
		if !exists || record.Context().CorrelationID() != correlation || record.Context().TenantID() != referenceTenant || record.Actor().ID() != "reference-client" {
			t.Fatal("audit record crossed request/actor/tenant/correlation scope")
		}
		delete(accepted, requestID)
	}
	if len(accepted) != 0 {
		t.Fatal("accepted requests did not each produce their own audit record")
	}
	otherTenant, err := audit.Tenant("tenant-other")
	if err != nil {
		t.Fatal(err)
	}
	query, err = audit.NewQuery(audit.QueryInput{Tenant: otherTenant, Action: "reference.echo", Limit: 99})
	if err != nil {
		t.Fatal(err)
	}
	otherPage, err := reference.AuditStore().Query(t.Context(), query)
	if err != nil || len(otherPage.Records) != 0 {
		t.Fatal("wrong tenant acquired an audit scope")
	}
}
