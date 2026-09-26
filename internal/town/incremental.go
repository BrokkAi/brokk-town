package town

import "time"

// Force a full inventory at least once a day, after a restart with no baseline,
// or on a clock discontinuity. Failed reads never advance either cursor.
func inventoryCursor(t *Town, now time.Time) *time.Time {
	if t.SyncBranch != t.Branch() || !t.Initialized || t.LastSync.IsZero() || t.LastFullSync.IsZero() || !t.LastSync.Before(now) || t.LastFullSync.After(now) || now.Sub(t.LastFullSync) >= 24*time.Hour {
		return nil
	}
	since := t.LastSync
	return &since
}
