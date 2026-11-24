// Package web embeds the dashboard so the panel ships as one binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed ui
var content embed.FS

// UI returns the dashboard files rooted at index.html.
func UI() fs.FS {
	sub, err := fs.Sub(content, "ui")
	if err != nil {
		panic(err) // the embed directive guarantees ui/ exists
	}
	return sub
}
