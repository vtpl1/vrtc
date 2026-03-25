package recorder

import "time"

const bytesPerGB = 1 << 30 // 1 GiB

// RetentionPolicy defines multi-tier retention rules.
type RetentionPolicy struct {
	ContinuousDays int     // keep all footage N days; 0 = no continuous tier
	MotionDays     int     // keep motion-tagged footage N days; 0 = disabled
	ObjectDays     int     // keep object-tagged footage N days; 0 = disabled
	MaxStorageGB   float64 // hard cap in GB; 0 = unlimited
	MinFreeGB      float64 // emergency disk threshold in GB; 0 = disabled
	DiskFreeBytes  int64   // current free bytes (set by caller from disk check)
}

// EvaluateRetention returns the subset of entries that should be deleted
// according to policy. Entries must be sorted ascending by StartTime.
func EvaluateRetention(
	entries []RecordingEntry,
	policy RetentionPolicy,
	now time.Time,
) []RecordingEntry {
	if len(entries) == 0 {
		return nil
	}

	deleted := make([]bool, len(entries))

	applyTimeTiers(entries, deleted, policy, now)
	applyStorageCap(entries, deleted, policy)
	applyDiskFreeCap(entries, deleted, policy)

	var result []RecordingEntry

	for i, e := range entries {
		if deleted[i] {
			result = append(result, e)
		}
	}

	return result
}

func applyTimeTiers(
	entries []RecordingEntry,
	deleted []bool,
	policy RetentionPolicy,
	now time.Time,
) {
	for i, e := range entries {
		cutoff, hasCutoff := tierCutoff(e, policy, now)
		if hasCutoff && e.StartTime.Before(cutoff) {
			deleted[i] = true
		}
	}
}

func applyStorageCap(entries []RecordingEntry, deleted []bool, policy RetentionPolicy) {
	if policy.MaxStorageGB <= 0 {
		return
	}

	maxBytes := int64(policy.MaxStorageGB * float64(bytesPerGB))

	var totalBytes int64

	for i, e := range entries {
		if !deleted[i] {
			totalBytes += e.SizeBytes
		}
	}

	for i, e := range entries {
		if totalBytes <= maxBytes {
			break
		}

		if deleted[i] {
			continue
		}

		deleted[i] = true
		totalBytes -= e.SizeBytes
	}
}

func applyDiskFreeCap(entries []RecordingEntry, deleted []bool, policy RetentionPolicy) {
	if policy.MinFreeGB <= 0 || policy.DiskFreeBytes <= 0 {
		return
	}

	minFreeBytes := int64(policy.MinFreeGB * float64(bytesPerGB))
	if policy.DiskFreeBytes >= minFreeBytes {
		return
	}

	deficit := minFreeBytes - policy.DiskFreeBytes

	// Account for bytes already scheduled for deletion.
	for i, e := range entries {
		if deleted[i] {
			deficit -= e.SizeBytes
		}
	}

	for i, e := range entries {
		if deficit <= 0 {
			break
		}

		if deleted[i] {
			continue
		}

		deleted[i] = true
		deficit -= e.SizeBytes
	}
}

// tierCutoff returns the time-based cutoff for an entry based on its tags.
func tierCutoff(
	e RecordingEntry,
	p RetentionPolicy,
	now time.Time,
) (cutoff time.Time, hasCutoff bool) {
	switch {
	case e.HasObjects && p.ObjectDays > 0:
		return now.AddDate(0, 0, -p.ObjectDays), true
	case e.HasMotion && p.MotionDays > 0:
		return now.AddDate(0, 0, -p.MotionDays), true
	case p.ContinuousDays > 0:
		return now.AddDate(0, 0, -p.ContinuousDays), true
	default:
		return time.Time{}, false
	}
}
