package notify

import "strings"

// Messages: the panel translates Msg (web/locales, "notify.*") with Params.
const (
	MsgUpdate        = "notify.update.available"   // version
	MsgPluginUpdates = "notify.plugin.updates"     // count, plugins (up to 3 names, "+N"), updates (JSON)
	MsgPluginPerms   = "notify.plugin.permissions" // count, plugins (up to 3 ids, "+N")
	MsgModule        = "notify.module.failed"      // module
	MsgLog           = "notify.log.error"          // source
	MsgLogin         = "notify.login.failed"       // ip
	MsgLockLogin     = "notify.lock.login"         // ip, until
	MsgLockPIN       = "notify.lock.pin"           // dashboard, until
	MsgLockToken     = "notify.lock.token"         // ip
	MsgLockDevice    = "notify.lock.device"        // device, until
	MsgSuppressed    = "notify.suppressed"         // count, source
)

// english are the title and body of each message, for a client without the panel's texts.
var english = map[string][2]string{
	MsgUpdate:        {"vclu {version} is available", "A new version of vclu is out."},
	MsgPluginUpdates: {"Plugin updates: {count} ({plugins})", "Newer versions of installed plugins are out."},
	MsgPluginPerms:   {"Plugins waiting for permissions: {count} ({plugins})", "An update asks for more than you allowed; it runs without it until you allow it on the plugins page."},
	MsgModule:        {"Module {module} did not load", ""},
	MsgLog:           {"Error in the log: {source}", ""},
	MsgLogin:         {"Failed login to the panel", "From {ip}."},
	MsgLockLogin:     {"Panel login locked for {ip}", "Too many failed logins, locked until {until}."},
	MsgLockPIN:       {"PIN of dashboard {dashboard} locked", "Too many wrong PINs, locked until {until}."},
	MsgLockDevice:    {"PIN of {device} locked", "Too many wrong PINs from the app, locked until {until}."},
	MsgLockToken:     {"Invalid tokens from {ip}", "Too many invalid tokens of the device API, answered with 429."},
	MsgSuppressed:    {"{count} notifications skipped", "Too many from {source} in an hour."},
}

func render(msg string, params map[string]string, title bool) string {
	t, ok := english[msg]
	if !ok {
		return ""
	}
	s := t[1]
	if title {
		s = t[0]
	}
	for k, v := range params {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}
