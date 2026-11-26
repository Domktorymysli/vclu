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

// Off GitHub a path keeps its case: two repositories on one server differ by it.
func TestRepoAddressPathCase(t *testing.T) {
	if sameRepoURL("https://git.example.com/Jan/plugins", "https://git.example.com/jan/plugins") {
		t.Error("paths differing in case are one repository")
	}
	if !sameRepoURL("HTTPS://Git.Example.COM/Jan/plugins.git/", "https://git.example.com/Jan/plugins") {
		t.Error("scheme and host case make another repository")
	}
	if !sameRepoURL("https://GitHub.com/Jan/Plugins", "https://github.com/jan/plugins") {
		t.Error("GitHub owner/repo case makes another repository")
	}
	// recorded before paths kept their case: on GitHub still the same
	if !sameRepoURL("https://github.com/domktorymysli/vclu-plugins", config.OfficialPluginsRepoURL) {
		t.Error("an official address recorded in lower case no longer matches")
	}
}
