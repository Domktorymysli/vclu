//go:build !linux

package calendar

func kernelSynced() (synced, ok bool) { return false, false }
