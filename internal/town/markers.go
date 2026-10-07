package town

import "strings"

// Town tags what it writes to GitHub and Slack with a hidden HTML comment so a
// retry finds the earlier write instead of repeating it. Before the rename to
// SlopCop Squad those comments began "<!-- brokk-town", and issues, comments and
// replies written then still do, so every lookup for an earlier write accepts
// either spelling. Town only writes the current one.
const (
	markerPrefix       = "<!-- slopcop-squad"
	legacyMarkerPrefix = "<!-- brokk-town"
)

// hasMarker reports whether text carries marker in its current spelling or the
// one Town wrote before the rename.
func hasMarker(text, marker string) bool {
	return strings.Contains(text, marker) || strings.Contains(text, legacyMarker(marker))
}

// legacyMarker spells marker the way Town wrote it before the rename.
func legacyMarker(marker string) string {
	return strings.Replace(marker, markerPrefix, legacyMarkerPrefix, 1)
}
