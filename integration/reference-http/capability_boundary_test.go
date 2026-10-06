package referencehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/go-capability/v2"
	caphttp "github.com/faustbrian/go-capability/v2/adapters/http"
	"github.com/faustbrian/go-service"
)

func TestPublishedCapabilityV2ReusableGrantReachesAuthorization(t *testing.T) {
	security, err := newRequestSecurity(Config{})
	if err != nil {
		t.Fatalf("construct request security: %v", err)
	}
	reference := &Reference{
		definition: service.Definition{Identity: service.Identity{Name: "reference-service"}},
		capability: security.capabilitySigner,
		capProfile: security.capabilityProfile,
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	if err := reference.PrepareRequest(request); err != nil {
		t.Fatalf("prepare signed request: %v", err)
	}
	var dispatched int
	handler := security.capabilityVerification(capabilityAuthorization("reference-service")(
		http.HandlerFunc(func(writer http.ResponseWriter, scoped *http.Request) {
			grant, present := caphttp.GrantFromContext(scoped.Context())
			if !present {
				t.Fatal("verified grant missing from application context")
			}
			payload := grant.Payload()
			if payload.Issuer != "reference-http" || payload.Resource != "/rpc" || payload.Operation != http.MethodPost || len(payload.Audiences) != 1 || payload.Audiences[0] != "reference-service" {
				t.Fatal("verified grant changed explicit reference authority")
			}
			if err := grant.Authorize(capability.Use{
				Issuer: "reference-http", Audience: "reference-service", Resource: "/rpc", Operation: http.MethodPost,
			}); err != nil {
				t.Fatalf("authorize trusted operation: %v", err)
			}
			dispatched++
			writer.WriteHeader(http.StatusNoContent)
		}),
	))
	// This reference route deliberately uses reusable authorization, not a
	// bounded-use consumption store: the same valid request remains authorized.
	for range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("trusted reusable grant status = %d", response.Code)
		}
	}
	if dispatched != 2 {
		t.Fatalf("application dispatches = %d, want 2", dispatched)
	}
}
