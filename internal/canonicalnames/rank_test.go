package canonicalnames

import (
	"slices"
	"testing"
)

func TestRankPrioritizesSameTitleEditions(t *testing.T) {
	const australia = "Darkstone (Australia)"
	const europe = "Darkstone (Europe) (En,Fr,De,Es,It)"
	const usa = "Darkstone (USA)"
	names := []string{"Darkstalkers", "Dark Seed", "Darkstone 2 (USA)", australia, europe, usa}

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"DarkStone", []string{usa, australia, europe}},
		{europe, []string{europe, australia, usa}},
	} {
		got := Rank(names, tc.query)
		if len(got) < len(tc.want) || !slices.Equal(got[:len(tc.want)], tc.want) {
			t.Errorf("Rank(%q) = %v, want prefix %v", tc.query, got, tc.want)
		}
	}
}
