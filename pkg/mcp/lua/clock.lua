-- os.time and os.date read the sandbox's virtual clock
local stdTime, stdDate = os.time, os.date
os.time = function(t)
	if t == nil then return math.floor(__go_time_ms() / 1000) end
	return stdTime(t)
end
os.date = function(format, t)
	if t == nil then t = math.floor(__go_time_ms() / 1000) end
	return stdDate(format or "%c", t)
end
