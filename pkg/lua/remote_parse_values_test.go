package lua

import (
	"strconv"
	"testing"
)

// The CLU's answer to clientRegister can end with a newline; the last value is still a
// number, not "20.50}".
func TestRemoteSubscribeAnswerWithTrailingNewline(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	for _, answer := range []string{`{1,0,20.50}`, "{1,0,20.50}\n", "{1,0,20.50}\r\n", " {1,0,20.50} "} {
		got := mustExec(t, engine, `(function()
			__go_send_command = function(ip, script) return "clientReport:1:" .. `+strconv.Quote(answer)+` end
			local _, values = RemoteSubscription.subscribe("192.168.0.3", {
				{ objId = "THE1", featureIndex = 6 }, { objId = "THE1", featureIndex = 8 }, { objId = "THE1", featureIndex = 3 },
			}, "CLU1")
			local out = {}
			for i, v in ipairs(values) do out[i] = type(v) .. ":" .. tostring(v) end
			return table.concat(out, ",")
		end)()`)
		if want := "number:1,number:0,number:20.5"; got != want {
			t.Errorf("answer %q: values = %s, want %s", answer, got, want)
		}
	}
}
