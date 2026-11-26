package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func hexSum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// A repository whose index gives digests: the files must match them, all of them.
func TestDownloadChecksHashes(t *testing.T) {
	const manifest = `{"id":"h","version":"1.0.0","files":["init.lua"]}`
	const code = "return {}\n"
	good := map[string]string{"plugin.json": hexSum(manifest), "init.lua": hexSum(code)}
	for what, tc := range map[string]struct {
		sums map[string]string
		ok   bool
	}{
		"matching":          {good, true},
		"none given":        {nil, true},
		"a changed file":    {map[string]string{"plugin.json": hexSum(manifest), "init.lua": hexSum("x")}, false},
		"a file not listed": {map[string]string{"plugin.json": hexSum(manifest)}, false},
		"a file not there":  {map[string]string{"plugin.json": hexSum(manifest), "init.lua": hexSum(code), "form.json": hexSum("{}")}, false},
	} {
		t.Run(what, func(t *testing.T) {
			listing, _ := json.Marshal(map[string]any{"id": "h", "version": "1.0.0", "path": "plugins/h", "tag": "h-v1.0.0", "sha256": tc.sums})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/plugins.json":
					_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[` + string(listing) + `]}`))
				case "/plugins/h/plugin.json":
					_, _ = w.Write([]byte(manifest))
				case "/plugins/h/init.lua":
					_, _ = w.Write([]byte(code))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			err := NewLoader(t.TempDir()).DownloadPlugin(configRepo("test", srv.URL), "@jan/h")
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrHashMismatch)) {
				t.Errorf("download: %v", err)
			}
		})
	}
}

// The official repository always gives digests.
func TestOfficialIndexNeedsHashes(t *testing.T) {
	official := config.DefaultPluginsConfig().Repositories[0]
	if err := checkHashes(t.TempDir(), PluginListing{ID: "x"}, official); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("official without sha256: %v", err)
	}
}

// Files already downloaded for a tag are checked again: when the index names other digests
// (a tag moved after a compromise), the cached ones are not used.
func TestCachedTagCheckedAgainstHashes(t *testing.T) {
	const manifest = `{"id":"h","version":"1.0.0","files":["init.lua"]}`
	code := "return 1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			listing, _ := json.Marshal(map[string]any{"id": "h", "version": "1.0.0", "path": "plugins/h", "tag": "h-v1.0.0",
				"sha256": map[string]string{"plugin.json": hexSum(manifest), "init.lua": hexSum(code)}})
			_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[` + string(listing) + `]}`))
		case "/plugins/h/plugin.json":
			_, _ = w.Write([]byte(manifest))
		case "/plugins/h/init.lua":
			_, _ = w.Write([]byte(code))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	l := NewLoader(t.TempDir())
	if err := l.DownloadPlugin(configRepo("test", srv.URL), "@jan/h"); err != nil {
		t.Fatal(err)
	}
	code = "return 2\n"
	if err := l.DownloadPlugin(configRepo("test", srv.URL), "@jan/h"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(l.resolvePluginDir("@jan/h"), "init.lua"))
	if string(got) != code {
		t.Errorf("init.lua %q: the cached files were kept", got)
	}
}
