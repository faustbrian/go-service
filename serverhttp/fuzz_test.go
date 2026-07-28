package serverhttp_test

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

type fuzzAddress string

func (address fuzzAddress) Network() string { return "fuzz" }
func (address fuzzAddress) String() string  { return string(address) }

type fuzzListener struct{}

func (fuzzListener) Accept() (net.Conn, error) { return nil, errors.New("unused") }
func (fuzzListener) Close() error              { return nil }
func (fuzzListener) Addr() net.Addr            { return fuzzAddress("fuzz") }

func FuzzOptions(fuzz *testing.F) {
	fuzz.Add(int64(0), int64(0), 4096)
	fuzz.Add(int64(-1), int64(-1), 0)
	fuzz.Add(int64(time.Second), int64(1024), 1<<20)

	fuzz.Fuzz(func(t *testing.T, timeout int64, bodyLimit int64, headerLimit int) {
		_, _ = serverhttp.New(
			fuzzListener{},
			http.NotFoundHandler(),
			serverhttp.WithReadTimeout(time.Duration(timeout)),
			serverhttp.WithBodyLimit(bodyLimit),
			serverhttp.WithMaxHeaderBytes(headerLimit),
		)
	})
}
