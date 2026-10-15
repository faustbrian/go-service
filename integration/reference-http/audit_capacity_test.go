package referencehttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/faustbrian/go-audit"
	referencehttp "github.com/faustbrian/go-service/integration/reference-http"
)

// This characterizes the pinned public audit sink: capacity rejects, not evicts.
func TestReferenceHTTPAuditCapacityFailsClosedAndPreservesAcceptedRecords(t *testing.T) {
	t.Parallel()
	transport, client := ownedClient(t)
	business, management := listen(t), listen(t)
	reference, err := referencehttp.New(referencehttp.Config{
		ServiceName: "reference-http", Version: "1.0.0", Environment: "test",
		BearerToken: referenceBearer, PrincipalID: "reference-client", TenantID: referenceTenant,
		BusinessListener: business, ManagementListener: management, ClientTransport: transport,
		TrustTenant: func(*http.Request) bool { return true }, Readiness: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reference.Telemetry().Shutdown(context.Background()) })
	runReference(t, reference, transport)
	awaitStatus(t, client, "http://"+management.Addr().String()+"/readyz", http.StatusOK)

	requestIDs := make(map[string]bool)
	for index := range 100 {
		message := fmt.Sprintf("accepted-%d", index)
		response := callEcho(t, reference, business, referenceBearer, referenceTenant, message)
		var payload struct {
			Result struct {
				Message string `json:"message"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&payload)
		closeErr := response.Body.Close()
		if decodeErr != nil || closeErr != nil {
			t.Fatal("could not decode and close accepted RPC response")
		}
		if response.StatusCode != http.StatusOK || payload.Result.Message != message || len(payload.Error) != 0 {
			t.Fatalf("accepted RPC %d lost its success contract", index)
		}
		requestID := response.Header.Get("X-Request-ID")
		if requestID == "" || requestIDs[requestID] {
			t.Fatal("accepted RPC did not have a distinct request identity")
		}
		requestIDs[requestID] = true
	}
	tenant, err := audit.Tenant(referenceTenant)
	if err != nil {
		t.Fatal(err)
	}
	query, err := audit.NewQuery(audit.QueryInput{Tenant: tenant, Action: "reference.echo", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string][]byte {
		t.Helper()
		page, err := reference.AuditStore().Query(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Records) != 100 || !page.Next.IsZero() {
			t.Fatalf("retained audit records = %d, want exactly 100", len(page.Records))
		}
		records := make(map[string][]byte, len(page.Records))
		encodedBytes := 0
		seenRequests := make(map[string]bool, len(page.Records))
		for _, record := range page.Records {
			encoded, err := audit.CanonicalJSON(record)
			if err != nil {
				t.Fatal(err)
			}
			requestID := record.Context().RequestID()
			if !requestIDs[requestID] || seenRequests[requestID] || record.Actor().ID() != "reference-client" {
				t.Fatal("retained record lost its authenticated RPC identity")
			}
			if bytes.Contains(encoded, []byte(referenceBearer)) {
				t.Fatal("audit record disclosed bearer credentials")
			}
			seenRequests[requestID] = true
			records[record.ID()] = encoded
			encodedBytes += len(encoded)
		}
		if len(records) != 100 || encodedBytes > 1<<20 {
			t.Fatal("retained audit snapshot exceeded its record/byte budget")
		}
		return records
	}
	before := snapshot()
	for range 2 {
		response := callEcho(t, reference, business, referenceBearer, referenceTenant, "overflow-must-not-succeed")
		wire, err := io.ReadAll(io.LimitReader(response.Body, 4097))
		_ = response.Body.Close()
		if err != nil || len(wire) > 4096 {
			t.Fatal("could not read bounded overflow response")
		}
		var payload struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int             `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(wire, &payload); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || len(payload.Result) != 0 || payload.Error == nil || payload.Error.Code != -32603 || payload.Error.Message != "Internal error" || len(payload.Error.Data) != 0 {
			t.Fatal("audit overflow exposed an apparent RPC success or non-generic error")
		}
		for _, private := range []string{referenceBearer, audit.ErrBackpressure.Error(), "overflow-must-not-succeed"} {
			if bytes.Contains(wire, []byte(private)) {
				t.Fatal("audit failure response disclosed credential, backend or rejected-operation details")
			}
		}
		for id, encoded := range snapshot() {
			if !bytes.Equal(encoded, before[id]) {
				t.Fatal("overflow evicted or changed a previously accepted audit record")
			}
		}
	}
}
