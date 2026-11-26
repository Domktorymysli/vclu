package plugins

import (
	"encoding/json"
	"testing"
)

// The API decides how plugin.json is read, so it cannot be written twice or in another case.
func TestManifestAPIKeyOnce(t *testing.T) {
	for _, raw := range []string{
		`{"id":"x","version":"1.0.0","api":2,"permissions":{},"api":1}`,
		`{"id":"x","version":"1.0.0","api":1,"API":2,"permissions":{}}`,
		`{"id":"x","version":"1.0.0","Api":2,"permissions":{}}`,
		`{"name":"a","name":"b","id":"x","version":"1.0.0","api":2,"permissions":{},"api":1}`,
	} {
		var m PluginManifest
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var m PluginManifest
	if err := json.Unmarshal([]byte(`{"id":"x","version":"1.0.0","api":2,"permissions":{}}`), &m); err != nil {
		t.Error(err)
	}
}
