//go:build release

package main

import (
	"embed"
	"io/fs"

	"retroarch-marketplace/internal/catalog"
)

//go:embed all:web/dist
var distEmbed embed.FS

func init() {
	if sub, err := fs.Sub(distEmbed, "web/dist"); err == nil {
		catalog.AdminFS = sub
	}
}
