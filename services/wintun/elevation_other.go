//go:build !windows

package wintun

func RequestElevation(_ func() bool) (bool, error) { return false, nil }

func WaitForElevationParent() error { return nil }
