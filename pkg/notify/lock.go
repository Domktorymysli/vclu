package notify

import "time"

// DeviceLocked records that wrong PINs from the app (cloud or LAN) locked device path for wait.
func DeviceLocked(n Notifier, path string, wait time.Duration) {
	until := time.Now().Add(wait).UTC().Truncate(time.Second).Format(time.RFC3339)
	n.Add(Entry{
		Kind: KindEvent, Source: SourceLock, Level: LevelWarning, Key: "lock:device:" + path,
		Msg: MsgLockDevice, Params: map[string]string{"device": path, "until": until}, Link: "/settings",
	})
}
