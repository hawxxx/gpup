//go:build !cgo

package gpu

import "errors"

func newNative() (provider, error) { return nil, errors.New("NVML requires a cgo-enabled build") }
