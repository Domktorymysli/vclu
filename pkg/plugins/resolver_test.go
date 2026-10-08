package plugins

import (
	"testing"
)

func TestIsValidGitRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"weather-v1.0.0", true},
		{"v1.0.1", true},
		{"my-plugin-v2.3.4", true},
		{"release_1.0", true},
		{"a", true},
		{"", false},
		{"../../evil", false},
		{"refs/heads/dev", false},          // contains /
		{"hello world", false},             // space
		{".hidden", false},                 // starts with .
		{"-dash", false},                   // starts with -
		{"a\x00b", false},                  // null byte
		{string(make([]byte, 128)), false}, // too long
	}

	for _, tt := range tests {
		got := isValidGitRef(tt.ref)
		if got != tt.want {
			t.Errorf("isValidGitRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestResolveRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"", "main"},
		{"weather-v1.0.1", "weather-v1.0.1"},
		{"v1.0.0", "v1.0.0"},
		{"../../evil", "main"},
		{"refs/heads/dev", "main"},
	}

	for _, tt := range tests {
		got := resolveRef(tt.ref)
		if got != tt.want {
			t.Errorf("resolveRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestGitHubResolver_RawFileURL(t *testing.T) {
	r := &GitHubResolver{}

	tests := []struct {
		name string
		repo string
		ref  string
		path string
		want string
	}{
		{
			"with tag",
			"https://github.com/user/repo",
			"weather-v1.0.1",
			"plugins/weather/plugin.json",
			"https://raw.githubusercontent.com/user/repo/weather-v1.0.1/plugins/weather/plugin.json",
		},
		{
			"empty ref falls back to main",
			"https://github.com/user/repo",
			"",
			"plugins.json",
			"https://raw.githubusercontent.com/user/repo/main/plugins.json",
		},
		{
			"invalid ref falls back to main",
			"https://github.com/user/repo",
			"../../evil",
			"plugins.json",
			"https://raw.githubusercontent.com/user/repo/main/plugins.json",
		},
		{
			"trailing slash stripped",
			"https://github.com/user/repo/",
			"v1.0.0",
			"file.txt",
			"https://raw.githubusercontent.com/user/repo/v1.0.0/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.RawFileURL(tt.repo, tt.ref, tt.path)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestGitHubResolver_BrowseURL(t *testing.T) {
	r := &GitHubResolver{}

	tests := []struct {
		name string
		repo string
		ref  string
		path string
		want string
	}{
		{
			"with tag",
			"https://github.com/user/repo",
			"weather-v1.0.1",
			"plugins/weather",
			"https://github.com/user/repo/tree/weather-v1.0.1/plugins/weather",
		},
		{
			"empty ref",
			"https://github.com/user/repo",
			"",
			"plugins/weather",
			"https://github.com/user/repo/tree/main/plugins/weather",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.BrowseURL(tt.repo, tt.ref, tt.path)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestGenericResolver(t *testing.T) {
	r := &GenericResolver{}

	raw := r.RawFileURL("https://example.com/plugins", "v1.0.0", "weather/plugin.json")
	if raw != "https://example.com/plugins/weather/plugin.json" {
		t.Errorf("RawFileURL: got %s", raw)
	}

	browse := r.BrowseURL("https://example.com/plugins", "v1.0.0", "weather")
	if browse != "https://example.com/plugins/weather" {
		t.Errorf("BrowseURL: got %s", browse)
	}
}

func TestDefaultResolver(t *testing.T) {
	gh := DefaultResolver("https://github.com/user/repo")
	if _, ok := gh.(*GitHubResolver); !ok {
		t.Errorf("expected GitHubResolver for github.com URL, got %T", gh)
	}

	gen := DefaultResolver("https://example.com/plugins")
	if _, ok := gen.(*GenericResolver); !ok {
		t.Errorf("expected GenericResolver for non-github URL, got %T", gen)
	}
}
