package lua

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // zone database in the binary, so setTimezone works without system tzdata

	lua "github.com/yuin/gopher-lua"
)

// Common time formats to try when parsing
var timeFormats = []string{
	// ISO 8601
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05.000-07:00",

	// Date only
	"2006-01-02",
	"02-01-2006",
	"02/01/2006",
	"01/02/2006", // US format
	"2006/01/02",
	"02.01.2006",
	"2006.01.02",

	// Date and time
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"02-01-2006 15:04:05",
	"02/01/2006 15:04:05",
	"01/02/2006 15:04:05",
	"02.01.2006 15:04:05",

	// Time only
	"15:04:05",
	"15:04",
	"3:04 PM",
	"3:04:05 PM",

	// RFC formats
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
	time.RFC850,
	time.ANSIC,
	time.UnixDate,
	time.RubyDate,

	// Other common formats
	"Mon Jan 2 15:04:05 2006",
	"Jan 2, 2006",
	"Jan 2, 2006 15:04:05",
	"January 2, 2006",
	"January 2, 2006 15:04:05",
	"2 Jan 2006",
	"2 Jan 2006 15:04:05",
}

// RegisterParserFunctions registers the pure json/xml/datetime/color libraries in L.
// Used by the MCP sandbox so drafts see the same libraries as production.
func RegisterParserFunctions(L *lua.LState) { registerParserFunctions(L) }

// registerParserFunctions registers JSON, XML, and datetime parsing functions in Lua
func registerParserFunctions(L *lua.LState) {
	// Register JSON module
	jsonMod := L.NewTable()
	L.SetField(jsonMod, "decode", L.NewFunction(jsonDecode))
	L.SetField(jsonMod, "encode", L.NewFunction(jsonEncode))
	L.SetGlobal("json", jsonMod)

	// Register XML module
	xmlMod := L.NewTable()
	L.SetField(xmlMod, "decode", L.NewFunction(xmlDecode))
	L.SetGlobal("xml", xmlMod)

	// Register datetime module
	datetimeMod := L.NewTable()
	L.SetField(datetimeMod, "parse", L.NewFunction(datetimeParse))
	L.SetField(datetimeMod, "now", L.NewFunction(datetimeNow))
	L.SetField(datetimeMod, "fromTimestamp", L.NewFunction(datetimeFromTimestamp))
	L.SetField(datetimeMod, "format", L.NewFunction(datetimeFormat))
	L.SetField(datetimeMod, "diff", L.NewFunction(datetimeDiff))
	L.SetField(datetimeMod, "add", L.NewFunction(datetimeAdd))
	L.SetField(datetimeMod, "setTimezone", L.NewFunction(datetimeSetTimezone))
	L.SetField(datetimeMod, "timezone", L.NewFunction(datetimeTimezone))
	L.SetField(datetimeMod, "between", L.NewFunction(datetimeBetween))
	L.SetGlobal("datetime", datetimeMod)

	// Register color module
	registerColorFunctions(L)

	// Also register as global functions for convenience
	L.SetGlobal("__go_json_decode", L.NewFunction(jsonDecode))
	L.SetGlobal("__go_json_encode", L.NewFunction(jsonEncode))
	L.SetGlobal("__go_xml_decode", L.NewFunction(xmlDecode))
	L.SetGlobal("__go_datetime_parse", L.NewFunction(datetimeParse))
	L.SetGlobal("__go_datetime_now", L.NewFunction(datetimeNow))
}

// jsonDecode decodes a JSON string into a Lua table
// Usage: local obj = json.decode('{"name":"test","value":123}')
func jsonDecode(L *lua.LState) int {
	jsonStr := L.CheckString(1)

	var data interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("JSON decode error: %v", err)))
		return 2
	}

	luaValue := goValueToLua(L, data)
	L.Push(luaValue)
	return 1
}

// jsonEncode encodes a Lua table into a JSON string
// Usage: local str = json.encode({name="test", value=123})
func jsonEncode(L *lua.LState) int {
	luaValue := L.Get(1)
	pretty := L.OptBool(2, false)

	goValue, err := LuaToGo(luaValue)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("JSON encode error: %v", err)))
		return 2
	}

	var jsonBytes []byte

	if pretty {
		jsonBytes, err = json.MarshalIndent(goValue, "", "  ")
	} else {
		jsonBytes, err = json.Marshal(goValue)
	}

	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("JSON encode error: %v", err)))
		return 2
	}

	L.Push(lua.LString(string(jsonBytes)))
	return 1
}

// xmlDecode decodes an XML string into a Lua table
// Usage: local obj = xml.decode('<root><name>test</name></root>')
func xmlDecode(L *lua.LState) int {
	xmlStr := L.CheckString(1)

	result, err := parseXML(xmlStr)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("XML decode error: %v", err)))
		return 2
	}

	luaValue := goValueToLua(L, result)
	L.Push(luaValue)
	return 1
}

// goValueToLua converts a Go value to a Lua value
func goValueToLua(L *lua.LState, value interface{}) lua.LValue {
	if value == nil {
		return lua.LNil
	}

	switch v := value.(type) {
	case bool:
		return lua.LBool(v)
	case float64:
		return lua.LNumber(v)
	case int64: // LuaToGo returns whole numbers as int64
		return lua.LNumber(v)
	case int:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case []interface{}:
		tbl := L.NewTable()
		for i, item := range v {
			tbl.RawSetInt(i+1, goValueToLua(L, item)) // Lua arrays are 1-indexed
		}
		return tbl
	case map[string]interface{}:
		tbl := L.NewTable()
		for key, val := range v {
			tbl.RawSetString(key, goValueToLua(L, val))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}

// Limits for Lua -> Go conversion: a cyclic or absurdly nested table would
// overflow the Go stack, which kills the whole process, not just the script.
const (
	maxLuaNesting  = 64
	maxLuaElements = 1000000
)

// LuaToGo converts a Lua value for JSON and KV. Tables with consecutive
// numeric keys from 1 become arrays, others maps. Cycles, nesting deeper
// than 64 or more than a million elements return an error.
func LuaToGo(value lua.LValue) (interface{}, error) {
	c := &luaConverter{onPath: map[*lua.LTable]bool{}}
	return c.convert(value, 0)
}

type luaConverter struct {
	onPath   map[*lua.LTable]bool // tables being converted (shared subtables are fine)
	elements int
}

func (c *luaConverter) convert(value lua.LValue, depth int) (interface{}, error) {
	switch v := value.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(v), nil
	case lua.LNumber:
		f := float64(v)
		// Return as int if it's a whole number
		if f == float64(int64(f)) {
			return int64(f), nil
		}
		return f, nil
	case lua.LString:
		return string(v), nil
	case *lua.LTable:
		return c.table(v, depth)
	default:
		return value.String(), nil
	}
}

func (c *luaConverter) table(t *lua.LTable, depth int) (interface{}, error) {
	if c.onPath[t] {
		return nil, fmt.Errorf("cyclic table")
	}
	if depth >= maxLuaNesting {
		return nil, fmt.Errorf("table nested deeper than %d levels", maxLuaNesting)
	}
	c.onPath[t] = true
	defer delete(c.onPath, t)

	isArray := true
	maxIndex := 0
	t.ForEach(func(key, _ lua.LValue) {
		c.elements++
		if num, ok := key.(lua.LNumber); ok {
			if idx := int(num); idx > maxIndex {
				maxIndex = idx
			}
		} else {
			isArray = false
		}
	})
	if c.elements > maxLuaElements {
		return nil, fmt.Errorf("table has more than %d elements", maxLuaElements)
	}

	var err error
	// A sparse {[1e9]=x} stays a map instead of allocating a huge array
	if isArray && maxIndex > 0 && maxIndex <= maxLuaElements {
		arr := make([]interface{}, maxIndex)
		t.ForEach(func(key, val lua.LValue) {
			idx := int(key.(lua.LNumber)) - 1 // Convert to 0-indexed
			if err == nil && idx >= 0 && idx < maxIndex {
				arr[idx], err = c.convert(val, depth+1)
			}
		})
		return arr, err
	}

	obj := make(map[string]interface{})
	t.ForEach(func(key, val lua.LValue) {
		if err != nil {
			return
		}
		keyStr := ""
		switch k := key.(type) {
		case lua.LString:
			keyStr = string(k)
		case lua.LNumber:
			keyStr = fmt.Sprintf("%v", float64(k))
		default:
			keyStr = key.String()
		}
		obj[keyStr], err = c.convert(val, depth+1)
	})
	return obj, err
}

// XMLNode represents a parsed XML node
type XMLNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Content  string     `xml:",chardata"`
	Children []XMLNode  `xml:",any"`
}

// parseXML parses XML string into a nested map structure
func parseXML(xmlStr string) (map[string]interface{}, error) {
	decoder := xml.NewDecoder(strings.NewReader(xmlStr))
	return parseXMLElement(decoder)
}

func parseXMLElement(decoder *xml.Decoder) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := token.(type) {
		case xml.StartElement:
			child, err := parseXMLContent(decoder, t)
			if err != nil {
				return nil, err
			}

			name := t.Name.Local

			// Handle multiple elements with same name (convert to array)
			if existing, exists := result[name]; exists {
				if arr, isArr := existing.([]interface{}); isArr {
					result[name] = append(arr, child)
				} else {
					result[name] = []interface{}{existing, child}
				}
			} else {
				result[name] = child
			}
		}
	}

	return result, nil
}

func parseXMLContent(decoder *xml.Decoder, start xml.StartElement) (interface{}, error) {
	var content strings.Builder
	children := make(map[string]interface{})
	hasChildren := false

	// Handle attributes on this element
	hasAttrs := len(start.Attr) > 0
	var attrs map[string]interface{}
	if hasAttrs {
		attrs = make(map[string]interface{})
		for _, attr := range start.Attr {
			attrs[attr.Name.Local] = tryParseValue(attr.Value)
		}
	}

	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}

		switch t := token.(type) {
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text != "" {
				content.WriteString(text)
			}

		case xml.StartElement:
			hasChildren = true
			child, err := parseXMLContent(decoder, t)
			if err != nil {
				return nil, err
			}

			name := t.Name.Local

			// Handle multiple elements with same name
			if existing, exists := children[name]; exists {
				if arr, isArr := existing.([]interface{}); isArr {
					children[name] = append(arr, child)
				} else {
					children[name] = []interface{}{existing, child}
				}
			} else {
				children[name] = child
			}

		case xml.EndElement:
			// If we have children, return children map
			if hasChildren {
				if hasAttrs {
					children["_attr"] = attrs
				}
				return children, nil
			}

			// No children - return text content
			textValue := tryParseValue(content.String())

			// If we have attributes, wrap in a map with _text and _attr
			if hasAttrs {
				result := make(map[string]interface{})
				result["_text"] = textValue
				result["_attr"] = attrs
				return result, nil
			}

			// Just return the text value
			return textValue, nil
		}
	}
}

// tryParseValue tries to parse a string as number or bool
func tryParseValue(s string) interface{} {
	s = strings.TrimSpace(s)

	// Try bool
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}

	// Try integer
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}

	// Try float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	return s
}

// ==================== DateTime Functions ====================

// datetimeTZKey holds the zone set by datetime.setTimezone in the Lua registry,
// so it lives exactly as long as the Lua state (a reload starts without one).
const datetimeTZKey = "__datetime_tz"

var tzCache sync.Map // zone name -> *time.Location

func loadZone(name string) (*time.Location, error) {
	if loc, ok := tzCache.Load(name); ok {
		return loc.(*time.Location), nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	tzCache.Store(name, loc)
	return loc, nil
}

// datetimeZone returns the zone set by setTimezone, or nil (server-local, as before).
func datetimeZone(L *lua.LState) *time.Location {
	name, ok := L.G.Registry.RawGetString(datetimeTZKey).(lua.LString)
	if !ok || name == "" {
		return nil
	}
	loc, err := loadZone(string(name))
	if err != nil {
		return nil
	}
	return loc
}

// inZone converts t to the datetime zone if one is set.
func inZone(L *lua.LState, t time.Time) time.Time {
	if loc := datetimeZone(L); loc != nil {
		return t.In(loc)
	}
	return t
}

// datetimeSetTimezone sets the zone used by all datetime functions in this runtime.
// Usage: datetime.setTimezone("Europe/Warsaw")  -- first line of modules/init.lua
func datetimeSetTimezone(L *lua.LState) int {
	name := L.CheckString(1)
	if name == "" { // LoadLocation("") means UTC, but "" is our "no zone" marker
		L.RaiseError("datetime.setTimezone: zone name is empty")
		return 0
	}
	if _, err := loadZone(name); err != nil {
		L.RaiseError("datetime.setTimezone: unknown zone %q", name)
		return 0
	}
	L.G.Registry.RawSetString(datetimeTZKey, lua.LString(name))
	return 0
}

// datetimeBetween reports whether the local time of day lies in [from, to),
// wrapping past midnight; from == to means the whole day. Local means the zone
// from setTimezone, so the window follows DST.
// Usage: datetime.between("21:00", "06:00")
//
//	datetime.between("21:00", "06:00", dt)  -- dt: datetime table or timestamp
func datetimeBetween(L *lua.LState) int {
	from, err := parseTimeOfDay(L.CheckString(1))
	if err != nil {
		L.ArgError(1, err.Error())
		return 0
	}
	to, err := parseTimeOfDay(L.CheckString(2))
	if err != nil {
		L.ArgError(2, err.Error())
		return 0
	}

	t := clockNow(L)
	switch v := L.Get(3).(type) {
	case *lua.LNilType:
	case lua.LNumber:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			L.ArgError(3, "timestamp is not a finite number")
			return 0
		}
		if f > 1e12 { // milliseconds, as in fromTimestamp
			t = time.UnixMilli(int64(f))
		} else {
			t = time.Unix(int64(f), 0)
		}
	case *lua.LTable:
		t = luaTableToTime(L, v)
	default:
		L.ArgError(3, "datetime table or timestamp expected")
		return 0
	}
	t = inZone(L, t)
	sec := t.Hour()*3600 + t.Minute()*60 + t.Second()

	var in bool
	switch {
	case from == to:
		in = true
	case from < to:
		in = sec >= from && sec < to
	default:
		in = sec >= from || sec < to
	}
	L.Push(lua.LBool(in))
	return 1
}

// parseTimeOfDay parses "H:MM", "HH:MM" or "HH:MM:SS" (digits only; "24:00"
// is not accepted, the end of the day is "00:00") into seconds after midnight.
func parseTimeOfDay(s string) (int, error) {
	bad := fmt.Errorf("time %q: expected HH:MM or HH:MM:SS", s)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, bad
	}
	limits := []int{23, 59, 59}
	sec := 0
	for i, p := range parts {
		if (i == 0 && (len(p) < 1 || len(p) > 2)) || (i > 0 && len(p) != 2) {
			return 0, bad
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0, bad
			}
			n = n*10 + int(c-'0')
		}
		if n > limits[i] {
			return 0, bad
		}
		sec += n * []int{3600, 60, 1}[i]
	}
	return sec, nil
}

// datetimeTimezone returns the zone set by setTimezone ("" if none: server-local time).
// Usage: local tz = datetime.timezone()
func datetimeTimezone(L *lua.LState) int {
	name, _ := L.G.Registry.RawGetString(datetimeTZKey).(lua.LString)
	L.Push(name)
	return 1
}

// datetimeParse parses a datetime string and returns a Lua table with components
// Usage: local dt = datetime.parse("2024-01-15 14:30:00")
//
//	dt.year, dt.month, dt.day, dt.hour, dt.minute, dt.second
//	dt.timestamp (Unix timestamp in seconds)
//	dt.weekday (1=Monday, 7=Sunday)
//	dt.yearday (1-366)
func datetimeParse(L *lua.LState) int {
	input := L.CheckString(1)
	input = strings.TrimSpace(input)

	// Try to parse as Unix timestamp first
	if ts, err := strconv.ParseInt(input, 10, 64); err == nil {
		// It's a timestamp (seconds or milliseconds)
		var t time.Time
		if ts > 1e12 {
			// Milliseconds
			t = time.UnixMilli(ts)
		} else {
			// Seconds
			t = time.Unix(ts, 0)
		}
		L.Push(timeToLuaTable(L, t))
		return 1
	}

	// Try each format; without an explicit offset the input is read in the datetime zone
	loc := datetimeZone(L)
	for _, format := range timeFormats {
		var t time.Time
		var err error
		if loc != nil {
			t, err = time.ParseInLocation(format, input, loc)
		} else {
			t, err = time.Parse(format, input)
		}
		if err == nil {
			L.Push(timeToLuaTable(L, t))
			return 1
		}
	}

	// Failed to parse
	L.Push(lua.LNil)
	L.Push(lua.LString(fmt.Sprintf("cannot parse datetime: %s", input)))
	return 2
}

// datetimeNow returns the current datetime as a Lua table
// Usage: local now = datetime.now()
func datetimeNow(L *lua.LState) int {
	L.Push(timeToLuaTable(L, clockNow(L)))
	return 1
}

// datetimeFromTimestamp creates a datetime from Unix timestamp
// Usage: local dt = datetime.fromTimestamp(1705312200)
func datetimeFromTimestamp(L *lua.LState) int {
	ts := L.CheckInt64(1)

	var t time.Time
	if ts > 1e12 {
		// Milliseconds
		t = time.UnixMilli(ts)
	} else {
		// Seconds
		t = time.Unix(ts, 0)
	}

	L.Push(timeToLuaTable(L, t))
	return 1
}

// datetimeFormat formats a datetime table to a string
// Usage: local str = datetime.format(dt, "2006-01-02 15:04:05")
// Common format patterns:
//
//	"2006-01-02"          -> "2024-01-15"
//	"15:04:05"            -> "14:30:00"
//	"2006-01-02 15:04:05" -> "2024-01-15 14:30:00"
//	"Mon Jan 2 2006"      -> "Mon Jan 15 2024"
//	"02/01/2006"          -> "15/01/2024"
func datetimeFormat(L *lua.LState) int {
	dtTable := L.CheckTable(1)
	format := L.OptString(2, "2006-01-02 15:04:05")

	t := luaTableToTime(L, dtTable)
	L.Push(lua.LString(t.Format(format)))
	return 1
}

// datetimeDiff calculates the difference between two datetimes
// Usage: local diff = datetime.diff(dt1, dt2)
// Returns: table with days, hours, minutes, seconds, totalSeconds
func datetimeDiff(L *lua.LState) int {
	dt1Table := L.CheckTable(1)
	dt2Table := L.CheckTable(2)

	t1 := luaTableToTime(L, dt1Table)
	t2 := luaTableToTime(L, dt2Table)

	duration := t2.Sub(t1)

	result := L.NewTable()
	totalSeconds := int64(duration.Seconds())
	L.SetField(result, "totalSeconds", lua.LNumber(totalSeconds))

	// Calculate components
	days := totalSeconds / 86400
	remaining := totalSeconds % 86400
	hours := remaining / 3600
	remaining = remaining % 3600
	minutes := remaining / 60
	seconds := remaining % 60

	L.SetField(result, "days", lua.LNumber(days))
	L.SetField(result, "hours", lua.LNumber(hours))
	L.SetField(result, "minutes", lua.LNumber(minutes))
	L.SetField(result, "seconds", lua.LNumber(seconds))

	// Also provide total in different units
	L.SetField(result, "totalMinutes", lua.LNumber(int64(duration.Minutes())))
	L.SetField(result, "totalHours", lua.LNumber(int64(duration.Hours())))
	L.SetField(result, "totalDays", lua.LNumber(days))

	L.Push(result)
	return 1
}

// datetimeAdd adds duration to a datetime
// Usage: local newDt = datetime.add(dt, {days=1, hours=2, minutes=30})
func datetimeAdd(L *lua.LState) int {
	dtTable := L.CheckTable(1)
	addTable := L.CheckTable(2)

	t := luaTableToTime(L, dtTable)

	// Get values to add
	years := int(getTableInt(addTable, "years", 0))
	months := int(getTableInt(addTable, "months", 0))
	days := int(getTableInt(addTable, "days", 0))
	hours := getTableInt(addTable, "hours", 0)
	minutes := getTableInt(addTable, "minutes", 0)
	seconds := getTableInt(addTable, "seconds", 0)

	// Add date components
	t = t.AddDate(years, months, days)

	// Add time components
	duration := time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second
	t = t.Add(duration)

	L.Push(timeToLuaTable(L, t))
	return 1
}

// timeToLuaTable converts a Go time.Time to a Lua table (in the datetime zone if set)
func timeToLuaTable(L *lua.LState, t time.Time) *lua.LTable {
	t = inZone(L, t)
	tbl := L.NewTable()

	// Basic components
	L.SetField(tbl, "year", lua.LNumber(t.Year()))
	L.SetField(tbl, "month", lua.LNumber(t.Month()))
	L.SetField(tbl, "day", lua.LNumber(t.Day()))
	L.SetField(tbl, "hour", lua.LNumber(t.Hour()))
	L.SetField(tbl, "minute", lua.LNumber(t.Minute()))
	L.SetField(tbl, "second", lua.LNumber(t.Second()))

	// Additional info
	L.SetField(tbl, "weekday", lua.LNumber(isoWeekday(t.Weekday())))
	L.SetField(tbl, "yearday", lua.LNumber(t.YearDay()))
	L.SetField(tbl, "timestamp", lua.LNumber(t.Unix()))
	L.SetField(tbl, "timestampMs", lua.LNumber(t.UnixMilli()))

	// Timezone info
	zone, offset := t.Zone()
	L.SetField(tbl, "tzOffset", lua.LNumber(offset))
	L.SetField(tbl, "tz", lua.LString(zone))

	// Pre-formatted strings for convenience
	L.SetField(tbl, "iso", lua.LString(t.Format(time.RFC3339)))
	L.SetField(tbl, "date", lua.LString(t.Format("2006-01-02")))
	L.SetField(tbl, "time", lua.LString(t.Format("15:04:05")))

	// Month and weekday names
	L.SetField(tbl, "monthName", lua.LString(t.Month().String()))
	L.SetField(tbl, "weekdayName", lua.LString(t.Weekday().String()))

	return tbl
}

// luaTableToTime converts a Lua datetime table back to Go time.Time
func luaTableToTime(L *lua.LState, tbl *lua.LTable) time.Time {
	// Try to get timestamp first
	if ts := tbl.RawGetString("timestamp"); ts != lua.LNil {
		if num, ok := ts.(lua.LNumber); ok {
			return inZone(L, time.Unix(int64(num), 0))
		}
	}

	// Otherwise build from components
	year := int(getTableInt(tbl, "year", int64(clockNow(L).Year())))
	month := time.Month(getTableInt(tbl, "month", 1))
	day := int(getTableInt(tbl, "day", 1))
	hour := int(getTableInt(tbl, "hour", 0))
	minute := int(getTableInt(tbl, "minute", 0))
	second := int(getTableInt(tbl, "second", 0))

	loc := datetimeZone(L)
	if loc == nil {
		loc = time.Local
	}
	return time.Date(year, month, day, hour, minute, second, 0, loc)
}

// getTableInt gets an integer from a Lua table with a default value
func getTableInt(tbl *lua.LTable, key string, defaultVal int64) int64 {
	val := tbl.RawGetString(key)
	if num, ok := val.(lua.LNumber); ok {
		return int64(num)
	}
	return defaultVal
}

// isoWeekday converts Go's weekday (Sunday=0) to ISO (Monday=1, Sunday=7)
func isoWeekday(wd time.Weekday) int {
	if wd == time.Sunday {
		return 7
	}
	return int(wd)
}

// ==================== Color Functions ====================

// Named colors map (CSS color names)
var namedColors = map[string][3]int{
	"black":       {0, 0, 0},
	"white":       {255, 255, 255},
	"red":         {255, 0, 0},
	"green":       {0, 128, 0},
	"blue":        {0, 0, 255},
	"yellow":      {255, 255, 0},
	"cyan":        {0, 255, 255},
	"magenta":     {255, 0, 255},
	"orange":      {255, 165, 0},
	"pink":        {255, 192, 203},
	"purple":      {128, 0, 128},
	"violet":      {238, 130, 238},
	"brown":       {165, 42, 42},
	"gray":        {128, 128, 128},
	"grey":        {128, 128, 128},
	"silver":      {192, 192, 192},
	"gold":        {255, 215, 0},
	"navy":        {0, 0, 128},
	"teal":        {0, 128, 128},
	"olive":       {128, 128, 0},
	"maroon":      {128, 0, 0},
	"lime":        {0, 255, 0},
	"aqua":        {0, 255, 255},
	"fuchsia":     {255, 0, 255},
	"coral":       {255, 127, 80},
	"salmon":      {250, 128, 114},
	"tomato":      {255, 99, 71},
	"crimson":     {220, 20, 60},
	"indigo":      {75, 0, 130},
	"turquoise":   {64, 224, 208},
	"skyblue":     {135, 206, 235},
	"lavender":    {230, 230, 250},
	"beige":       {245, 245, 220},
	"ivory":       {255, 255, 240},
	"khaki":       {240, 230, 140},
	"chocolate":   {210, 105, 30},
	"tan":         {210, 180, 140},
	"wheat":       {245, 222, 179},
	"snow":        {255, 250, 250},
	"honeydew":    {240, 255, 240},
	"azure":       {240, 255, 255},
	"mintcream":   {245, 255, 250},
	"darkred":     {139, 0, 0},
	"darkgreen":   {0, 100, 0},
	"darkblue":    {0, 0, 139},
	"darkorange":  {255, 140, 0},
	"darkcyan":    {0, 139, 139},
	"darkmagenta": {139, 0, 139},
	"darkgray":    {169, 169, 169},
	"darkgrey":    {169, 169, 169},
	"lightred":    {255, 102, 102},
	"lightgreen":  {144, 238, 144},
	"lightblue":   {173, 216, 230},
	"lightyellow": {255, 255, 224},
	"lightcyan":   {224, 255, 255},
	"lightpink":   {255, 182, 193},
	"lightgray":   {211, 211, 211},
	"lightgrey":   {211, 211, 211},
	"warmwhite":   {255, 244, 229},
	"coolwhite":   {244, 255, 255},
	"daylight":    {255, 255, 251},
}

// registerColorFunctions registers color manipulation functions in Lua
func registerColorFunctions(L *lua.LState) {
	colorMod := L.NewTable()

	// Conversion functions
	L.SetField(colorMod, "hexToRGB", L.NewFunction(colorHexToRGB))
	L.SetField(colorMod, "rgbToHex", L.NewFunction(colorRGBToHex))
	L.SetField(colorMod, "hsvToRGB", L.NewFunction(colorHSVToRGB))
	L.SetField(colorMod, "rgbToHSV", L.NewFunction(colorRGBToHSV))
	L.SetField(colorMod, "hslToRGB", L.NewFunction(colorHSLToRGB))
	L.SetField(colorMod, "rgbToHSL", L.NewFunction(colorRGBToHSL))

	// Temperature
	L.SetField(colorMod, "kelvinToRGB", L.NewFunction(colorKelvinToRGB))

	// Named colors
	L.SetField(colorMod, "fromName", L.NewFunction(colorFromName))
	L.SetField(colorMod, "toName", L.NewFunction(colorToName))
	L.SetField(colorMod, "getNames", L.NewFunction(colorGetNames))

	// Manipulation
	L.SetField(colorMod, "setBrightness", L.NewFunction(colorSetBrightness))
	L.SetField(colorMod, "blend", L.NewFunction(colorBlend))
	L.SetField(colorMod, "complement", L.NewFunction(colorComplement))
	L.SetField(colorMod, "invert", L.NewFunction(colorInvert))

	// Parsing (auto-detect format)
	L.SetField(colorMod, "parse", L.NewFunction(colorParse))

	L.SetGlobal("color", colorMod)
}

// colorHexToRGB converts hex color to RGB table
// Usage: local rgb = color.hexToRGB("#FF5500") or color.hexToRGB("FF5500")
func colorHexToRGB(L *lua.LState) int {
	hex := L.CheckString(1)
	hex = strings.TrimPrefix(hex, "#")
	hex = strings.ToUpper(hex)

	// Handle short format (e.g., "F50" -> "FF5500")
	if len(hex) == 3 {
		hex = string(hex[0]) + string(hex[0]) + string(hex[1]) + string(hex[1]) + string(hex[2]) + string(hex[2])
	}

	if len(hex) != 6 {
		L.Push(lua.LNil)
		L.Push(lua.LString("invalid hex color format"))
		return 2
	}

	r, err1 := strconv.ParseInt(hex[0:2], 16, 64)
	g, err2 := strconv.ParseInt(hex[2:4], 16, 64)
	b, err3 := strconv.ParseInt(hex[4:6], 16, 64)

	if err1 != nil || err2 != nil || err3 != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("invalid hex color value"))
		return 2
	}

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.Push(tbl)
	return 1
}

// colorRGBToHex converts RGB to hex string
// Usage: local hex = color.rgbToHex(255, 85, 0) or color.rgbToHex({r=255, g=85, b=0})
func colorRGBToHex(L *lua.LState) int {
	var r, g, b int

	if L.GetTop() == 1 {
		// Table input
		tbl := L.CheckTable(1)
		r = int(getTableInt(tbl, "r", 0))
		g = int(getTableInt(tbl, "g", 0))
		b = int(getTableInt(tbl, "b", 0))
	} else {
		// Separate arguments
		r = L.CheckInt(1)
		g = L.CheckInt(2)
		b = L.CheckInt(3)
	}

	// Clamp values
	r = clampInt(r, 0, 255)
	g = clampInt(g, 0, 255)
	b = clampInt(b, 0, 255)

	hex := fmt.Sprintf("#%02X%02X%02X", r, g, b)
	L.Push(lua.LString(hex))
	return 1
}

// colorHSVToRGB converts HSV to RGB
// Usage: local rgb = color.hsvToRGB(20, 100, 100) -- h=0-360, s=0-100, v=0-100
func colorHSVToRGB(L *lua.LState) int {
	var h, s, v float64

	if L.GetTop() == 1 {
		tbl := L.CheckTable(1)
		h = float64(getTableInt(tbl, "h", 0))
		s = float64(getTableInt(tbl, "s", 0))
		v = float64(getTableInt(tbl, "v", 0))
	} else {
		h = float64(L.CheckNumber(1))
		s = float64(L.CheckNumber(2))
		v = float64(L.CheckNumber(3))
	}

	r, g, b := hsvToRGB(h, s/100, v/100)

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.Push(tbl)
	return 1
}

// colorRGBToHSV converts RGB to HSV
// Usage: local hsv = color.rgbToHSV(255, 85, 0)
func colorRGBToHSV(L *lua.LState) int {
	var r, g, b int

	if L.GetTop() == 1 {
		tbl := L.CheckTable(1)
		r = int(getTableInt(tbl, "r", 0))
		g = int(getTableInt(tbl, "g", 0))
		b = int(getTableInt(tbl, "b", 0))
	} else {
		r = L.CheckInt(1)
		g = L.CheckInt(2)
		b = L.CheckInt(3)
	}

	h, s, v := rgbToHSV(r, g, b)

	tbl := L.NewTable()
	L.SetField(tbl, "h", lua.LNumber(int(h)))
	L.SetField(tbl, "s", lua.LNumber(int(s*100)))
	L.SetField(tbl, "v", lua.LNumber(int(v*100)))
	L.Push(tbl)
	return 1
}

// colorHSLToRGB converts HSL to RGB
// Usage: local rgb = color.hslToRGB(20, 100, 50) -- h=0-360, s=0-100, l=0-100
func colorHSLToRGB(L *lua.LState) int {
	var h, s, l float64

	if L.GetTop() == 1 {
		tbl := L.CheckTable(1)
		h = float64(getTableInt(tbl, "h", 0))
		s = float64(getTableInt(tbl, "s", 0))
		l = float64(getTableInt(tbl, "l", 0))
	} else {
		h = float64(L.CheckNumber(1))
		s = float64(L.CheckNumber(2))
		l = float64(L.CheckNumber(3))
	}

	r, g, b := hslToRGB(h, s/100, l/100)

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.Push(tbl)
	return 1
}

// colorRGBToHSL converts RGB to HSL
// Usage: local hsl = color.rgbToHSL(255, 85, 0)
func colorRGBToHSL(L *lua.LState) int {
	var r, g, b int

	if L.GetTop() == 1 {
		tbl := L.CheckTable(1)
		r = int(getTableInt(tbl, "r", 0))
		g = int(getTableInt(tbl, "g", 0))
		b = int(getTableInt(tbl, "b", 0))
	} else {
		r = L.CheckInt(1)
		g = L.CheckInt(2)
		b = L.CheckInt(3)
	}

	h, s, l := rgbToHSL(r, g, b)

	tbl := L.NewTable()
	L.SetField(tbl, "h", lua.LNumber(int(h)))
	L.SetField(tbl, "s", lua.LNumber(int(s*100)))
	L.SetField(tbl, "l", lua.LNumber(int(l*100)))
	L.Push(tbl)
	return 1
}

// colorKelvinToRGB converts color temperature (Kelvin) to RGB
// Usage: local rgb = color.kelvinToRGB(2700) -- warm white
//
//	local rgb = color.kelvinToRGB(6500) -- cool daylight
func colorKelvinToRGB(L *lua.LState) int {
	kelvin := float64(L.CheckNumber(1))

	// Clamp to reasonable range
	if kelvin < 1000 {
		kelvin = 1000
	}
	if kelvin > 40000 {
		kelvin = 40000
	}

	r, g, b := kelvinToRGB(kelvin)

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.SetField(tbl, "kelvin", lua.LNumber(int(kelvin)))
	L.Push(tbl)
	return 1
}

// colorFromName gets RGB from a color name
// Usage: local rgb = color.fromName("orange")
func colorFromName(L *lua.LState) int {
	name := strings.ToLower(L.CheckString(1))

	if rgb, ok := namedColors[name]; ok {
		tbl := L.NewTable()
		L.SetField(tbl, "r", lua.LNumber(rgb[0]))
		L.SetField(tbl, "g", lua.LNumber(rgb[1]))
		L.SetField(tbl, "b", lua.LNumber(rgb[2]))
		L.SetField(tbl, "name", lua.LString(name))
		L.Push(tbl)
		return 1
	}

	L.Push(lua.LNil)
	L.Push(lua.LString(fmt.Sprintf("unknown color name: %s", name)))
	return 2
}

// colorToName finds the closest named color to given RGB
// Usage: local name = color.toName(255, 165, 0) -- "orange"
func colorToName(L *lua.LState) int {
	var r, g, b int

	if L.GetTop() == 1 {
		tbl := L.CheckTable(1)
		r = int(getTableInt(tbl, "r", 0))
		g = int(getTableInt(tbl, "g", 0))
		b = int(getTableInt(tbl, "b", 0))
	} else {
		r = L.CheckInt(1)
		g = L.CheckInt(2)
		b = L.CheckInt(3)
	}

	closestName := "unknown"
	minDistance := float64(1000000)

	for name, rgb := range namedColors {
		// Euclidean distance in RGB space
		dr := float64(r - rgb[0])
		dg := float64(g - rgb[1])
		db := float64(b - rgb[2])
		distance := dr*dr + dg*dg + db*db

		if distance < minDistance {
			minDistance = distance
			closestName = name
		}
	}

	L.Push(lua.LString(closestName))
	L.Push(lua.LNumber(minDistance)) // Also return distance for confidence
	return 2
}

// colorGetNames returns a list of all available color names
func colorGetNames(L *lua.LState) int {
	tbl := L.NewTable()
	i := 1
	for name := range namedColors {
		tbl.RawSetInt(i, lua.LString(name))
		i++
	}
	L.Push(tbl)
	return 1
}

// colorSetBrightness adjusts the brightness of a color
// Usage: local dimmed = color.setBrightness("#FF5500", 50) -- 50% brightness
func colorSetBrightness(L *lua.LState) int {
	var r, g, b int
	var brightness float64

	if L.GetTop() >= 2 {
		first := L.Get(1)
		brightness = float64(L.CheckNumber(2)) / 100

		if str, ok := first.(lua.LString); ok {
			// Hex input
			hex := strings.TrimPrefix(string(str), "#")
			if len(hex) == 3 {
				hex = string(hex[0]) + string(hex[0]) + string(hex[1]) + string(hex[1]) + string(hex[2]) + string(hex[2])
			}
			rVal, _ := strconv.ParseInt(hex[0:2], 16, 64)
			gVal, _ := strconv.ParseInt(hex[2:4], 16, 64)
			bVal, _ := strconv.ParseInt(hex[4:6], 16, 64)
			r, g, b = int(rVal), int(gVal), int(bVal)
		} else if tbl, ok := first.(*lua.LTable); ok {
			// Table input
			r = int(getTableInt(tbl, "r", 0))
			g = int(getTableInt(tbl, "g", 0))
			b = int(getTableInt(tbl, "b", 0))
		}
	} else {
		L.Push(lua.LNil)
		L.Push(lua.LString("usage: color.setBrightness(color, percent)"))
		return 2
	}

	// Convert to HSV, adjust V, convert back
	h, s, v := rgbToHSV(r, g, b)
	newV := v * brightness
	newR, newG, newB := hsvToRGB(h, s, newV)

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(newR))
	L.SetField(tbl, "g", lua.LNumber(newG))
	L.SetField(tbl, "b", lua.LNumber(newB))
	L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", newR, newG, newB)))
	L.Push(tbl)
	return 1
}

// colorBlend blends two colors
// Usage: local mixed = color.blend("#FF0000", "#0000FF", 0.5) -- 50% mix
func colorBlend(L *lua.LState) int {
	color1 := parseColorArg(L, 1)
	color2 := parseColorArg(L, 2)
	ratio := float64(L.OptNumber(3, 0.5))

	if color1 == nil || color2 == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("invalid color argument"))
		return 2
	}

	r := int(float64(color1[0])*(1-ratio) + float64(color2[0])*ratio)
	g := int(float64(color1[1])*(1-ratio) + float64(color2[1])*ratio)
	b := int(float64(color1[2])*(1-ratio) + float64(color2[2])*ratio)

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", r, g, b)))
	L.Push(tbl)
	return 1
}

// colorComplement returns the complementary color
// Usage: local comp = color.complement("#FF5500")
func colorComplement(L *lua.LState) int {
	rgb := parseColorArg(L, 1)
	if rgb == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("invalid color argument"))
		return 2
	}

	// Shift hue by 180 degrees
	h, s, v := rgbToHSV(rgb[0], rgb[1], rgb[2])
	h = h + 180
	if h >= 360 {
		h -= 360
	}
	r, g, b := hsvToRGB(h, s, v)

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", r, g, b)))
	L.Push(tbl)
	return 1
}

// colorInvert returns the inverted color
// Usage: local inv = color.invert("#FF5500")
func colorInvert(L *lua.LState) int {
	rgb := parseColorArg(L, 1)
	if rgb == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("invalid color argument"))
		return 2
	}

	r := 255 - rgb[0]
	g := 255 - rgb[1]
	b := 255 - rgb[2]

	tbl := L.NewTable()
	L.SetField(tbl, "r", lua.LNumber(r))
	L.SetField(tbl, "g", lua.LNumber(g))
	L.SetField(tbl, "b", lua.LNumber(b))
	L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", r, g, b)))
	L.Push(tbl)
	return 1
}

// colorParse auto-detects and parses color format
// Usage: local rgb = color.parse("#FF5500") or color.parse("orange") or color.parse("rgb(255,85,0)")
func colorParse(L *lua.LState) int {
	input := L.CheckString(1)
	input = strings.TrimSpace(input)

	// Try hex
	if strings.HasPrefix(input, "#") || len(input) == 6 || len(input) == 3 {
		hex := strings.TrimPrefix(input, "#")
		if len(hex) == 3 {
			hex = string(hex[0]) + string(hex[0]) + string(hex[1]) + string(hex[1]) + string(hex[2]) + string(hex[2])
		}
		if len(hex) == 6 {
			r, err1 := strconv.ParseInt(hex[0:2], 16, 64)
			g, err2 := strconv.ParseInt(hex[2:4], 16, 64)
			b, err3 := strconv.ParseInt(hex[4:6], 16, 64)
			if err1 == nil && err2 == nil && err3 == nil {
				tbl := L.NewTable()
				L.SetField(tbl, "r", lua.LNumber(r))
				L.SetField(tbl, "g", lua.LNumber(g))
				L.SetField(tbl, "b", lua.LNumber(b))
				L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", r, g, b)))
				L.SetField(tbl, "format", lua.LString("hex"))
				L.Push(tbl)
				return 1
			}
		}
	}

	// Try rgb(...) format
	if strings.HasPrefix(strings.ToLower(input), "rgb(") {
		input = strings.TrimPrefix(strings.ToLower(input), "rgb(")
		input = strings.TrimSuffix(input, ")")
		parts := strings.Split(input, ",")
		if len(parts) == 3 {
			r, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
			g, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			b, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
			tbl := L.NewTable()
			L.SetField(tbl, "r", lua.LNumber(r))
			L.SetField(tbl, "g", lua.LNumber(g))
			L.SetField(tbl, "b", lua.LNumber(b))
			L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", r, g, b)))
			L.SetField(tbl, "format", lua.LString("rgb"))
			L.Push(tbl)
			return 1
		}
	}

	// Try named color
	name := strings.ToLower(input)
	if rgb, ok := namedColors[name]; ok {
		tbl := L.NewTable()
		L.SetField(tbl, "r", lua.LNumber(rgb[0]))
		L.SetField(tbl, "g", lua.LNumber(rgb[1]))
		L.SetField(tbl, "b", lua.LNumber(rgb[2]))
		L.SetField(tbl, "hex", lua.LString(fmt.Sprintf("#%02X%02X%02X", rgb[0], rgb[1], rgb[2])))
		L.SetField(tbl, "name", lua.LString(name))
		L.SetField(tbl, "format", lua.LString("name"))
		L.Push(tbl)
		return 1
	}

	L.Push(lua.LNil)
	L.Push(lua.LString(fmt.Sprintf("cannot parse color: %s", input)))
	return 2
}

// parseColorArg parses a color argument (hex string or RGB table)
func parseColorArg(L *lua.LState, idx int) []int {
	arg := L.Get(idx)

	switch v := arg.(type) {
	case lua.LString:
		hex := strings.TrimPrefix(string(v), "#")
		if len(hex) == 3 {
			hex = string(hex[0]) + string(hex[0]) + string(hex[1]) + string(hex[1]) + string(hex[2]) + string(hex[2])
		}
		if len(hex) == 6 {
			r, _ := strconv.ParseInt(hex[0:2], 16, 64)
			g, _ := strconv.ParseInt(hex[2:4], 16, 64)
			b, _ := strconv.ParseInt(hex[4:6], 16, 64)
			return []int{int(r), int(g), int(b)}
		}
		// Try named color
		if rgb, ok := namedColors[strings.ToLower(string(v))]; ok {
			return []int{rgb[0], rgb[1], rgb[2]}
		}
	case *lua.LTable:
		r := int(getTableInt(v, "r", 0))
		g := int(getTableInt(v, "g", 0))
		b := int(getTableInt(v, "b", 0))
		return []int{r, g, b}
	}

	return nil
}

// ==================== Color Conversion Helpers ====================

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// hsvToRGB converts HSV to RGB
// h: 0-360, s: 0-1, v: 0-1
func hsvToRGB(h, s, v float64) (int, int, int) {
	if s == 0 {
		val := int(v * 255)
		return val, val, val
	}

	h = h / 60
	i := int(h)
	f := h - float64(i)
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))

	var r, g, b float64
	switch i % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	case 5:
		r, g, b = v, p, q
	}

	return int(r * 255), int(g * 255), int(b * 255)
}

// rgbToHSV converts RGB to HSV
func rgbToHSV(r, g, b int) (float64, float64, float64) {
	rf := float64(r) / 255
	gf := float64(g) / 255
	bf := float64(b) / 255

	max := maxFloat(rf, maxFloat(gf, bf))
	min := minFloat(rf, minFloat(gf, bf))
	delta := max - min

	var h, s, v float64
	v = max

	if max == 0 {
		s = 0
	} else {
		s = delta / max
	}

	if delta == 0 {
		h = 0
	} else {
		switch max {
		case rf:
			h = (gf - bf) / delta
			if gf < bf {
				h += 6
			}
		case gf:
			h = (bf-rf)/delta + 2
		case bf:
			h = (rf-gf)/delta + 4
		}
		h *= 60
	}

	return h, s, v
}

// hslToRGB converts HSL to RGB
// h: 0-360, s: 0-1, l: 0-1
func hslToRGB(h, s, l float64) (int, int, int) {
	if s == 0 {
		val := int(l * 255)
		return val, val, val
	}

	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q

	hk := h / 360

	tr := hk + 1.0/3.0
	tg := hk
	tb := hk - 1.0/3.0

	r := hueToRGB(p, q, tr)
	g := hueToRGB(p, q, tg)
	b := hueToRGB(p, q, tb)

	return int(r * 255), int(g * 255), int(b * 255)
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6.0 {
		return p + (q-p)*6*t
	}
	if t < 0.5 {
		return q
	}
	if t < 2.0/3.0 {
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

// rgbToHSL converts RGB to HSL
func rgbToHSL(r, g, b int) (float64, float64, float64) {
	rf := float64(r) / 255
	gf := float64(g) / 255
	bf := float64(b) / 255

	max := maxFloat(rf, maxFloat(gf, bf))
	min := minFloat(rf, minFloat(gf, bf))
	delta := max - min

	var h, s, l float64
	l = (max + min) / 2

	if delta == 0 {
		h = 0
		s = 0
	} else {
		if l < 0.5 {
			s = delta / (max + min)
		} else {
			s = delta / (2 - max - min)
		}

		switch max {
		case rf:
			h = (gf - bf) / delta
			if gf < bf {
				h += 6
			}
		case gf:
			h = (bf-rf)/delta + 2
		case bf:
			h = (rf-gf)/delta + 4
		}
		h *= 60
	}

	return h, s, l
}

// kelvinToRGB converts color temperature to RGB
// Based on Tanner Helland's algorithm
func kelvinToRGB(kelvin float64) (int, int, int) {
	temp := kelvin / 100

	var r, g, b float64

	// Red
	if temp <= 66 {
		r = 255
	} else {
		r = temp - 60
		r = 329.698727446 * math.Pow(r, -0.1332047592)
		r = clampFloat(r, 0, 255)
	}

	// Green
	if temp <= 66 {
		g = temp
		g = 99.4708025861*math.Log(g) - 161.1195681661
		g = clampFloat(g, 0, 255)
	} else {
		g = temp - 60
		g = 288.1221695283 * math.Pow(g, -0.0755148492)
		g = clampFloat(g, 0, 255)
	}

	// Blue
	if temp >= 66 {
		b = 255
	} else if temp <= 19 {
		b = 0
	} else {
		b = temp - 10
		b = 138.5177312231*math.Log(b) - 305.0447927307
		b = clampFloat(b, 0, 255)
	}

	return int(r), int(g), int(b)
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
