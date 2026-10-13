-- A demo house: virtual objects only, no hardware.
local M = {}

M.livingCeiling = VirtualSwitch:new("living_ceiling", { initial = 1 })
M.livingLamp    = VirtualDimmer:new("living_lamp", { initial = 0.6 })
M.kitchen       = VirtualSwitch:new("kitchen_light", { initial = 0 })
M.hall          = VirtualSwitch:new("hall_light", { initial = 0 })
M.blinds        = VirtualCover:new("living_blinds", { initial = 40 })
M.motion        = VirtualSensor:new("hall_motion", { initial = 0 })
M.target        = VirtualNumber:new("target_temp", { min = 18, max = 24, step = 0.5, unit = "°C", initial = 21.5 })
M.mode          = VirtualText:new("house_mode", { initial = "Home" })

M.temps = { living = 21.4, bedroom = 19.8, office = 22.1, outside = 8.6 }

-- Hall light on motion, off 30 s after the last one
local lastMotion = 0
M.motion:onChange(function(v)
    if v == 1 then
        M.hall:switchOn()
        lastMotion = os.time()
        setTimeout(function()
            if os.time() - lastMotion >= 30 then M.hall:switchOff() end
        end, 30000)
    end
end)

-- Simulated sensors and a bit of life in the house
EventBus:getShared():setInterval(function()
    for k, v in pairs(M.temps) do
        M.temps[k] = math.floor((v + (math.random() - 0.5) * 0.4) * 10 + 0.5) / 10
    end
end, 20000)
EventBus:getShared():setInterval(function()
    M.motion:updateState(1)
    setTimeout(function() M.motion:updateState(0) end, 5000)
end, 90000)

for k in pairs(M.temps) do
    metric("temp:" .. k, function() return M.temps[k] end, { unit = "°C" })
end

expose(M.livingCeiling, "switch", { name = "Living room ceiling", path = "demo:living_ceiling", area = "Living room" })
expose(M.livingLamp, "dimmer", { name = "Living room lamp", path = "demo:living_lamp", area = "Living room" })
expose(M.kitchen, "switch", { name = "Kitchen", path = "demo:kitchen", area = "Kitchen" })
expose(M.blinds, "cover", { name = "Living room blinds", path = "demo:blinds", area = "Living room" })

expose(M.hall, "switch", { name = "Hall light", path = "demo:hall", area = "Hall" })
expose(M.motion, "binary_sensor", { name = "Hall motion", path = "demo:hall_motion", area = "Hall" })

return M
