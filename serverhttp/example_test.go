package serverhttp_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/faustbrian/go-service/serverhttp"
)

func ExampleChain() {
	requestIDs, _ := serverhttp.RequestIDs(serverhttp.RequestIDConfig{
		Generator: func() (string, error) { return "example-id", nil },
	})
	handler, _ := serverhttp.Chain(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			requestID, _ := serverhttp.RequestID(request.Context())
			fmt.Println(requestID)
		}),
		serverhttp.Recover(),
		requestIDs,
	)

	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	// Output:
	// example-id
}
