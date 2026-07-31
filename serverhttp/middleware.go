package serverhttp

import (
	"net/http"

	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
)

// Middleware wraps an http.Handler. In Chain, the first middleware is the
// outermost and observes the request first and response last.
type Middleware func(http.Handler) http.Handler

// Chain composes middleware around handler in listed order.
func Chain(handler http.Handler, middleware ...Middleware) (http.Handler, error) {
	if handler == nil {
		handler = http.NotFoundHandler()
	}

	for index := len(middleware) - 1; index >= 0; index-- {
		if middleware[index] == nil {
			return nil, &ConfigError{Field: "middleware", Reason: "must not contain nil"}
		}
		handler = middleware[index](handler)
		if handler == nil {
			return nil, &ConfigError{Field: "middleware", Reason: "must not return nil"}
		}
	}

	return handler, nil
}

// Recover contains handler panics. If no response was committed, it removes
// prepared headers and sends a generic HTTP 500 response.
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		next = handlerOrNotFound(next)

		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			tracked := &responseWriter{ResponseWriter: writer}
			defer func() {
				if recover() == nil || tracked.wroteHeader {
					return
				}

				identity := map[string][]string{}
				for _, name := range []string{
					httpcorrelation.CorrelationHeader,
					httpcorrelation.RequestHeader,
					httpcorrelation.CausationHeader,
				} {
					identity[name] = append(
						[]string(nil),
						writer.Header().Values(name)...,
					)
				}
				for header := range writer.Header() {
					writer.Header().Del(header)
				}
				for name, values := range identity {
					for _, value := range values {
						writer.Header().Add(name, value)
					}
				}
				http.Error(writer, "internal server error", http.StatusInternalServerError)
			}()

			next.ServeHTTP(tracked, request)
		})
	}
}

// LimitBody rejects a known oversized request before the handler and limits
// streaming or chunked bodies before their first read. Zero disables it.
func LimitBody(limit int64) (Middleware, error) {
	if limit < 0 {
		return nil, &ConfigError{Field: "BodyLimit", Reason: "must not be negative"}
	}

	return limitBody(limit), nil
}

func limitBody(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		next = handlerOrNotFound(next)

		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if limit == 0 {
				next.ServeHTTP(writer, request)

				return
			}
			if request.ContentLength > limit {
				http.Error(writer, "request body too large", http.StatusRequestEntityTooLarge)

				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, limit)
			next.ServeHTTP(writer, request)
		})
	}
}

func handlerOrNotFound(handler http.Handler) http.Handler {
	switch handler {
	case nil:
		return http.NotFoundHandler()
	default:
		return handler
	}
}

type responseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (writer *responseWriter) WriteHeader(status int) {
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseWriter) Write(body []byte) (int, error) {
	writer.wroteHeader = true

	return writer.ResponseWriter.Write(body)
}

func (writer *responseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
