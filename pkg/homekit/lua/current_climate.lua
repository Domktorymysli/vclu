-- A thermostat as the Home app shows it: ExposedObjects._climateJSON, or "nil" when not
-- exposed as one. climate.go appends return currentClimate(path).
local function currentClimate(path)
	local h = ExposedObjects:get(path)
	if not h or h.type ~= "climate" then return "nil" end
	return ExposedObjects._climateJSON(h)
end
