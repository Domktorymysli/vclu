-- A write from the Home app; accessory_factory.go appends
-- return command(ExposedObjects:get(path), function(h) <call> end), the call using h and hk.
local hk = {}

-- a colour picked while the light was off goes out before it lights (SwitchOn alone
-- would bring back the old one)
local function sendPickedColor(h)
	local c = h._hkColor
	if c then
		h._hkColor = nil
		h.object:setColor(c[1], c[2], c[3])
	end
end

-- a dimmer keeps its last brightness when switched on or off
function hk.switchOn(h)
	sendPickedColor(h)
	if h.object.switchOn then h.object:switchOn() else h:setValue(1) end
end

function hk.switchOff(h)
	if h.object.switchOff then h.object:switchOff() else h:setValue(0) end
end

function hk.setBrightness(h, pct)
	if pct > 0 then sendPickedColor(h) end
	if h.object.setBrightness then h.object:setBrightness(pct) else h:setValue(pct / 100) end
end

-- A colour (brightest channel 255) keeps the brightness; a light that is off keeps
-- it until it is switched on from the Home app, so a colour alone does not light it
function hk.setColor(h, r, g, b)
	if h.options.readonly then error("read-only") end
	local v = tonumber(h:getValue())
	if v and v > 0 then
		h._hkColor = nil
		h.object:setColor(r, g, b)
	else
		h._hkColor = { r, g, b }
	end
end

-- A Grenton roller without a position (class 11, or a proxy not regenerated) has
-- no SetPosition: it opens or closes, as a tap in the Home app does
function hk.setPosition(h, target)
	local o = h.object
	if o._hasPosition == false and o.open then
		if target >= 50 then o:open() else o:close() end
	elseif o.setPosition then
		o:setPosition(target)
	else
		h:setValue(target)
	end
end

local function command(h, call)
	if not h then return "missing" end
	call(h)
	return "ok"
end
