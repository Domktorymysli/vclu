-- Test user scripts for E2E testing

function testInit()
    setVar("initialized", true)
end

-- Test function returning a value
function sceneLightsOff()
    return "all_off"
end

-- Test function for synchronize (called from remote CLUs)
function synchronize(pathOrName, value)
    if pathOrName == nil then
        pathOrName = "default"
    end
    if value == nil then
        value = "default"
    end

    setVar("last_sync_path", pathOrName)
    setVar("last_sync_value", value)
    return "synced"
end

-- Test function for registry inspection
function inspectRegistry()
    local count = 0
    for path, obj in pairs(_registry or {}) do
        count = count + 1
    end
    return count
end

-- Test helper function
function addNumbers(a, b)
    return a + b
end

-- Test MQTT (creates client without actual connection)
function testMqttSetup()
    local mqtt = MQTT.new("test_client")
    mqtt:setHost("127.0.0.1")
    mqtt:setPort(1883)
    mqtt:setCredentials("test", "test")
    mqtt:setTopicPrefix("grenton")
    return mqtt
end
