package plugins

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func TestNamespaceAllowed(t *testing.T) {
	official := config.DefaultPluginsConfig().Repositories[0]
	fork := config.PluginRepository{Name: "fork", URL: "https://github.com/jan/vclu-plugins", Enabled: true}
	sameURL := config.PluginRepository{Name: "copy", URL: official.URL, Enabled: true} // not the default entry
	if !NamespaceAllowed("vclu", official) || NamespaceAllowed("vclu", fork) || NamespaceAllowed("vclu", sameURL) {
		t.Error("vclu: only the official repository")
	}
	if !NamespaceAllowed("jan", fork) || !NamespaceAllowed("jan", official) {
		t.Error("other namespaces from any repository")
	}
	err := NewLoader(t.TempDir()).DownloadPlugin(fork, "@vclu/tasmota")
	if !errors.Is(err, ErrReservedNamespace) {
		t.Errorf("download of @vclu from a fork: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"tasmota","version":"1.0.0","path":"p"}]}`))
	}))
	defer srv.Close()
	if _, err := NewLoader(t.TempDir()).PreviewPermissions(configRepo("fork", srv.URL), "tasmota"); !errors.Is(err, ErrReservedNamespace) {
		t.Errorf("preview of @vclu from a fork: %v", err)
	}
}
