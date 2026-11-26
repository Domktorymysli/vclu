package plugins

import "sync"

// installMu serialises what changes which plugins are installed, from where and with which
// files: the panel's install and uninstall, and a reload fetching files. Taken before downloadMu.
var installMu sync.Mutex

// LockInstalls takes the install lock; the returned function releases it.
func LockInstalls() func() {
	installMu.Lock()
	return installMu.Unlock
}
