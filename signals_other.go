//go:build !unix

package service

import "os"

func defaultSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
