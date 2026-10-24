-- A write from the Home app; accessory_factory.go appends
-- return command(ExposedObjects:get(path), function(h) <call> end), the call using h and hk.
local hk = {}

-- a dimmer keeps its last brightness when switched on or off
function hk.switchOn(h)
	if h.object.switchOn then h.object:switchOn() else h:setValue(1) end
end

function hk.switchOff(h)
	if h.object.switchOff then h.object:switchOff() else h:setValue(0) end
end

function hk.setBrightness(h, pct)
	if h.object.setBrightness then h.object:setBrightness(pct) else h:setValue(pct / 100) end
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
