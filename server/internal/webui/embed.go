//go:build embedweb

// Package webui exposes the built web app when it is compiled into the binary.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built web app, or nil if this binary doesn't include it.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
