local d = require("demo")
if type(d) ~= "table" then return end

local function sw(o) return function() return o:getValue() == 1 end end
local function set(o) return function(v) if v then o:switchOn() else o:switchOff() end end end

Dashboard.new('home')
  :name('Home')
  :icon('house-door')
  :columns(3)
  :auth('public')
  :poll(3)
  :header()
    :status('out', 'Outside', 'thermometer-half', function() return d.temps.outside end, '°C')
    :status('mode', 'Mode', 'house', function() return d.mode:getValue() end)
  :section('Living room', 'lamp')
    :toggle('ceiling', 'Ceiling', 'lightbulb', sw(d.livingCeiling), set(d.livingCeiling))
    :slider('lamp', 'Lamp', 0, 100,
      function() return math.floor(d.livingLamp:getValue() * 100 + 0.5) end,
      function(v) d.livingLamp:setBrightness(v) end)
    :slider('blinds', 'Blinds', 0, 100,
      function() return d.blinds:getPosition() end,
      function(v) d.blinds:setPosition(v) end)
  :section('Kitchen and hall', 'house')
    :toggle('kitchen', 'Kitchen', 'lightbulb', sw(d.kitchen), set(d.kitchen))
    :status('hall', 'Hall light', 'lightbulb', function() return d.hall:getValue() == 1 and 'on' or 'off' end)
    :select('mode_sel', 'Mode', 'gear', {'Home', 'Away', 'Night'},
      function() return d.mode:getValue() end,
      function(v) d.mode:setValue(v) end)
  :section('Temperatures', 'thermometer-half', { columns = 4 })
    :status('t_living', 'Living room', 'thermometer-half', function() return d.temps.living end, '°C')
    :status('t_bedroom', 'Bedroom', 'thermometer-half', function() return d.temps.bedroom end, '°C')
    :status('t_office', 'Office', 'thermometer-half', function() return d.temps.office end, '°C')
    :slider('target', 'Target', 18, 24,
      function() return d.target:getValue() end,
      function(v) d.target:setValue(v) end)
  :build()
