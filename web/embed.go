// Package web contains the embedded GPUP dashboard.
package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var assets embed.FS

// FS returns the dashboard files rooted at dist.
func FS() fs.FS {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return root
}
