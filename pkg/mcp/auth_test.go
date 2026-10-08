package mcp

import (
	"net/http"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func TestVerifyTokenHash(t *testing.T) {
	key := "vclu_mcp_abc123"
	hash := config.HashAPIKey(key)

	if !verifyTokenHash(key, hash) {
		t.Fatal("valid token should verify")
	}
	if verifyTokenHash("wrong-key", hash) {
		t.Fatal("wrong token should not verify")
	}
	if verifyTokenHash("", hash) {
		t.Fatal("empty token should not verify")
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid", "Bearer my-token", "my-token"},
		{"no prefix", "Basic abc123", ""},
		{"empty", "", ""},
		{"bearer only", "Bearer ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/mcp", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got := extractBearerToken(r)
			if got != tt.want {
				t.Errorf("extractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

type mockConfigProvider struct {
	bots    []config.MCPBot
	devMode bool
	plugins []config.InstalledPlugin
}

func (m *mockConfigProvider) GetMCPBots() []config.MCPBot {
	return m.bots
}

func (m *mockConfigProvider) IsDevModeEnabled() bool {
	return m.devMode
}

func (m *mockConfigProvider) GetInstalledPlugins() []config.InstalledPlugin {
	return m.plugins
}

func TestValidateBearerToken(t *testing.T) {
	key := "vclu_mcp_test"
	hash := config.HashAPIKey(key)

	bot := config.MCPBot{
		ID:         "bot-test",
		Name:       "Test Bot",
		APIKeyHash: hash,
		Profiles:   []string{"full-access"},
	}

	t.Run("valid token", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		if !validateBearerToken(r, &mockConfigProvider{bots: []config.MCPBot{bot}}) {
			t.Fatal("expected true for valid token")
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer wrong")
		if validateBearerToken(r, &mockConfigProvider{bots: []config.MCPBot{bot}}) {
			t.Fatal("expected false for wrong token")
		}
	})

	t.Run("no bots configured", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		if validateBearerToken(r, &mockConfigProvider{bots: nil}) {
			t.Fatal("expected false when no bots configured")
		}
	})

	t.Run("no auth header", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/mcp", nil)
		if validateBearerToken(r, &mockConfigProvider{bots: []config.MCPBot{bot}}) {
			t.Fatal("expected false with no auth header")
		}
	})
}
