package store

import (
	_ "embed"
	"encoding/json"

	"retroarch-marketplace/internal/content"
)

//go:embed known-platforms.json
var knownPlatformsJSON []byte

// KnownPlatforms returns the built-in Libretro platform list in display order.
func KnownPlatforms() []content.Platform {
	var platforms []content.Platform
	if err := json.Unmarshal(knownPlatformsJSON, &platforms); err != nil {
		panic("known-platforms.json: " + err.Error())
	}
	return platforms
}
