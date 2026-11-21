package notify

// Sources' ids.
const (
	SourceUpdate = "update"
	SourcePlugin = "plugin"
	SourceModule = "module"
	SourceLog    = "log"
	SourceLogin  = "login"
	SourceLock   = "lock"
)

// SourceInfo is what adds entries; its label is the panel's "notify.source.<id>".
type SourceInfo struct {
	ID        string
	DefaultOn bool // on until the user turns it off
	ToLua     bool // Notifications.on handlers get its entries
}

// Sources are every source, in the order the settings list them. A new one is added here,
// with its messages (messages.go) and its label in web/locales.
var Sources = []SourceInfo{
	{ID: SourceUpdate, DefaultOn: true, ToLua: true},
	{ID: SourcePlugin, DefaultOn: true, ToLua: true},
	{ID: SourceModule, DefaultOn: true, ToLua: true},
	{ID: SourceLog, DefaultOn: false}, // some installations log many errors; a handler's own would loop
	{ID: SourceLogin, DefaultOn: true, ToLua: true},
	{ID: SourceLock, DefaultOn: true, ToLua: true},
}

// Lookup is the source with id.
func Lookup(id string) (SourceInfo, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return SourceInfo{}, false
}

// LuaSources are the ids Notifications.on handlers get.
func LuaSources() []string {
	var out []string
	for _, s := range Sources {
		if s.ToLua {
			out = append(out, s.ID)
		}
	}
	return out
}

// Notifier is what a source needs of the store.
type Notifier interface {
	Add(Entry)
	Resolve(func(Entry) bool)
}
