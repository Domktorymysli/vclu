package lua

import (
	"path/filepath"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/notify"
)

// ModuleNotices turns each module load report into the bell's states (SetModuleLoadHandler);
// the error loses the directory, as the editor shows it.
func ModuleNotices(n notify.Notifier, modulesDir string) func([]ModuleLoadResult) {
	prefix := modulesDir + string(filepath.Separator)
	if abs, err := filepath.Abs(modulesDir); err == nil {
		prefix = abs + string(filepath.Separator)
	}
	return func(results []ModuleLoadResult) {
		failed := map[string]string{}
		for _, r := range results {
			if !r.OK {
				failed[r.Name] = strings.ReplaceAll(r.Error, prefix, "modules/")
			}
		}
		notify.ModuleLoad(n, failed)
	}
}
