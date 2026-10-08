collectgarbage("collect")
require "user"

collectgarbage("collect")
require "om"
collectgarbage("collect")


function checkAlive()
	-- Return serial number as 8 hex characters (required by Object Manager)
	-- OM accepts: serialNumber OR "true"
	return "{{SERIAL_HEX_SHORT}}"
end

SYSTEM.Init()
