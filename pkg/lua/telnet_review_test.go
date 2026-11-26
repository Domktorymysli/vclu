package lua

import "testing"

// Review of lib/telnet limits: commands across reads at the line limit, and patterns that
// fail only on some text.
func TestReviewTelnetLimitEdges(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		local Telnet = require("lib/telnet")
		local conn = { sent = {} }
		function conn:send(b) self.sent[#self.sent + 1] = b return true end
		function conn:onReceive(fn) self.recv = fn end
		function conn:close() end
		local t = Telnet.wrap(conn)
		local lines = {}
		t:onLine(function(line) lines[#lines + 1] = line end)
		-- a DO split across reads right at the limit: answered, not in the text
		conn.recv(string.rep("x", Telnet.MAX_LINE - 1) .. "\255")
		conn.recv("\253\1z")
		assert(#lines == 0, "lines " .. #lines)
		assert(conn.sent[1] == "\255\252\1", "reply " .. tostring(conn.sent[1]))
		conn.recv("w")
		assert(#lines == 1 and #lines[1] == Telnet.MAX_LINE + 1 and not lines[1]:find("\255", 1, true), "cut " .. #lines)
		local ok = pcall(function() t:onPrompt("a%1", function() end) end)
		__badCapture = ok
	`)
	if got, _ := engine.Execute(`tostring(__badCapture)`); got == "true" {
		t.Log("a%1 passes the check at registration (gopher-lua compiles it); fails only on matching text")
	}
}
