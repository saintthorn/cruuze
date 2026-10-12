//go:build !embedweb

// Package webui exposes the built web app when it is compiled into the binary.
package webui

import "io/fs"

// FS returns the built web app, or nil if this binary doesn't include it.
func FS() fs.FS { return nil }
