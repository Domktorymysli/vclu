package lua

import "testing"

// lib/telnet on bytes given in pieces: options refused once each, commands and
// subnegotiation dropped also when a read splits them, lines and a prompt joined.
func TestTelnetLibBytes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		local Telnet = require("lib/telnet")
		local sent = {}
		local conn = { closed = false }
		function conn:send(b) sent[#sent + 1] = b return true end
		function conn:onReceive(fn) self.recv = fn end
		function conn:close() self.closed = true end
		local function hex(s) return (s:gsub(".", function(c) return string.format("%02x ", c:byte()) end)) end
		local function eq(label, got, want)
			if got ~= want then error(label .. ": got " .. tostring(got) .. ", want " .. tostring(want), 2) end
		end

		local t = Telnet.wrap(conn, { newline = "\n" })
		local log = {}
		t:onLine(function(line) log[#log + 1] = "L:" .. line end)
		t:onPrompt("Password: $", function(text) log[#log + 1] = "P:" .. text end)

		-- DO ECHO split after IAC and after DO; WILL SGA whole; DONT and WONT need no answer
		conn.recv("\255") conn.recv("\253") conn.recv("\1hel")
		conn.recv("lo\255\251\3 wor") conn.recv("ld\r\n\255\254\1\255\252\1")
		eq("answers", hex(table.concat(sent)), "ff fc 01 ff fe 03 ")
		-- a subnegotiation with IAC IAC inside, split, then text, IAC IAC as 255, CR NUL
		conn.recv("a\255\250\24\1\255\255x") conn.recv("y\255") conn.recv("\240b\255\255c\r\0d\n")
		-- NOP and GA dropped; a line split in the middle; a prompt without a line end
		conn.recv("pa\255\241rt") conn.recv("ial\nPass") conn.recv("word: ")
		eq("log", table.concat(log, "|"), "L:hello world|L:ab\255c\rd|L:partial|P:Password: ")

		sent = {}
		t:send("ok\255")
		t:sendRaw("\255\244")
		eq("sent", hex(table.concat(sent)), "6f 6b ff ff 0a ff f4 ")
		t:close()
		eq("closed", conn.closed, true)
	`)
}

// A device that never ends a line: past MAX_LINE the text goes out as a line; a bad
// prompt pattern is refused when it is set, not on every read.
func TestTelnetLibLimits(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		local Telnet = require("lib/telnet")
		local conn = {}
		function conn:send(b) return true end
		function conn:onReceive(fn) self.recv = fn end
		function conn:close() end
		local t = Telnet.wrap(conn)
		local lines = {}
		t:onLine(function(line) lines[#lines + 1] = #line end)
		local chunk = string.rep("x", 16 * 1024)
		for _ = 1, 4 do conn.recv(chunk) end
		assert(#lines == 0, "a line at the limit waits")
		conn.recv("y")
		assert(#lines == 1 and lines[1] == Telnet.MAX_LINE + 1, "over the limit: " .. tostring(lines[1]))
		conn.recv("end\n")
		assert(lines[2] == 3, "next line: " .. tostring(lines[2]))

		local ok, err = pcall(function() t:onPrompt("login[", function() end) end)
		assert(not ok and tostring(err):find("bad pattern"), tostring(err))
		conn.recv("login: ")
	`)
}
