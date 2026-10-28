package serverhttp

import (
	"net/http"
	"sync"
)

// handlerDrain serializes admission and completion for one server. done closes
// only after admission closes and every admitted call returns; no caller code
// or I/O runs under the mutex. A wait timeout does not change that ownership.
type handlerDrain struct {
	mu       sync.Mutex
	active   int
	draining bool
	done     chan struct{}
}

func newHandlerDrain() *handlerDrain {
	return &handlerDrain{done: make(chan struct{})}
}

func (drain *handlerDrain) wrap(next http.Handler) http.Handler {
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

func (drain *handlerDrain) release() {
	drain.mu.Lock()
	defer drain.mu.Unlock()
	drain.active--
	if drain.draining && drain.active == 0 {
		close(drain.done)
	}
}

func (drain *handlerDrain) closeAdmission() {
	drain.mu.Lock()
	defer drain.mu.Unlock()
	if !drain.draining {
		drain.draining = true
		if drain.active == 0 {
			close(drain.done)
		}
	}
}
