-- Test OM file - simplified for E2E testing
-- Uses only types available in bootstrap_go.lua (GATE, CLU_REMOTE)
-- FwType 00000003
-- FwVersion 000003e8
-- HwType 00000012
-- HwVersion 00000001

-- Main GATE (Virtual CLU)
CLU520046772 = GATE:new(5000, 0xC0A800DA, 0xFFFFFF00, 0xC0A80001, "", "")
-- NAME_CLU TestVclu=CLU520046772

-- Remote CLUs (simulating real Grenton CLUs on the network)
CLU212345678 = OBJECT:new(1, 0xC0A80003, "CLU212345678")
CLU220000592 = OBJECT:new(1, 0xC0A80005, "CLU220000592")

-- User variables for testing
test_var = "initial_value"
counter = 0
initialized = false

function setVar(name, value)
    _G[name] = value
end

function getVar(name)
    return _G[name]
end

function OnInit()
    -- INIT_CLU_OBJECTS
    CLU520046772:set(1, 10000)
    CLU520046772:set(2, "8.8.8.8")
    CLU520046772:set(3, "8.8.4.4")

    testInit(nil) -- CluOnInit
end

CLU520046772:add_event(0, OnInit)
