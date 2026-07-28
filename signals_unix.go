//go:build unix

package service

import (
	"os"
	"syscall"
)

func defaultSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
