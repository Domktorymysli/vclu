package lua

import (
	"strings"
	"testing"
)

// EventBus:on warns when callbacks for one event pile up: once at 100, again at 200, 400...
// Every callback is still added and runs.
func TestEventBusWarnsOnCallbackPileup(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	got, err := engine.Execute(`(function()
		local warns = {}
		local origWarn = Logger.warn
		Logger.warn = function(_, msg, ...) warns[#warns + 1] = Logger:format(msg, ...) end

		local bus = EventBus:new()
		local c = { runs = 0 }
		local cb = function() c.runs = c.runs + 1 end
		local counts = {}
		for i = 1, 400 do
			bus:on("pileup", cb)
			if i == 99 or i == 100 or i == 199 or i == 200 or i == 399 or i == 400 then
				counts[#counts + 1] = i .. ":" .. #warns
			end
		end
		bus:on("other", cb)
		bus:emit("pileup")

		-- Back to 100 and up to 400 again crosses 200 and 400 without a warning: the next mark is 800.
		for i = 1, 300 do bus:off("pileup", cb) end
		counts[#counts + 1] = "left:" .. #bus.callbacks["pileup"]
		for i = 1, 300 do bus:on("pileup", cb) end
		counts[#counts + 1] = "readd:" .. #bus.callbacks["pileup"] .. ":" .. #warns

		Logger.warn = origWarn
		return table.concat(counts, ",") .. "|" .. c.runs .. "|" .. table.concat(warns, "\n")
	end)()`)
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	parts := strings.SplitN(got, "|", 3)
	if len(parts) != 3 {
		t.Fatalf("got %q", got)
	}
	// No warning below 100, one at 100, the next at 200 and 400.
	if parts[0] != "99:0,100:1,199:1,200:2,399:2,400:3,left:100,readd:400:3" {
		t.Errorf("warnings by count = %q, want 99:0,100:1,199:1,200:2,399:2,400:3,left:100,readd:400:3", parts[0])
	}
	if parts[1] != "400" {
		t.Errorf("emit ran %s callbacks, want all 400", parts[1])
	}
	warns := strings.Split(parts[2], "\n")
	for i, want := range []string{"100", "200", "400"} {
		if i >= len(warns) || !strings.Contains(warns[i], "'pileup'") || !strings.Contains(warns[i], want) {
			t.Errorf("warning %d = %q, want the event name 'pileup' and the count %s", i+1, strings.Join(warns, " / "), want)
		}
	}
}
