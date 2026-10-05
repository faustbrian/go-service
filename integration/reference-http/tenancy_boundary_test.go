package referencehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/go-authentication"
	"github.com/faustbrian/go-authorization"
	tenanthttp "github.com/faustbrian/go-tenancy/v2/http"
)

func TestPublishedTenancyV2ScopeReachesAuthorization(t *testing.T) {
	principal, err := authentication.NewPrincipal(authentication.PrincipalSpec{
		Subject: "reference-client", Method: "bearer",
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := tenanthttp.New(tenanthttp.Options{
		Trust: func(*http.Request) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	request.Header.Set(tenanthttp.DefaultHeader, "tenant-reference")
	request = request.WithContext(authentication.ContextWithPrincipal(request.Context(), principal))
	var mapped authorization.Request
	var mappingErr error
	called := false
	handler := adapter.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, scoped *http.Request) {
		called = true
		mapped, mappingErr = mapAuthorizationRequest(scoped)
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("trusted tenant request did not reach authorization: called=%v status=%d", called, response.Code)
	}
	if mappingErr != nil {
		t.Fatalf("published Tenancy v2 scope was not accepted: %v", mappingErr)
	}
	if mapped.Tenant != authorization.TenantID("tenant-reference") ||
		mapped.Subject.ID != authorization.SubjectID("reference-client") ||
		mapped.Subject.Kind != authorization.SubjectServiceAccount ||
		mapped.Action != "reference.echo" || mapped.Resource.Type != "reference-service" {
		t.Fatalf("mapped tenant authorization = %#v", mapped)
	}
}
