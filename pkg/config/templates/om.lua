-- FwType {{FIRMWARE_TYPE_HEX8}}
-- FwVersion {{FIRMWARE_VERSION_HEX}}
-- HwType {{HARDWARE_TYPE_HEX8}}
-- HwVersion {{HARDWARE_VERSION_HEX8}}

{{CLU_NAME}} = GATE:new(5000, {{IP_ADDRESS_INT}}, 0xFFFFFF00, {{GATEWAY_INT}}, "", "")
-- NAME_CLU Gate={{CLU_NAME}}

-- MODULES

-- IO_MODULES

function setVar(name, value)
	_G[name] = value
end

function getVar(name)
	return _G[name]
end

function OnInit()
-- INIT_CLU_OBJECTS
{{CLU_NAME}}:set(1, 1000)
{{CLU_NAME}}:set(2, "8.8.8.8")
{{CLU_NAME}}:set(3, "8.8.4.4")

	init(nil) -- CluOnInit
end

{{CLU_NAME}}:add_event(0, OnInit)
