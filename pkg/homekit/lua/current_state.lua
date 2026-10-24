-- The state the Home app shows: the position for covers, the value otherwise;
-- "nil" when unknown. server.go appends return currentState(path).
local function currentState(path)
	local h = ExposedObjects:get(path)
	if not h then return "nil" end
	local o, v = h.object, nil
	if h.type == "cover" and type(o) == "table" and type(o.getPosition) == "function" then
		v = o:getPosition()
	else
		v = h:getValue()
	end
	if type(v) == "boolean" then v = v and 1 or 0 end
	if type(v) ~= "number" then return "nil" end
	return tostring(v)
end
