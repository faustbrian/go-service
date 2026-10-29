package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

var (
	// ErrResourceNotStarted identifies an Owned resource that has not opened yet,
	// including while Open is still running or after a failed Open.
	ErrResourceNotStarted = errors.New("owned resource not started")
	// ErrResourceAlreadyStarted identifies a Start while Open is running or the
	// resource is already open.
	ErrResourceAlreadyStarted = errors.New("owned resource already started")
	// ErrResourceClosed identifies an Owned resource whose Stop has begun. It is
	// terminal: an Owned value is not restartable.
	ErrResourceClosed = errors.New("owned resource closed")
	// ErrResourceNil identifies an Open callback that returned a nil resource
	// without an error.
	ErrResourceNil = errors.New("owned resource open returned nil")
)

const defaultOwnedCloseTimeout = 30 * time.Second

// OwnedCloser is the close contract an Owned resource may implement instead of
// supplying OwnedConfig.Close.
type OwnedCloser interface {
	// Close releases the resource and must honor context cancellation.
	Close(context.Context) error
}

// OwnedConfig describes one lifecycle-owned resource.
type OwnedConfig[R any] struct {
	// Name is the component name; it follows Component.Name limits.
	Name string
	// Open acquires the resource. Ownership transfers to Owned only when it
	// returns a non-nil resource and a nil error; on error Open must release
	// any partial acquisition itself.
	Open func(context.Context) (R, error)
	// Close releases an opened resource. Nil selects the resource's own
	// OwnedCloser implementation; New rejects a nil Close when R does not
	// implement OwnedCloser.
	Close func(context.Context, R) error
	// CloseTimeout bounds each Close call in addition to the caller's context.
	// Zero selects 30 seconds; negative values are invalid.
	CloseTimeout time.Duration
}

type ownedState uint8

const (
	ownedNew ownedState = iota
	ownedOpening
	ownedOpen
	ownedClosed
)

// Owned holds one resource for one ordered lifecycle: new, opening, open and
// closed. Its mutex is the single synchronization owner of the state and
// resource; Open and Close always run outside it. Construct it with NewOwned
// and do not copy it after use.
//
// Current returns a borrowed snapshot, not a lease: a reader can still hold the
// resource while Stop closes it, so callers that need in-flight protection
// must order their work before component stopping (as Plan tasks and HTTP are).
type Owned[R any] struct {
	name    string
	open    func(context.Context) (R, error)
	close   func(context.Context, R) error
	timeout time.Duration

	mu         sync.RWMutex
	state      ownedState
	resource   R
	cancelOpen context.CancelCauseFunc
	stopDone   chan struct{}
	stopErr    error
}

// NewOwned validates config and returns an inert Owned. Errors are
// ConfigError values matching ErrInvalidConfig.
func NewOwned[R any](config OwnedConfig[R]) (*Owned[R], error) {
	if !validRuntimeIdentity(config.Name) {
		return nil, &ConfigError{
			Field: "Name", Reason: fmt.Sprintf("must contain 1 to %d bytes", MaxRuntimeIdentityBytes),
		}
	}
	if config.Open == nil {
		return nil, &ConfigError{Field: "Open", Reason: "must not be nil"}
	}
	if config.CloseTimeout < 0 {
		return nil, &ConfigError{Field: "CloseTimeout", Reason: "must not be negative"}
	}
	closeResource := config.Close
	if closeResource == nil {
		if !reflect.TypeFor[R]().Implements(reflect.TypeFor[OwnedCloser]()) {
			return nil, &ConfigError{Field: "Close", Reason: "is required when the resource has no Close(context.Context) error method"}
		}
		closeResource = func(ctx context.Context, resource R) error {
			return any(resource).(OwnedCloser).Close(ctx)
		}
	}
	timeout := config.CloseTimeout
	if timeout == 0 {
		timeout = defaultOwnedCloseTimeout
	}

	return &Owned[R]{name: config.Name, open: config.Open, close: closeResource, timeout: timeout}, nil
}

// Component returns the lifecycle callbacks for Plan.Components or
// Config.Components. Every call returns callbacks for the same Owned state.
//
// Start rejects a nil context with ConfigError, returns ErrResourceAlreadyStarted
// while opening or open and ErrResourceClosed after Stop. A failed Open leaves
// the resource not started and may be retried. When Open succeeds after its
// context ended or after a concurrent Stop, Start closes the unpublished
// resource itself and returns the cancellation cause or ErrResourceClosed,
// joined with any close failure.
//
// Stop is idempotent and terminal. The first call closes an open resource with
// its context bounded by CloseTimeout; Stop before Start only prevents a later
// Start. Stop during Open cancels Open's context and waits for Start to discard
// any resource it acquired. Concurrent and repeated calls wait, within their
// own contexts, for that one close and return its retained result.
func (owned *Owned[R]) Component() Component {
	return Component{Name: owned.name, Start: owned.start, Stop: owned.stop}
}

// Current returns the open resource. It returns ErrResourceNotStarted before a
// successful Start and ErrResourceClosed once Stop has begun, with the zero R.
// Applications can wrap these sentinels and still match them with errors.Is.
func (owned *Owned[R]) Current() (R, error) {
	owned.mu.RLock()
	defer owned.mu.RUnlock()

	switch owned.state {
	case ownedOpen:
		return owned.resource, nil
	case ownedClosed:
		var zero R
		return zero, ErrResourceClosed
	default:
		var zero R
		return zero, ErrResourceNotStarted
	}
}

func (owned *Owned[R]) start(ctx context.Context) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}

	openContext, cancelOpen := context.WithCancelCause(ctx)
	defer cancelOpen(nil)
	owned.mu.Lock()
	switch owned.state {
	case ownedOpening, ownedOpen:
		owned.mu.Unlock()
		return ErrResourceAlreadyStarted
	case ownedClosed:
		owned.mu.Unlock()
		return ErrResourceClosed
	}
	owned.state = ownedOpening
	owned.cancelOpen = cancelOpen
	owned.mu.Unlock()

	resource, err := owned.invokeOpen(openContext)
	if err == nil && nilResource(resource) {
		err = ErrResourceNil
	}

	owned.mu.Lock()
	owned.cancelOpen = nil
	stopped := owned.state == ownedClosed
	if err != nil {
		if stopped {
			// Nothing was acquired, so the waiting Stop has nothing to close.
			close(owned.stopDone)
		} else {
			owned.state = ownedNew
		}
		owned.mu.Unlock()
		return err
	}
	if !stopped && ctx.Err() == nil {
		owned.state = ownedOpen
		owned.resource = resource
		owned.mu.Unlock()
		return nil
	}
	owned.mu.Unlock()

	// The resource was never published, so no reader can hold it. Detach the
	// close from Start's cancellation so an ended context does not abandon it.
	cause := ErrResourceClosed
	if !stopped {
		cause = context.Cause(ctx)
	}
	closeErr := owned.closeWith(context.WithoutCancel(ctx), resource)

	owned.mu.Lock()
	if owned.state == ownedClosed {
		owned.stopErr = closeErr
		close(owned.stopDone)
	} else {
		owned.state = ownedNew
	}
	owned.mu.Unlock()

	return errors.Join(cause, closeErr)
}

func (owned *Owned[R]) stop(ctx context.Context) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}

	owned.mu.Lock()
	switch owned.state {
	case ownedNew:
		owned.state = ownedClosed
		owned.stopDone = make(chan struct{})
		close(owned.stopDone)
		owned.mu.Unlock()
		return nil
	case ownedOpening:
		owned.state = ownedClosed
		owned.stopDone = make(chan struct{})
		// cancelOpen is nil while Start discards a resource it acquired after
		// its own context ended; that discard needs no further cancellation.
		if owned.cancelOpen != nil {
			owned.cancelOpen(ErrResourceClosed)
		}
		done := owned.stopDone
		owned.mu.Unlock()
		return owned.waitStop(ctx, done)
	case ownedClosed:
		done := owned.stopDone
		owned.mu.Unlock()
		return owned.waitStop(ctx, done)
	}
	resource := owned.resource
	var zero R
	owned.resource = zero
	owned.state = ownedClosed
	owned.stopDone = make(chan struct{})
	done := owned.stopDone
	owned.mu.Unlock()

	err := owned.closeWith(ctx, resource)

	owned.mu.Lock()
	owned.stopErr = err
	close(done)
	owned.mu.Unlock()

	return err
}

// waitStop prefers a completed close so an ended waiter context still reports
// the retained result rather than a random choice between ready cases.
func (owned *Owned[R]) waitStop(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
	default:
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	owned.mu.RLock()
	defer owned.mu.RUnlock()

	return owned.stopErr
}

func (owned *Owned[R]) invokeOpen(ctx context.Context) (resource R, err error) {
	defer func() {
		if value := recover(); value != nil {
			var zero R
			resource = zero
			err = &PanicError{Component: owned.name, Operation: "open", Value: value}
		}
	}()

	return owned.open(ctx)
}

func (owned *Owned[R]) closeWith(ctx context.Context, resource R) (err error) {
	bounded, cancel := context.WithTimeout(ctx, owned.timeout)
	defer cancel()
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{Component: owned.name, Operation: "close", Value: value}
		}
	}()

	return owned.close(bounded, resource)
}

func nilResource[R any](resource R) bool {
	value := reflect.ValueOf(any(resource))
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
