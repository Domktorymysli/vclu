-- An injected event: its name and its data as JSON
local name, dataJSON = ...
local data = JSON:decode(dataJSON)
EventBus:getShared():emit(name, data)
