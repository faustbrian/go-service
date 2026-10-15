package serverhttp

import (
	"net"
	"sync"
)

// connectionLimitListener owns admission reservations, not connection wrappers.
// Returning the original connection preserves crypto/tls and net/http protocol
// identity. Run releases one reservation at net/http's terminal owned state:
// Closed, or Hijacked when ownership transfers to the caller.
// capacity is never closed; only Accept acquires and terminal state/Accept
// failure releases. Closing closed wakes admission without another goroutine.
type connectionLimitListener struct {
	net.Listener
	capacity chan struct{}
	closed   chan struct{}
	once     sync.Once
}

func limitConnections(listener net.Listener, maximum int) *connectionLimitListener {
	return &connectionLimitListener{Listener: listener, capacity: make(chan struct{}, maximum), closed: make(chan struct{})}
}

func (listener *connectionLimitListener) Accept() (net.Conn, error) {
	select {
	case listener.capacity <- struct{}{}:
	case <-listener.closed:
		return nil, net.ErrClosed
	}
	connection, err := listener.Listener.Accept()
	if err != nil {
		listener.release()
		return nil, err
	}
	return connection, nil
}

func (listener *connectionLimitListener) release() { <-listener.capacity }

func (listener *connectionLimitListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return listener.Listener.Close()
}
