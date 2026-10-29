-- The colour the Home app shows for a colour light: "r,g,b", or "nil" when unknown.
-- A colour picked while the light was off waits on the handle (hk.setColor) until it is on.
-- color.go appends return currentColor(path).
local function currentColor(path)
	local h = ExposedObjects:get(path)
	if not h or not ExposedObjects.hasColor(h) then return "nil" end
	local c = h._hkColor
	if c then
		local v = tonumber(h:getValue())
		if not v or v <= 0 then return string.format("%d,%d,%d", c[1], c[2], c[3]) end
		h._hkColor = nil -- switched on elsewhere: the light's own colour counts
	end
	local r, g, b = h.object:getColor()
	if type(r) ~= "number" then return "nil" end
	return string.format("%d,%d,%d", math.floor(r + 0.5), math.floor(g + 0.5), math.floor(b + 0.5))
end
