package plugins

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func TestFromRepositoryByAddress(t *testing.T) {
	inst := config.InstalledPlugin{ID: "@jan/cloud", Repository: "jan", RepositoryURL: RepoURL("https://github.com/jan/plugins")}
	cases := []struct {
		repo        config.PluginRepository
		from, moved bool
	}{
		{config.PluginRepository{Name: "jan", URL: "https://github.com/jan/plugins"}, true, false},
		{config.PluginRepository{Name: "jan", URL: "https://GitHub.com/jan/plugins.git/"}, true, false},
		{config.PluginRepository{Name: "renamed", URL: "https://github.com/jan/plugins"}, true, false},
		{config.PluginRepository{Name: "jan", URL: "https://github.com/eve/plugins"}, false, true},
		{config.PluginRepository{Name: "eve", URL: "https://github.com/eve/plugins"}, false, false},
	}
	for _, c := range cases {
		if got := FromRepository(inst, c.repo); got != c.from {
			t.Errorf("FromRepository(%s %s) = %v, want %v", c.repo.Name, c.repo.URL, got, c.from)
		}
		if got := SourceMoved(inst, c.repo); got != c.moved {
			t.Errorf("SourceMoved(%s %s) = %v, want %v", c.repo.Name, c.repo.URL, got, c.moved)
		}
	}
}
