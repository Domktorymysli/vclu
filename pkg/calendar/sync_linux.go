package calendar

import "golang.org/x/sys/unix"

// kernelSynced reports whether the kernel considers the clock synchronised (NTP cleared
// STA_UNSYNC, as `timedatectl` shows it). ok is false when the kernel cannot be asked.
func kernelSynced() (synced, ok bool) {
	var tx unix.Timex // modes 0: read only, no privileges needed
	if _, err := unix.Adjtimex(&tx); err != nil {
		return false, false
	}
	return tx.Status&unix.STA_UNSYNC == 0, true
}
