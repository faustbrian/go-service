package serverhttp

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
)

const (
	defaultRequestIDHeader    = "X-Request-ID"
	defaultRequestIDMaxLength = 128
)

// Middleware wraps an http.Handler. In Chain, the first middleware is the
// outermost and observes the request first and response last.
type Middleware func(http.Handler) http.Handler

// RequestIDGenerator creates a request ID.
type RequestIDGenerator func() (string, error)

// RequestIDConfig controls correlation ID generation and inbound trust.
type RequestIDConfig struct {
	// Header is the HTTP token used for request and response correlation. Empty
	// uses X-Request-ID.
	Header string
	// TrustInbound permits a valid bounded client value to become the request ID.
	TrustInbound bool
	// MaxLength bounds trusted and generated IDs. Zero uses the default.
	MaxLength int
	// Generator creates an ID when no trusted value is available. Nil uses a
	// cryptographically random standard-library generator.
	Generator RequestIDGenerator
}

type requestIDKey struct{}

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
		if next == nil {
			next = http.NotFoundHandler()
		}

		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			tracked := &responseWriter{ResponseWriter: writer}
			defer func() {
				if recover() == nil || tracked.wroteHeader {
					return
				}

				for header := range writer.Header() {
					writer.Header().Del(header)
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
		if next == nil {
			next = http.NotFoundHandler()
		}

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

// RequestIDs returns request ID middleware after validating its configuration.
func RequestIDs(config RequestIDConfig) (Middleware, error) {
	configured, err := normalizeRequestIDConfig(config)
	if err != nil {
		return nil, err
	}

	return requestIDs(configured), nil
}

func requestIDs(configured RequestIDConfig) Middleware {
	return func(next http.Handler) http.Handler {
		if next == nil {
			next = http.NotFoundHandler()
		}

		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requestID := ""
			if configured.TrustInbound {
				candidate := request.Header.Get(configured.Header)
				if validRequestID(candidate, configured.MaxLength) {
					requestID = candidate
				}
			}
			if requestID == "" {
				generated, generateErr := configured.Generator()
				if generateErr != nil || !validRequestID(generated, configured.MaxLength) {
					http.Error(writer, "internal server error", http.StatusInternalServerError)

					return
				}
				requestID = generated
			}

			writer.Header().Set(configured.Header, requestID)
			ctx := context.WithValue(request.Context(), requestIDKey{}, requestID)
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
}

// RequestID returns the request ID installed by RequestIDs.
func RequestID(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDKey{}).(string)

	return requestID, ok
}

func normalizeRequestIDConfig(config RequestIDConfig) (RequestIDConfig, error) {
	if config.Header == "" {
		config.Header = defaultRequestIDHeader
	}
	if !validToken(config.Header) {
		return RequestIDConfig{}, &ConfigError{
			Field:  "RequestID.Header",
			Reason: "must be an HTTP token",
		}
	}
	if config.MaxLength < 0 {
		return RequestIDConfig{}, &ConfigError{
			Field:  "RequestID.MaxLength",
			Reason: "must not be negative",
		}
	}
	if config.MaxLength == 0 {
		config.MaxLength = defaultRequestIDMaxLength
	}
	if config.Generator == nil {
		config.Generator = randomRequestID
	}

	return config, nil
}

func randomRequestID() (string, error) {
	return rand.Text(), nil
}

func validRequestID(value string, maxLength int) bool {
	return value != "" && len(value) <= maxLength && validToken(value)
}

func validToken(value string) bool {
	for _, character := range value {
		if character < 33 || character > 126 || strings.ContainsRune(
			"()<>@,;:\"/[]?={}\\",
			character,
		) {
			return false
		}
	}

	return true
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
