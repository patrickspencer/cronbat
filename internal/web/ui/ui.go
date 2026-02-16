package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

// Handler serves the embedded web UI assets.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
