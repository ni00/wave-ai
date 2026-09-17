// Package web embeds the console built by npm run build.
package web

import "embed"

//go:embed dist/compressed
var assets embed.FS

// ReadAsset reads a gzip-compressed console asset relative to the build root.
func ReadAsset(name string) ([]byte, error) {
	return assets.ReadFile("dist/compressed/" + name)
}
