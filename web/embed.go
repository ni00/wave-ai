// Package web embeds the console built by npm run build.
package web

import "embed"

// Include the tracked .gitkeep so Go tooling works before the frontend is built.
//
//go:embed all:dist/compressed
var assets embed.FS

// ReadAsset reads a gzip-compressed console asset relative to the build root.
func ReadAsset(name string) ([]byte, error) {
	return assets.ReadFile("dist/compressed/" + name)
}
