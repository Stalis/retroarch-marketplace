---
name: find-canonical-name
description: Look up likely Libretro canonical names for a game without modifying the catalog database.
---

# Find a canonical name

1. Establish the game title or file and the exact platform `libretro_name` from the user, the database, and `internal/store/known-platforms.json` as applicable. Ask if they cannot be established.
2. From the project root, run `go run . find-name -database <db> -platform 'Sony - PlayStation' -q "King's Field"`, replacing the flag values with the established database, platform, and title. To match a file name instead, use `-file '<path>'` in place of `-q`. BoxArt listings are cached in the database for 7 days.
3. Treat the ranked candidates as suggestions, not verified release identities. Compare region, edition, revision, and disc evidence from authorized files or user input. Ask the user when identity is ambiguous; never invent release facts or silently assume the first candidate is correct.
4. Report the exact chosen candidate and relevant uncertainty. If the lookup returns an empty array or fails, report that fact; do not fabricate a verified name.

This skill never edits the database's games. Preserve existing canonical names unless the user explicitly requests changes through a separate workflow.
