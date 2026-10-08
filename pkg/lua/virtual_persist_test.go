package lua

import "testing"

func TestVirtual_InitialAndPersist(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		local mode = VirtualSwitch:new("tryb_nocny", { initial = 1, persist = true })
		assert(mode:getValue() == 1, "initial")
		mode:setValue(0)
		local kwh = VirtualNumber:new("energia_klima", { unit = "kWh", initial = 0, persist = "energia.klima" })
		kwh:setValue(214.5)
		local label = VirtualText:new("status", { initial = "OK", persist = true })
		label:setValue("ALARM")
		local plain = VirtualSwitch:new("plain", { initial = 1 })
		plain:setValue(0)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `
		local c = { events = 0 }
		StateBus:watch("*", function() c.events = c.events + 1 end)
		local mode = VirtualSwitch:new("tryb_nocny", { initial = 1, persist = true })
		assert(mode:getValue() == 0, "persisted switch: " .. tostring(mode:getValue()))
		local kwh = VirtualNumber:new("energia_klima", { unit = "kWh", initial = 0, persist = "energia.klima" })
		assert(kwh:getValue() == 214.5, "persisted number: " .. tostring(kwh:getValue()))
		local label = VirtualText:new("status", { initial = "OK", persist = true })
		assert(label:getValue() == "ALARM", "persisted text")
		local plain = VirtualSwitch:new("plain", { initial = 1 })
		assert(plain:getValue() == 1, "without persist the initial value applies")
		assert(c.events == 0, "restoring a value is not a state change")
	`)
}

func TestVirtual_PersistGuards(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		local n = VirtualNumber:new("limited", { min = 0, max = 10, persist = true })
		n:setValue(8)
		persist("virtual-objects"):set("limited", 50)       -- e.g. the bounds were lowered
		persist("virtual-objects"):set("kind", "text")     -- the object changed kind
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `
		local n = VirtualNumber:new("limited", { min = 0, max = 10, persist = true })
		assert(n:getValue() == 10, "restored value must be clamped: " .. tostring(n:getValue()))
		local k = VirtualSwitch:new("kind", { initial = 0, persist = true })
		assert(k:getValue() == 0, "a value of another type must be ignored")
		assert(not pcall(VirtualSwitch.new, VirtualSwitch, "x", { persist = "" }), "empty key accepted")
		local s = VirtualSwitch:new("x")
		assert(s:getValue() == 0, "old constructor call still works")
	`)
}

func TestVirtual_PersistRejectsBadValuesAndKeys(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		local a = VirtualNumber:new("good", { persist = true })
		local b = VirtualNumber:new("bad", { persist = true })
		a:setValue(5)
		b:setValue(0/0)                       -- NaN from a broken reading
		a:setValue(6)
		persist("virtual-objects"):save()
		assert(Persist.errors["module:virtual-objects"] == nil, "NaN blocked the shared namespace")
		assert(not pcall(VirtualSwitch.new, VirtualSwitch, nil, { persist = true }), "unnamed object accepted")
		assert(not pcall(VirtualSwitch.new, VirtualSwitch, "x", { persist = "save" }), "reserved key accepted")
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `
		assert(VirtualNumber:new("good", { persist = true }):getValue() == 6, "good value lost")
	`)
}
