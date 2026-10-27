package service

import (
	"net/http"
	"sync"
)

// httpHandlerDrain belongs to one business owner. Its mutex serializes admission
// and completion; done closes only after admission is closed and every handler
// returns. No callback or I/O runs under the mutex.
type httpHandlerDrain struct {
	mu       sync.Mutex
	active   int
	draining bool
	done     chan struct{}
}

func newHTTPHandlerDrain() *httpHandlerDrain {
	return &httpHandlerDrain{done: make(chan struct{})}
}

func (drain *httpHandlerDrain) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		drain.mu.Lock()
		if drain.draining {
			drain.mu.Unlock()
			http.Error(writer, "service draining", http.StatusServiceUnavailable)
			return
		}
		drain.active++
		drain.mu.Unlock()
		defer drain.release()
		next.ServeHTTP(writer, request)
	})
}

func (drain *httpHandlerDrain) release() {
	drain.mu.Lock()
	defer drain.mu.Unlock()
	drain.active--
	if drain.draining && drain.active == 0 {
		close(drain.done)
	}
}

func (drain *httpHandlerDrain) closeAdmission() error {
	drain.mu.Lock()
	defer drain.mu.Unlock()
	if !drain.draining {
		drain.draining = true
		if drain.active == 0 {
			close(drain.done)
		}
	}
	return nil
}
