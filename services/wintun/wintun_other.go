//go:build !windows

package wintun

import "net/http"

func Prepare(_ string, _ *http.Client, _ func(string)) error { return nil }
