package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// Module names of an id without a namespace meet a namespaced one too; ids stay lower case, so
// a case-insensitive file system adds no collisions.
func TestReview_ModuleNamesWithoutNamespace(t *testing.T) {
	if other := ModuleNameTaken("a__b", []string{"@a/b"}); other != "@a/b" {
		t.Errorf("a__b next to @a/b: %q, want @a/b", other)
	}
	if other := ModuleNameTaken("@a/b-c", []string{"@a/b_c", "@a/b.d"}); other != "" {
		t.Errorf("@a/b-c: %q, want none", other)
	}
	for _, id := range []string{"@A/b", "@a/B", "@a/b c"} {
		if ValidPluginID(id) {
			t.Errorf("%s is a valid id", id)
		}
	}
}

// Addresses written differently only by case, a trailing / or .git are one repository; the
// scheme, a query or credentials make another one (refused, never wider).
func TestReview_RepoAddressForms(t *testing.T) {
	base := "https://github.com/Jan/plugins"
	for _, same := range []string{"https://github.com/jan/plugins/", "HTTPS://GITHUB.COM/jan/plugins.git", " https://github.com/jan/plugins.git/ "} {
		if !sameRepoURL(base, same) {
			t.Errorf("%s is not %s", same, base)
		}
	}
	for _, other := range []string{"http://github.com/jan/plugins", "https://github.com/jan/plugins?x=1", "https://u:p@github.com/jan/plugins", "https://github.com/jan/plugins2", "https://github.com/jan/plugins//"} {
		if sameRepoURL(base, other) {
			t.Errorf("%s is %s", other, base)
		}
	}
}

// A marker with the tag only (written by 1.12) names no address: the entry is pinned on load.
func TestReview_TagOnlyMarkerNamesNoAddress(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/cloud", Repository: "test"}})
	writePluginFiles(t, l, "@jan/cloud", "1.0.0", `{"id":"cloud","version":"1.0.0","api":2}`)
	marker := filepath.Join(l.pluginsDir, "jan--cloud", "1.0.0", ".complete")
	if err := os.WriteFile(marker, []byte("cloud-v1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if from := l.downloadedFrom("@jan/cloud"); from != "" {
		t.Errorf("tag-only marker names %q", from)
	}
	if err := os.WriteFile(marker, []byte(completeStamp("cloud-v1.0.0", "https://a.example/R.git")), 0o644); err != nil {
		t.Fatal(err)
	}
	if from := l.downloadedFrom("@jan/cloud"); !strings.HasSuffix(from, "a.example/r") {
		t.Errorf("marker names %q", from)
	}
}

// An official plugin installed by 1.12 is pinned to the official address on load and still
// updates from the official repository.
func TestReview_OfficialEntryFrom112(t *testing.T) {
	repos := config.DefaultPluginsConfig().Repositories
	l := permissionsLoader(t, repos, []config.InstalledPlugin{{ID: "@vclu/weather", Version: "1.0.0", Repository: repos[0].Name}})
	writePluginFiles(t, l, "@vclu/weather", "1.0.0", `{"id":"weather","version":"1.0.0","api":2}`)
	if err := os.WriteFile(filepath.Join(l.pluginsDir, "vclu--weather", "1.0.0", ".complete"), []byte("weather-v1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	mc, _ := config.ReadMasterConfig(l.serverDir)
	l.pinOlderEntries(mc)
	inst, _ := l.installedEntry("@vclu/weather")
	if inst.RepositoryURL != normRepoURL(config.OfficialPluginsRepoURL) || inst.API != 2 {
		t.Fatalf("pinned %+v", inst)
	}
	if ok, moved := l.FromSource(inst, repos[0]); !ok || moved {
		t.Errorf("official update refused: ok %v moved %v", ok, moved)
	}
	if !l.isOfficial("@vclu/weather") {
		t.Error("no longer official")
	}
}
