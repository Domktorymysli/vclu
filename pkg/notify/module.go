package notify

import (
	"net/url"
	"sort"
)

// ModulesDir is the name a load report gives an error of the modules/ directory itself.
const ModulesDir = "modules/"

// ModuleLoad records a module load report: failed maps a file in modules/ to its error, and
// the states of every other module are resolved.
func ModuleLoad(n Notifier, failed map[string]string) {
	n.Resolve(func(e Entry) bool {
		_, still := failed[e.Params["module"]]
		return e.Source == SourceModule && e.Msg == MsgModule && !still
	})
	names := make([]string, 0, len(failed))
	for name := range failed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		key, link := "module:"+name, "/editor?file=modules/"+url.QueryEscape(name)
		if name == ModulesDir {
			key, link = "module:dir", "/editor"
		}
		n.Add(Entry{
			Kind: KindState, Source: SourceModule, Level: LevelError, Key: key,
			Msg: MsgModule, Params: map[string]string{"module": name}, Detail: failed[name], Link: link,
		})
	}
}
