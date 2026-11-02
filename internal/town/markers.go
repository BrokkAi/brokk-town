package town

import "strings"

// Town tags what it writes to GitHub and Slack with a hidden HTML comment so a
// retry finds the earlier write instead of repeating it.
const markerPrefix = "<!-- brokk-town"

// hasMarker reports whether text carries marker.
func hasMarker(text, marker string) bool {
	return strings.Contains(text, marker)
}
