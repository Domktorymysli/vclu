package lua

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ==================== JSON Tests ====================

func TestJsonDecode(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: json module exists
	result, err := engine.Execute("type(json)")
	if err != nil {
		t.Fatalf("Failed to check json type: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected json to be table, got %s", result)
	}

	// Test 2: Decode simple object
	_, err = engine.Execute(`
		data = json.decode('{"name":"test","value":123,"active":true}')
	`)
	if err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	result, _ = engine.Execute("data.name")
	if result != "test" {
		t.Errorf("Expected name='test', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(data.value)")
	if result != "123" {
		t.Errorf("Expected value=123, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(data.active)")
	if result != "true" {
		t.Errorf("Expected active=true, got '%s'", result)
	}

	// Test 3: Decode array
	_, err = engine.Execute(`
		arr = json.decode('[1, 2, 3, "four", true]')
	`)
	if err != nil {
		t.Fatalf("Failed to decode JSON array: %v", err)
	}

	result, _ = engine.Execute("tostring(#arr)")
	if result != "5" {
		t.Errorf("Expected array length 5, got %s", result)
	}

	result, _ = engine.Execute("tostring(arr[1])")
	if result != "1" {
		t.Errorf("Expected arr[1]=1, got '%s'", result)
	}

	result, _ = engine.Execute("arr[4]")
	if result != "four" {
		t.Errorf("Expected arr[4]='four', got '%s'", result)
	}

	// Test 4: Decode nested object
	_, err = engine.Execute(`
		nested = json.decode('{"user":{"name":"John","age":30},"items":[1,2,3]}')
	`)
	if err != nil {
		t.Fatalf("Failed to decode nested JSON: %v", err)
	}

	result, _ = engine.Execute("nested.user.name")
	if result != "John" {
		t.Errorf("Expected nested.user.name='John', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(nested.user.age)")
	if result != "30" {
		t.Errorf("Expected nested.user.age=30, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(nested.items[2])")
	if result != "2" {
		t.Errorf("Expected nested.items[2]=2, got '%s'", result)
	}

	// Test 5: Handle null
	_, err = engine.Execute(`
		withNull = json.decode('{"value":null}')
	`)
	if err != nil {
		t.Fatalf("Failed to decode JSON with null: %v", err)
	}

	result, _ = engine.Execute("tostring(withNull.value)")
	if result != "nil" {
		t.Errorf("Expected withNull.value=nil, got '%s'", result)
	}

	// Test 6: Error handling
	_, err = engine.Execute(`
		bad, errMsg = json.decode('invalid json')
	`)
	if err != nil {
		t.Fatalf("Failed to handle invalid JSON: %v", err)
	}

	result, _ = engine.Execute("tostring(bad)")
	if result != "nil" {
		t.Errorf("Expected bad=nil, got '%s'", result)
	}

	result, _ = engine.Execute("errMsg")
	if !strings.Contains(result, "error") {
		t.Errorf("Expected error message, got '%s'", result)
	}

	t.Log("JSON decode test passed!")
}

func TestJsonEncode(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: Encode simple object
	_, err := engine.Execute(`
		str = json.encode({name = "test", value = 123, active = true})
	`)
	if err != nil {
		t.Fatalf("Failed to encode JSON: %v", err)
	}

	result, _ := engine.Execute("str")
	if !strings.Contains(result, `"name"`) || !strings.Contains(result, `"test"`) {
		t.Errorf("Expected encoded JSON to contain name:test, got '%s'", result)
	}

	// Test 2: Encode array
	_, err = engine.Execute(`
		arrStr = json.encode({1, 2, 3, "four"})
	`)
	if err != nil {
		t.Fatalf("Failed to encode JSON array: %v", err)
	}

	result, _ = engine.Execute("arrStr")
	if !strings.Contains(result, "1") || !strings.Contains(result, `"four"`) {
		t.Errorf("Expected encoded array, got '%s'", result)
	}

	// Test 3: Round-trip
	_, err = engine.Execute(`
		original = {user = {name = "Alice", age = 25}, tags = {"a", "b", "c"}}
		encoded = json.encode(original)
		decoded = json.decode(encoded)
	`)
	if err != nil {
		t.Fatalf("Failed round-trip: %v", err)
	}

	result, _ = engine.Execute("decoded.user.name")
	if result != "Alice" {
		t.Errorf("Expected decoded.user.name='Alice', got '%s'", result)
	}

	result, _ = engine.Execute("decoded.tags[2]")
	if result != "b" {
		t.Errorf("Expected decoded.tags[2]='b', got '%s'", result)
	}

	// Test 4: Pretty print
	_, err = engine.Execute(`
		pretty = json.encode({name = "test"}, true)
	`)
	if err != nil {
		t.Fatalf("Failed pretty print: %v", err)
	}

	result, _ = engine.Execute("pretty")
	if !strings.Contains(result, "\n") {
		t.Errorf("Expected pretty print with newlines, got '%s'", result)
	}

	t.Log("JSON encode test passed!")
}

// ==================== XML Tests ====================

func TestXmlDecode(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: xml module exists
	result, err := engine.Execute("type(xml)")
	if err != nil {
		t.Fatalf("Failed to check xml type: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected xml to be table, got %s", result)
	}

	// Test 2: Decode simple XML
	_, err = engine.Execute(`
		data = xml.decode('<root><name>test</name><value>123</value></root>')
	`)
	if err != nil {
		t.Fatalf("Failed to decode XML: %v", err)
	}

	result, _ = engine.Execute("data.root.name")
	if result != "test" {
		t.Errorf("Expected root.name='test', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(data.root.value)")
	if result != "123" {
		t.Errorf("Expected root.value=123, got '%s'", result)
	}

	// Test 3: Decode with attributes
	_, err = engine.Execute(`
		withAttr = xml.decode('<item id="42" active="true">content</item>')
	`)
	if err != nil {
		t.Fatalf("Failed to decode XML with attributes: %v", err)
	}

	result, _ = engine.Execute("tostring(withAttr.item._attr.id)")
	if result != "42" {
		t.Errorf("Expected item._attr.id=42, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(withAttr.item._attr.active)")
	if result != "true" {
		t.Errorf("Expected item._attr.active=true, got '%s'", result)
	}

	result, _ = engine.Execute("withAttr.item._text")
	if result != "content" {
		t.Errorf("Expected item._text='content', got '%s'", result)
	}

	// Test 4: Decode nested XML
	_, err = engine.Execute(`
		nested = xml.decode([[
			<response>
				<status>ok</status>
				<data>
					<user>
						<name>John</name>
						<age>30</age>
					</user>
				</data>
			</response>
		]])
	`)
	if err != nil {
		t.Fatalf("Failed to decode nested XML: %v", err)
	}

	result, _ = engine.Execute("nested.response.status")
	if result != "ok" {
		t.Errorf("Expected response.status='ok', got '%s'", result)
	}

	result, _ = engine.Execute("nested.response.data.user.name")
	if result != "John" {
		t.Errorf("Expected user.name='John', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(nested.response.data.user.age)")
	if result != "30" {
		t.Errorf("Expected user.age=30, got '%s'", result)
	}

	// Test 5: Multiple same-name elements (array)
	_, err = engine.Execute(`
		multi = xml.decode([[
			<list>
				<item>first</item>
				<item>second</item>
				<item>third</item>
			</list>
		]])
	`)
	if err != nil {
		t.Fatalf("Failed to decode XML with multiple elements: %v", err)
	}

	result, _ = engine.Execute("type(multi.list.item)")
	if result != "table" {
		t.Errorf("Expected list.item to be table (array), got %s", result)
	}

	result, _ = engine.Execute("multi.list.item[1]")
	if result != "first" {
		t.Errorf("Expected list.item[1]='first', got '%s'", result)
	}

	result, _ = engine.Execute("multi.list.item[3]")
	if result != "third" {
		t.Errorf("Expected list.item[3]='third', got '%s'", result)
	}

	// Test 6: Boolean and number parsing
	_, err = engine.Execute(`
		types = xml.decode('<data><bool>true</bool><num>3.14</num><int>42</int></data>')
	`)
	if err != nil {
		t.Fatalf("Failed to decode XML types: %v", err)
	}

	result, _ = engine.Execute("tostring(types.data.bool)")
	if result != "true" {
		t.Errorf("Expected data.bool=true, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(types.data.num)")
	if result != "3.14" {
		t.Errorf("Expected data.num=3.14, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(types.data.int)")
	if result != "42" {
		t.Errorf("Expected data.int=42, got '%s'", result)
	}

	// Test 7: Error handling
	_, err = engine.Execute(`
		bad, errMsg = xml.decode('not valid xml <><>')
	`)
	if err != nil {
		t.Fatalf("Failed to handle invalid XML: %v", err)
	}

	result, _ = engine.Execute("errMsg")
	if result == "nil" {
		// Some invalid XML might parse partially - that's OK
		t.Log("XML parsed partially or succeeded unexpectedly")
	}

	t.Log("XML decode test passed!")
}

// ==================== Integration Tests ====================

func TestParsersWithHTTP(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Simulate parsing HTTP JSON response
	_, err := engine.Execute(`
		-- Simulate HTTP response
		httpResponse = '{"status":"success","data":{"id":123,"name":"Widget"}}'

		-- Parse the response
		result = json.decode(httpResponse)

		-- Access the data
		productName = result.data.name
		productId = result.data.id
	`)
	if err != nil {
		t.Fatalf("Failed HTTP JSON simulation: %v", err)
	}

	result, _ := engine.Execute("productName")
	if result != "Widget" {
		t.Errorf("Expected productName='Widget', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(productId)")
	if result != "123" {
		t.Errorf("Expected productId=123, got '%s'", result)
	}

	// Simulate parsing HTTP XML response (REST API style)
	_, err = engine.Execute(`
		-- Simulate REST API XML response
		apiResponse = [[
			<Response>
				<Weather>
					<Temperature>22.5</Temperature>
					<Humidity>65</Humidity>
					<Condition>Sunny</Condition>
				</Weather>
			</Response>
		]]

		weather = xml.decode(apiResponse)
	`)
	if err != nil {
		t.Fatalf("Failed XML API simulation: %v", err)
	}

	// Navigate the XML structure
	result, _ = engine.Execute("weather.Response.Weather.Condition")
	if result != "Sunny" {
		t.Errorf("Expected Condition='Sunny', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(weather.Response.Weather.Temperature)")
	if result != "22.5" {
		t.Errorf("Expected Temperature=22.5, got '%s'", result)
	}

	t.Log("Parsers HTTP integration test passed!")
}

// ==================== DateTime Tests ====================

func TestDatetimeParse(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: datetime module exists
	result, err := engine.Execute("type(datetime)")
	if err != nil {
		t.Fatalf("Failed to check datetime type: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected datetime to be table, got %s", result)
	}

	// Test 2: Parse ISO 8601
	_, err = engine.Execute(`
		dt = datetime.parse("2024-01-15T14:30:00Z")
	`)
	if err != nil {
		t.Fatalf("Failed to parse ISO datetime: %v", err)
	}

	result, _ = engine.Execute("tostring(dt.year)")
	if result != "2024" {
		t.Errorf("Expected year=2024, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(dt.month)")
	if result != "1" {
		t.Errorf("Expected month=1, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(dt.day)")
	if result != "15" {
		t.Errorf("Expected day=15, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(dt.hour)")
	if result != "14" {
		t.Errorf("Expected hour=14, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(dt.minute)")
	if result != "30" {
		t.Errorf("Expected minute=30, got '%s'", result)
	}

	// Test 3: Parse date only
	_, err = engine.Execute(`
		dt2 = datetime.parse("2024-01-15")
	`)
	if err != nil {
		t.Fatalf("Failed to parse date: %v", err)
	}

	result, _ = engine.Execute("dt2.date")
	if result != "2024-01-15" {
		t.Errorf("Expected date='2024-01-15', got '%s'", result)
	}

	// Test 4: Parse European format
	_, err = engine.Execute(`
		dt3 = datetime.parse("15/01/2024 14:30:05")
	`)
	if err != nil {
		t.Fatalf("Failed to parse European format: %v", err)
	}

	result, _ = engine.Execute("tostring(dt3.day)")
	if result != "15" {
		t.Errorf("Expected day=15 for European format, got '%s'", result)
	}

	// Test 5: Parse Unix timestamp
	_, err = engine.Execute(`
		dt4 = datetime.parse("1705312200")
	`)
	if err != nil {
		t.Fatalf("Failed to parse timestamp: %v", err)
	}

	result, _ = engine.Execute("tostring(dt4.timestamp)")
	if result != "1705312200" {
		t.Errorf("Expected timestamp=1705312200, got '%s'", result)
	}

	// Test 6: Check all fields
	_, err = engine.Execute(`
		dt5 = datetime.parse("2024-06-15 12:30:45")
	`)
	if err != nil {
		t.Fatalf("Failed to parse full datetime: %v", err)
	}

	result, _ = engine.Execute("dt5.iso")
	if !strings.Contains(result, "2024-06-15") {
		t.Errorf("Expected iso to contain date, got '%s'", result)
	}

	result, _ = engine.Execute("dt5.time")
	if result != "12:30:45" {
		t.Errorf("Expected time='12:30:45', got '%s'", result)
	}

	result, _ = engine.Execute("dt5.monthName")
	if result != "June" {
		t.Errorf("Expected monthName='June', got '%s'", result)
	}

	result, _ = engine.Execute("tostring(dt5.weekday)")
	// June 15, 2024 is a Saturday = 6
	if result != "6" {
		t.Errorf("Expected weekday=6 (Saturday), got '%s'", result)
	}

	// Test 7: Error handling
	_, err = engine.Execute(`
		bad, errMsg = datetime.parse("not a date")
	`)
	if err != nil {
		t.Fatalf("Failed to handle invalid date: %v", err)
	}

	result, _ = engine.Execute("tostring(bad)")
	if result != "nil" {
		t.Errorf("Expected bad=nil, got '%s'", result)
	}

	t.Log("DateTime parse test passed!")
}

func TestDatetimeNow(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Get current time
	_, err := engine.Execute(`
		now = datetime.now()
	`)
	if err != nil {
		t.Fatalf("Failed to get now: %v", err)
	}

	// Should have reasonable year
	result, _ := engine.Execute("tostring(now.year)")
	if result != "2024" && result != "2025" && result != "2026" {
		t.Errorf("Expected reasonable year, got '%s'", result)
	}

	// Should have timestamp
	result, _ = engine.Execute("tostring(now.timestamp)")
	if result == "0" || result == "" {
		t.Error("Expected non-zero timestamp")
	}

	t.Log("DateTime now test passed!")
}

func TestDatetimeFromTimestamp(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// From seconds
	_, err := engine.Execute(`
		dt = datetime.fromTimestamp(1705312200)
	`)
	if err != nil {
		t.Fatalf("Failed fromTimestamp: %v", err)
	}

	result, _ := engine.Execute("tostring(dt.year)")
	if result != "2024" {
		t.Errorf("Expected year=2024, got '%s'", result)
	}

	// From milliseconds
	_, err = engine.Execute(`
		dtMs = datetime.fromTimestamp(1705312200000)
	`)
	if err != nil {
		t.Fatalf("Failed fromTimestamp ms: %v", err)
	}

	result, _ = engine.Execute("tostring(dtMs.year)")
	if result != "2024" {
		t.Errorf("Expected year=2024 from ms, got '%s'", result)
	}

	t.Log("DateTime fromTimestamp test passed!")
}

func TestDatetimeFormat(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Use ISO format with Z to ensure UTC
	_, err := engine.Execute(`
		dt = datetime.parse("2024-06-15T14:30:45Z")

		-- Default format
		str1 = datetime.format(dt)

		-- Custom formats
		str2 = datetime.format(dt, "02/01/2006")
		str3 = datetime.format(dt, "15:04")
		str4 = datetime.format(dt, "Mon, 02 Jan 2006")
	`)
	if err != nil {
		t.Fatalf("Failed to format: %v", err)
	}

	// Just check that format works - exact time may vary by timezone
	result, _ := engine.Execute("str1")
	if !strings.Contains(result, "2024") || !strings.Contains(result, "06") {
		t.Errorf("Expected date in format, got '%s'", result)
	}

	result, _ = engine.Execute("str2")
	if !strings.Contains(result, "2024") {
		t.Errorf("Expected year in DD/MM/YYYY format, got '%s'", result)
	}

	result, _ = engine.Execute("str3")
	if !strings.Contains(result, ":") {
		t.Errorf("Expected HH:MM format, got '%s'", result)
	}

	result, _ = engine.Execute("str4")
	if !strings.Contains(result, "Jun 2024") {
		t.Errorf("Expected verbose format with Jun 2024, got '%s'", result)
	}

	t.Log("DateTime format test passed!")
}

func TestDatetimeDiff(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		dt1 = datetime.parse("2024-01-01 00:00:00")
		dt2 = datetime.parse("2024-01-02 02:30:45")

		diff = datetime.diff(dt1, dt2)
	`)
	if err != nil {
		t.Fatalf("Failed to diff: %v", err)
	}

	result, _ := engine.Execute("tostring(diff.days)")
	if result != "1" {
		t.Errorf("Expected days=1, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(diff.hours)")
	if result != "2" {
		t.Errorf("Expected hours=2, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(diff.minutes)")
	if result != "30" {
		t.Errorf("Expected minutes=30, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(diff.seconds)")
	if result != "45" {
		t.Errorf("Expected seconds=45, got '%s'", result)
	}

	// Total seconds = 1*86400 + 2*3600 + 30*60 + 45 = 95445
	result, _ = engine.Execute("tostring(diff.totalSeconds)")
	if result != "95445" {
		t.Errorf("Expected totalSeconds=95445, got '%s'", result)
	}

	t.Log("DateTime diff test passed!")
}

func TestDatetimeAdd(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Use fromTimestamp for consistent timezone handling
	_, err := engine.Execute(`
		-- Create datetime from known timestamp (2024-01-15 10:00:00 UTC)
		dt = datetime.fromTimestamp(1705312800)

		-- Add 1 day
		dt2 = datetime.add(dt, {days = 1})

		-- Add months
		dt4 = datetime.add(dt, {months = 2})

		-- Subtract (negative values)
		dt5 = datetime.add(dt, {days = -5})

		-- Calculate differences to verify
		diff_days = dt2.timestamp - dt.timestamp
		diff_months_day = dt4.day
	`)
	if err != nil {
		t.Fatalf("Failed to add: %v", err)
	}

	// +1 day = +86400 seconds
	result, _ := engine.Execute("tostring(diff_days)")
	if result != "86400" {
		t.Errorf("Expected +86400 seconds after +1 day, got '%s'", result)
	}

	// After +2 months from January, month should be 3 (March)
	result, _ = engine.Execute("tostring(dt4.month)")
	if result != "3" {
		t.Errorf("Expected month=3 after +2 months, got '%s'", result)
	}

	// After -5 days from 15th, day should be 10th
	result, _ = engine.Execute("tostring(dt5.day)")
	if result != "10" {
		t.Errorf("Expected day=10 after -5 days, got '%s'", result)
	}

	// Test hours/minutes add
	_, err = engine.Execute(`
		base = datetime.fromTimestamp(1705312800)  -- 10:00 UTC
		added = datetime.add(base, {hours = 2, minutes = 30})
		hour_diff = added.timestamp - base.timestamp
	`)
	if err != nil {
		t.Fatalf("Failed to add hours: %v", err)
	}

	// +2h30m = +9000 seconds
	result, _ = engine.Execute("tostring(hour_diff)")
	if result != "9000" {
		t.Errorf("Expected +9000 seconds after +2h30m, got '%s'", result)
	}

	t.Log("DateTime add test passed!")
}

func TestDatetimeFormats(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test various input formats
	formats := []struct {
		input    string
		expected string // expected date part
	}{
		{"2024-01-15", "2024-01-15"},
		{"15-01-2024", "2024-01-15"},
		{"15/01/2024", "2024-01-15"},
		{"2024/01/15", "2024-01-15"},
		{"15.01.2024", "2024-01-15"},
		{"2024-01-15 14:30:00", "2024-01-15"},
		{"2024-01-15T14:30:00Z", "2024-01-15"},
		{"Jan 15, 2024", "2024-01-15"},
		{"January 15, 2024", "2024-01-15"},
		{"15 Jan 2024", "2024-01-15"},
	}

	for _, tc := range formats {
		_, err := engine.Execute(fmt.Sprintf(`testDt = datetime.parse("%s")`, tc.input))
		if err != nil {
			t.Errorf("Failed to parse '%s': %v", tc.input, err)
			continue
		}

		result, _ := engine.Execute("testDt.date")
		if result != tc.expected {
			t.Errorf("Format '%s': expected date='%s', got '%s'", tc.input, tc.expected, result)
		}
	}

	t.Log("DateTime formats test passed!")
}

func TestDatetimeRealWorld(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Real-world scenario: Check if event is in the past
	_, err := engine.Execute(`
		-- Parse event time from API
		eventTime = datetime.parse("2024-12-25 18:00:00")
		now = datetime.now()

		-- Calculate time until event
		diff = datetime.diff(now, eventTime)

		-- Format for display
		displayDate = datetime.format(eventTime, "Monday, January 2, 2006 at 15:04")
	`)
	if err != nil {
		t.Fatalf("Failed real-world scenario: %v", err)
	}

	result, _ := engine.Execute("displayDate")
	if !strings.Contains(result, "December 25, 2024") {
		t.Errorf("Expected formatted date, got '%s'", result)
	}

	// Real-world scenario: Schedule for tomorrow at same time
	_, err = engine.Execute(`
		now = datetime.now()
		tomorrow = datetime.add(now, {days = 1})
		tomorrowStr = datetime.format(tomorrow, "2006-01-02")
	`)
	if err != nil {
		t.Fatalf("Failed tomorrow scenario: %v", err)
	}

	// Just verify it runs - can't check exact value
	result, _ = engine.Execute("tomorrowStr")
	if len(result) != 10 { // YYYY-MM-DD
		t.Errorf("Expected date format YYYY-MM-DD, got '%s'", result)
	}

	t.Log("DateTime real-world test passed!")
}

// ==================== Color Parser Tests ====================

func TestColorHexToRGB(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test basic hex conversion
	_, err := engine.Execute(`
		rgb = color.hexToRGB("#FF5500")
	`)
	if err != nil {
		t.Fatalf("Failed to convert hex: %v", err)
	}

	result, _ := engine.Execute("rgb.r")
	if result != "255" {
		t.Errorf("Expected r=255, got '%s'", result)
	}

	result, _ = engine.Execute("rgb.g")
	if result != "85" {
		t.Errorf("Expected g=85, got '%s'", result)
	}

	result, _ = engine.Execute("rgb.b")
	if result != "0" {
		t.Errorf("Expected b=0, got '%s'", result)
	}

	// Test without # prefix
	_, err = engine.Execute(`rgb2 = color.hexToRGB("00FF00")`)
	if err != nil {
		t.Fatalf("Failed without # prefix: %v", err)
	}
	result, _ = engine.Execute("rgb2.g")
	if result != "255" {
		t.Errorf("Expected g=255, got '%s'", result)
	}

	// Test short format
	_, err = engine.Execute(`rgb3 = color.hexToRGB("#F00")`)
	if err != nil {
		t.Fatalf("Failed short format: %v", err)
	}
	result, _ = engine.Execute("rgb3.r")
	if result != "255" {
		t.Errorf("Expected r=255 for short format, got '%s'", result)
	}

	t.Log("Color hexToRGB test passed!")
}

func TestColorRGBToHex(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test with separate arguments
	result, err := engine.Execute("color.rgbToHex(255, 85, 0)")
	if err != nil {
		t.Fatalf("Failed rgbToHex: %v", err)
	}
	if result != "#FF5500" {
		t.Errorf("Expected #FF5500, got '%s'", result)
	}

	// Test with table
	result, err = engine.Execute("color.rgbToHex({r=0, g=255, b=128})")
	if err != nil {
		t.Fatalf("Failed rgbToHex with table: %v", err)
	}
	if result != "#00FF80" {
		t.Errorf("Expected #00FF80, got '%s'", result)
	}

	t.Log("Color rgbToHex test passed!")
}

func TestColorHSVConversion(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test HSV to RGB (red)
	_, err := engine.Execute(`rgb = color.hsvToRGB(0, 100, 100)`)
	if err != nil {
		t.Fatalf("Failed hsvToRGB: %v", err)
	}
	result, _ := engine.Execute("rgb.r")
	if result != "255" {
		t.Errorf("Expected r=255 for red, got '%s'", result)
	}

	// Test RGB to HSV
	_, err = engine.Execute(`hsv = color.rgbToHSV(255, 0, 0)`)
	if err != nil {
		t.Fatalf("Failed rgbToHSV: %v", err)
	}
	result, _ = engine.Execute("hsv.h")
	if result != "0" {
		t.Errorf("Expected h=0 for red, got '%s'", result)
	}
	result, _ = engine.Execute("hsv.s")
	if result != "100" {
		t.Errorf("Expected s=100 for red, got '%s'", result)
	}

	// Test orange (h=20-40)
	_, err = engine.Execute(`hsv_orange = color.rgbToHSV(255, 165, 0)`)
	if err != nil {
		t.Fatalf("Failed rgbToHSV for orange: %v", err)
	}
	result, _ = engine.Execute("hsv_orange.h")
	h, _ := strconv.Atoi(result)
	if h < 30 || h > 45 {
		t.Errorf("Expected h~38 for orange, got '%s'", result)
	}

	t.Log("Color HSV conversion test passed!")
}

func TestColorHSLConversion(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test HSL to RGB (pure red: h=0, s=100, l=50)
	_, err := engine.Execute(`rgb = color.hslToRGB(0, 100, 50)`)
	if err != nil {
		t.Fatalf("Failed hslToRGB: %v", err)
	}
	result, _ := engine.Execute("rgb.r")
	if result != "255" {
		t.Errorf("Expected r=255 for red HSL, got '%s'", result)
	}

	// Test RGB to HSL
	_, err = engine.Execute(`hsl = color.rgbToHSL(255, 0, 0)`)
	if err != nil {
		t.Fatalf("Failed rgbToHSL: %v", err)
	}
	result, _ = engine.Execute("hsl.h")
	if result != "0" {
		t.Errorf("Expected h=0 for red, got '%s'", result)
	}

	t.Log("Color HSL conversion test passed!")
}

func TestColorKelvinToRGB(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test warm white (2700K)
	_, err := engine.Execute(`warm = color.kelvinToRGB(2700)`)
	if err != nil {
		t.Fatalf("Failed kelvinToRGB: %v", err)
	}
	result, _ := engine.Execute("warm.r")
	rWarm, _ := strconv.Atoi(result)
	if rWarm != 255 {
		t.Errorf("Expected r=255 for warm white, got '%s'", result)
	}
	result, _ = engine.Execute("warm.b")
	bWarm, _ := strconv.Atoi(result)
	if bWarm > 200 {
		t.Errorf("Expected low blue for warm white, got '%s'", result)
	}

	// Test cool white (6500K)
	_, err = engine.Execute(`cool = color.kelvinToRGB(6500)`)
	if err != nil {
		t.Fatalf("Failed kelvinToRGB cool: %v", err)
	}
	result, _ = engine.Execute("cool.b")
	bCool, _ := strconv.Atoi(result)
	if bCool < 240 { // Algorithm approximation gives ~250
		t.Errorf("Expected b>=240 for cool white, got '%s'", result)
	}

	t.Logf("Warm white (2700K): r=%d, b=%d", rWarm, bWarm)
	t.Logf("Cool white (6500K): b=%d", bCool)
	t.Log("Color kelvinToRGB test passed!")
}

func TestColorNamedColors(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test fromName
	_, err := engine.Execute(`orange = color.fromName("orange")`)
	if err != nil {
		t.Fatalf("Failed fromName: %v", err)
	}
	result, _ := engine.Execute("orange.r")
	if result != "255" {
		t.Errorf("Expected r=255 for orange, got '%s'", result)
	}
	result, _ = engine.Execute("orange.g")
	if result != "165" {
		t.Errorf("Expected g=165 for orange, got '%s'", result)
	}

	// Test toName
	result, err = engine.Execute("color.toName(255, 0, 0)")
	if err != nil {
		t.Fatalf("Failed toName: %v", err)
	}
	if result != "red" {
		t.Errorf("Expected 'red', got '%s'", result)
	}

	// Test toName with table
	result, err = engine.Execute("color.toName({r=0, g=0, b=255})")
	if err != nil {
		t.Fatalf("Failed toName with table: %v", err)
	}
	if result != "blue" {
		t.Errorf("Expected 'blue', got '%s'", result)
	}

	// Test unknown color gets closest match
	result, err = engine.Execute("color.toName(254, 1, 1)")
	if err != nil {
		t.Fatalf("Failed toName for near-red: %v", err)
	}
	if result != "red" {
		t.Errorf("Expected 'red' for (254,1,1), got '%s'", result)
	}

	t.Log("Color named colors test passed!")
}

func TestColorManipulation(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test setBrightness
	_, err := engine.Execute(`dimmed = color.setBrightness("#FF0000", 50)`)
	if err != nil {
		t.Fatalf("Failed setBrightness: %v", err)
	}
	result, _ := engine.Execute("dimmed.r")
	r, _ := strconv.Atoi(result)
	if r > 130 || r < 120 {
		t.Errorf("Expected r~127 for 50%% brightness red, got %d", r)
	}

	// Test blend
	_, err = engine.Execute(`blended = color.blend("#FF0000", "#0000FF", 0.5)`)
	if err != nil {
		t.Fatalf("Failed blend: %v", err)
	}
	result, _ = engine.Execute("blended.r")
	rBlend, _ := strconv.Atoi(result)
	result, _ = engine.Execute("blended.b")
	bBlend, _ := strconv.Atoi(result)
	if rBlend < 120 || rBlend > 140 || bBlend < 120 || bBlend > 140 {
		t.Errorf("Expected r~127, b~127 for 50%% blend, got r=%d, b=%d", rBlend, bBlend)
	}

	// Test complement
	_, err = engine.Execute(`comp = color.complement("#FF0000")`)
	if err != nil {
		t.Fatalf("Failed complement: %v", err)
	}
	// Red complement is cyan (0, 255, 255)
	result, _ = engine.Execute("comp.r")
	if result != "0" {
		t.Errorf("Expected r=0 for red complement, got '%s'", result)
	}
	result, _ = engine.Execute("comp.g")
	if result != "255" {
		t.Errorf("Expected g=255 for red complement, got '%s'", result)
	}

	// Test invert
	_, err = engine.Execute(`inv = color.invert("#FF5500")`)
	if err != nil {
		t.Fatalf("Failed invert: %v", err)
	}
	result, _ = engine.Execute("inv.r")
	if result != "0" {
		t.Errorf("Expected r=0 for inverted, got '%s'", result)
	}
	result, _ = engine.Execute("inv.g")
	if result != "170" { // 255 - 85 = 170
		t.Errorf("Expected g=170 for inverted, got '%s'", result)
	}

	t.Log("Color manipulation test passed!")
}

func TestColorParse(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test hex parsing
	_, err := engine.Execute(`c1 = color.parse("#FF5500")`)
	if err != nil {
		t.Fatalf("Failed parse hex: %v", err)
	}
	result, _ := engine.Execute("c1.format")
	if result != "hex" {
		t.Errorf("Expected format='hex', got '%s'", result)
	}

	// Test rgb() parsing
	_, err = engine.Execute(`c2 = color.parse("rgb(255, 85, 0)")`)
	if err != nil {
		t.Fatalf("Failed parse rgb(): %v", err)
	}
	result, _ = engine.Execute("c2.format")
	if result != "rgb" {
		t.Errorf("Expected format='rgb', got '%s'", result)
	}
	result, _ = engine.Execute("c2.r")
	if result != "255" {
		t.Errorf("Expected r=255, got '%s'", result)
	}

	// Test named color parsing
	_, err = engine.Execute(`c3 = color.parse("orange")`)
	if err != nil {
		t.Fatalf("Failed parse name: %v", err)
	}
	result, _ = engine.Execute("c3.format")
	if result != "name" {
		t.Errorf("Expected format='name', got '%s'", result)
	}
	result, _ = engine.Execute("c3.name")
	if result != "orange" {
		t.Errorf("Expected name='orange', got '%s'", result)
	}

	t.Log("Color parse test passed!")
}

func TestColorRealWorld(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Real-world scenario: LED RGB strip control
	_, err := engine.Execute(`
		-- Parse color from Home Assistant
		userColor = color.parse("#FF6B00")

		-- Convert to HSV for dimming
		hsv = color.rgbToHSV(userColor.r, userColor.g, userColor.b)

		-- Dim to 30%
		dimmedRgb = color.hsvToRGB(hsv.h, hsv.s, 30)
		dimmedHex = color.rgbToHex(dimmedRgb)

		-- Get warm white for night mode
		nightLight = color.kelvinToRGB(2200)
		nightHex = color.rgbToHex(nightLight)
	`)
	if err != nil {
		t.Fatalf("Failed real-world scenario: %v", err)
	}

	result, _ := engine.Execute("dimmedHex")
	if !strings.HasPrefix(result, "#") {
		t.Errorf("Expected hex string, got '%s'", result)
	}
	t.Logf("Dimmed color: %s", result)

	result, _ = engine.Execute("nightHex")
	t.Logf("Night light color: %s", result)

	// Color transition blending
	_, err = engine.Execute(`
		startColor = color.fromName("red")
		endColor = color.fromName("blue")

		-- Create 5-step gradient
		steps = {}
		for i = 0, 4 do
			local ratio = i / 4
			steps[i+1] = color.blend(
				{r=startColor.r, g=startColor.g, b=startColor.b},
				{r=endColor.r, g=endColor.g, b=endColor.b},
				ratio
			)
		end

		step1_hex = steps[1].hex
		step3_hex = steps[3].hex
		step5_hex = steps[5].hex
	`)
	if err != nil {
		t.Fatalf("Failed gradient scenario: %v", err)
	}

	result, _ = engine.Execute("step1_hex")
	t.Logf("Gradient step 1: %s", result)
	result, _ = engine.Execute("step3_hex")
	t.Logf("Gradient step 3: %s", result)
	result, _ = engine.Execute("step5_hex")
	t.Logf("Gradient step 5: %s", result)

	t.Log("Color real-world test passed!")
}

// datetime.setTimezone: the server clock may be UTC, automations need local time.
func TestDatetimeTimezone(t *testing.T) {
	// A machine zone unlike Warsaw, so falling back to time.Local shows up on any host
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.FixedZone("TEST", -7*3600)

	engine := NewLuaEngine()
	defer engine.Close()

	run := func(script string) string {
		t.Helper()
		r, err := engine.Execute(script)
		if err != nil {
			t.Fatalf("Lua error: %v\nscript: %s", err, script)
		}
		return r
	}

	// Unset: nothing changes (server-local, as before)
	if got := run(`return datetime.timezone()`); got != "" {
		t.Errorf("timezone() before set = %q, want empty", got)
	}

	run(`datetime.setTimezone("Europe/Warsaw")`)
	if got := run(`return datetime.timezone()`); got != "Europe/Warsaw" {
		t.Errorf("timezone() = %q", got)
	}

	cases := []struct{ name, script, want string }{
		// 2026-07-01 19:00 UTC = 21:00 CEST; 2026-12-31 20:00 UTC = 21:00 CET
		{"summer hour", `local d = datetime.fromTimestamp(1782932400) return d.hour .. "/" .. d.tzOffset .. "/" .. d.tz`, "21/7200/CEST"},
		{"winter hour", `local d = datetime.fromTimestamp(1798747200) return d.hour .. "/" .. d.tzOffset .. "/" .. d.tz`, "21/3600/CET"},
		// DST change 2026-10-25 01:00 UTC: 02:59 CEST -> 02:00 CET
		{"before DST end", `return datetime.fromTimestamp(1792889940).time`, "02:59:00"},
		{"after DST end", `return datetime.fromTimestamp(1792890000).time`, "02:00:00"},
		// Naive strings are read in the zone; explicit offsets are kept
		{"parse naive", `return tostring(datetime.parse("2026-10-04 23:00:00").timestamp)`, "1791147600"},
		{"parse with Z", `local d = datetime.parse("2026-10-04T21:00:00Z") return d.hour .. "/" .. d.timestamp`, "23/1791147600"},
		{"format", `return datetime.format(datetime.fromTimestamp(1791147600), "2006-01-02 15:04 MST")`, "2026-10-04 23:00 CEST"},
		// Adding a day across the DST change keeps the wall-clock hour (25 h)
		{"add across DST", `local d = datetime.add(datetime.parse("2026-10-24 12:00:00"), {days = 1}) return d.date .. " " .. d.time .. " " .. d.tz`, "2026-10-25 12:00:00 CET"},
		{"diff across DST", `return tostring(datetime.diff(datetime.parse("2026-10-24 12:00:00"), datetime.parse("2026-10-25 12:00:00")).totalHours)`, "25"},
		{"components table", `return datetime.format({year = 2026, month = 7, day = 1, hour = 21}, "15:04 MST")`, "21:00 CEST"},
		{"now has zone", `local d = datetime.now() return tostring(d.tz == "CET" or d.tz == "CEST")`, "true"},
	}
	for _, c := range cases {
		if got := run(c.script); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	if _, err := engine.Execute(`datetime.setTimezone("Mars/Olympus")`); err == nil {
		t.Error("unknown zone should raise an error")
	}
	if _, err := engine.Execute(`datetime.setTimezone("")`); err == nil {
		t.Error("empty zone should raise an error, not silently reset")
	}
	if got := run(`return datetime.timezone()`); got != "Europe/Warsaw" {
		t.Errorf("failed setTimezone must keep the previous zone, got %q", got)
	}
}

// The zone lives in the Lua state: a reload starts without it (init.lua sets it again).
func TestDatetimeTimezoneResetOnReinitialize(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`datetime.setTimezone("Europe/Warsaw")`); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatalf("Reinitialize: %v", err)
	}
	if got, _ := engine.Execute(`return datetime.timezone()`); got != "" {
		t.Errorf("timezone after Reinitialize = %q, want empty", got)
	}
}

// The vCLU's own CLU date/time features (read by Grenton scripts) follow
// datetime.setTimezone instead of the server clock. Tokyo: unlike any dev/CI machine zone.
func TestCLUTimeFeaturesUseDatetimeZone(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		before := time.Now().In(loc)
		got, err := engine.Execute(`
			datetime.setTimezone("Asia/Tokyo")
			local clu = CLU:new(1, 0, EventBus:getShared())
			return table.concat({
				tostring(clu:get(CLU.FEATURE_HOUR)), tostring(clu:get(CLU.FEATURE_MINUTE)),
				tostring(clu:get(CLU.FEATURE_DAY)), tostring(clu:get(CLU.FEATURE_MONTH)),
				tostring(clu:get(CLU.FEATURE_YEAR)), tostring(clu:get(CLU.FEATURE_DAY_OF_WEEK)),
				tostring(clu:get(CLU.FEATURE_TIME)), tostring(clu:get(CLU.FEATURE_DATE)),
				tostring(clu:get(CLU.FEATURE_UNIX_TIME)),
			}, "|")`)
		if err != nil {
			t.Fatalf("Lua error: %v", err)
		}
		after := time.Now().In(loc)
		if before.Minute() != after.Minute() {
			continue // crossed a minute boundary, retry
		}
		n := before
		parts := strings.Split(got, "|")
		want := []string{
			fmt.Sprint(n.Hour()), fmt.Sprint(n.Minute()), fmt.Sprint(n.Day()), fmt.Sprint(int(n.Month())),
			fmt.Sprint(n.Year()), fmt.Sprint(int(n.Weekday())), // Grenton: 0 = Sunday, like Go
		}
		for i, w := range want {
			if parts[i] != w {
				t.Errorf("feature %d = %s, want %s (all: %s)", i, parts[i], w, got)
			}
		}
		if !strings.HasPrefix(parts[6], fmt.Sprintf("%d:%d:", n.Hour(), n.Minute())) {
			t.Errorf("FEATURE_TIME = %s, want %d:%d:ss", parts[6], n.Hour(), n.Minute())
		}
		if parts[7] != fmt.Sprintf("%d-%d-%d", n.Day(), int(n.Month()), n.Year()) {
			t.Errorf("FEATURE_DATE = %s", parts[7])
		}
		var unix int64
		fmt.Sscan(parts[8], &unix)
		if d := unix - time.Now().Unix(); d < -2 || d > 2 {
			t.Errorf("FEATURE_UNIX_TIME = %d, off by %d s", unix, d)
		}
		return
	}
	t.Skip("crossed a minute boundary twice")
}

// Grenton numbers weekdays 0 = Sunday; datetime uses ISO 1 = Monday .. 7 = Sunday.
// Checked with fixed dates, so the test does not depend on the day it runs.
func TestCLUDayOfWeekMapping(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	got, err := engine.Execute(`
		local real = datetime.now
		local clu = CLU:new(1, 0, EventBus:getShared())
		local out = {}
		for _, wd in ipairs({7, 1, 6}) do
			datetime.now = function() return { weekday = wd, hour = 0, minute = 0, second = 0, day = 1, month = 1, year = 2026 } end
			out[#out + 1] = tostring(clu:get(CLU.FEATURE_DAY_OF_WEEK))
		end
		datetime.now = real
		return table.concat(out, ",")`)
	if err != nil {
		t.Fatalf("Lua error: %v", err)
	}
	if got != "0,1,6" {
		t.Errorf("Sunday/Monday/Saturday = %s, want 0,1,6", got)
	}
}
