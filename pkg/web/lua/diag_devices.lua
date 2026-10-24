-- Exposed objects for the diagnostic archive (devices.json)
local list = {}
if ExposedObjects and ExposedObjects.list then
	for _, obj in ipairs(ExposedObjects:list()) do
		table.insert(list, {
			path = obj.path or "",
			type = obj.type or "",
			name = obj.name or "",
			area = obj.area or "",
			description = obj.description or ""
		})
	end
end
return require("json").encode(list)
