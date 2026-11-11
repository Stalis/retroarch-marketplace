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

func TestRankWithMaxDistance(t *testing.T) {
	names := []string{"Darkstone (Australia)", "Darkstones (USA)", "Dark Seed"}
	for _, tc := range []struct {
		max  int
		want []string
	}{
		{0, names[:1]},
		{1, names[:2]},
	} {
		if got := rank(names, "DarkStone (Europe)", &tc.max); !slices.Equal(got, tc.want) {
			t.Errorf("max %d: got %v, want %v", tc.max, got, tc.want)
		}
	}
	zero := 0
	if got := rank(names, "Other", &zero); got == nil || len(got) != 0 {
		t.Errorf("expected an empty result, got %#v", got)
	}
}
