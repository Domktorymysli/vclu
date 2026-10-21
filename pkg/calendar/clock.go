package calendar

import "time"

// MinYear: a clock before it has not been set yet (a Raspberry Pi without RTC boots in 1970
// or at the time it was switched off).
const MinYear = 2025

// ClockTrusted reports whether now can be planned from: the kernel says NTP synchronised the
// clock or, where it cannot be asked (macOS, some containers), the year is plausible.
// reason says why not.
func ClockTrusted(now time.Time) (trusted bool, reason string) {
	if now.Year() < MinYear {
		return false, "clock is before 2025, not set yet"
	}
	synced, ok := kernelSynced()
	if ok && !synced {
		return false, "the kernel reports the clock as not synchronised (NTP)"
	}
	return true, ""
}
