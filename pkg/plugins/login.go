package plugins

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Domktorymysli/vclu/pkg/luastr"
)

// LoginState is a sign-in as the panel sees it; the secrets never leave vclu.
type LoginState struct {
	State string `json:"state"` // pending, done, error, gone
	QR    string `json:"qr,omitempty"`
	Code  string `json:"code,omitempty"`
	URL   string `json:"url,omitempty"`
	Hint  string `json:"hint,omitempty"`
	Error string `json:"error,omitempty"`
}

// ErrLoginRunning: the plugin is signing in already.
var ErrLoginRunning = errors.New("a sign-in is already running")

// HasLogin: the running plugin signs in from the wizard (plugin:login).
func (l *Loader) HasLogin(pluginID string) bool {
	if !l.IsLoaded(pluginID) {
		return false
	}
	out, err := l.luaExecutor.Execute(fmt.Sprintf(`return PluginSandbox:hasLogin(%s)`, luastr.Quote(pluginID)))
	return err == nil && out == "true"
}

// LoginStart starts the plugin's sign-in and returns its id for LoginPoll and LoginCancel.
func (l *Loader) LoginStart(pluginID string) (string, error) {
	if !l.IsLoaded(pluginID) {
		return "", fmt.Errorf("the plugin is not running: save its settings first")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	req := hex.EncodeToString(b)
	out, err := l.executeAsPlugin(pluginID, fmt.Sprintf(`return PluginSandbox:loginStart(%s, %s)`, luastr.Quote(pluginID), luastr.Quote(req)))
	if err != nil {
		// the sign-in may have started before fn failed: nobody could end it
		_ = l.LoginCancel(pluginID, req)
		return "", fmt.Errorf("login: %w", err)
	}
	if out == ErrLoginRunning.Error() {
		return "", ErrLoginRunning
	}
	if out != "" {
		return "", errors.New(out)
	}
	return req, nil
}

// LoginPoll returns the sign-in's state; when it is done, its secrets are saved and given
// to the running plugin.
func (l *Loader) LoginPoll(pluginID, req string) (LoginState, error) {
	out, err := l.luaExecutor.Execute(fmt.Sprintf(`return PluginSandbox:loginPoll(%s, %s)`, luastr.Quote(pluginID), luastr.Quote(req)))
	if err != nil {
		return LoginState{}, err
	}
	var r struct {
		State   string            `json:"state"`
		Show    json.RawMessage   `json:"show"`
		Err     string            `json:"err"`
		Secrets map[string]string `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return LoginState{}, fmt.Errorf("login: %w", err)
	}
	st := LoginState{State: r.State, Error: r.Err}
	if len(r.Show) > 0 && string(r.Show) != "[]" {
		_ = json.Unmarshal(r.Show, &st)
		st.State, st.Error = r.State, r.Err
	}
	if r.State != "done" {
		return st, nil
	}
	if err := l.saveLoginSecrets(pluginID, r.Secrets); err != nil {
		return LoginState{State: "error", Error: "the sign-in worked, but saving it failed: " + err.Error()}, nil
	}
	return LoginState{State: "done"}, nil
}

func (l *Loader) saveLoginSecrets(pluginID string, secrets map[string]string) error {
	if err := SetPluginSecrets(l.serverDir, pluginID, secrets); err != nil {
		return err
	}
	all, err := PluginSecrets(l.serverDir, pluginID)
	if err != nil {
		return err
	}
	j, _ := json.Marshal(all)
	_, err = l.luaExecutor.Execute(fmt.Sprintf(`PluginSandbox:setSecrets(%s, JSON:decode(%s))`, luastr.Quote(pluginID), luastr.Quote(string(j))))
	return err
}

// LoginCancel ends the sign-in; the plugin's later show and done change nothing.
func (l *Loader) LoginCancel(pluginID, req string) error {
	_, err := l.luaExecutor.Execute(fmt.Sprintf(`PluginSandbox:loginCancel(%s, %s)`, luastr.Quote(pluginID), luastr.Quote(req)))
	return err
}
