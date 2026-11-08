---
name: find-canonical-name
description: Look up likely Libretro canonical names for a game without modifying a manifest.
---

# Find a canonical name

1. Establish the game title and exact platform `libretro_name` from the user, the manifest, and `web/src/platforms.js` as applicable. Ask if they cannot be established.
2. From the project root, run `go run ./cmd/find-canonical-name -platform 'Sony - PlayStation' -q "King's Field"`, replacing both flag values with the established platform and title.
3. Treat the ranked candidates as suggestions, not verified release identities. Compare region, edition, revision, and disc evidence from authorized files or user input. Ask the user when identity is ambiguous; never invent release facts or silently assume the first candidate is correct.
4. Report the exact chosen candidate and relevant uncertainty. If the lookup returns an empty array or fails, report that fact; do not fabricate a verified name.

This skill never edits a manifest. Preserve existing manifests and their canonical names unless the user explicitly requests changes through a separate workflow.
