package serverhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func ExampleChain() {
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		panic(err)
	}
	identity, err := httpcorrelation.New(factory, httpcorrelation.Options{})
	if err != nil {
		panic(err)
	}
	handler, err := serverhttp.Chain(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			values, _ := correlation.FromContext(request.Context())
			fmt.Println(values.RequestID != "")
		}),
		serverhttp.Recover(),
		identity.Wrap,
	)
	if err != nil {
		panic(err)
	}

	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	// Output:
	// true
}

type principalKey struct{}

func ExampleChain_authenticationAndAuthorization() {
	authentication := serverhttp.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			fmt.Println("authenticate")
			if request.Header.Get("Authorization") != "Bearer example" {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)

				return
			}
			ctx := context.WithValue(request.Context(), principalKey{}, "user-1")
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	})
	authorization := serverhttp.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			fmt.Println("authorize")
			if request.Context().Value(principalKey{}) == nil {
				http.Error(writer, "forbidden", http.StatusForbidden)

				return
			}
			next.ServeHTTP(writer, request)
		})
	})
	handler, err := serverhttp.Chain(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
		authentication,
		authorization,
	)
	if err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	fmt.Println(recorder.Code)
	// Output:
	// authenticate
	// authorize
	// 204
}
