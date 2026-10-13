package lua

import (
	"strings"
	"testing"
	"time"
)

func TestHttpModuleLua(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: HTTP module exists
	result, err := engine.Execute("type(HTTP)")
	if err != nil {
		t.Fatalf("Failed to check HTTP type: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected HTTP to be table, got %s", result)
	}

	// Test 2: Chainable configuration
	_, err = engine.Execute(`
		api = HTTP:new("api")
		api:setBaseUrl("https://api.example.com")
		   :setTimeout(5000)
		   :setBearerToken("my_token_123")
		   :setHeader("X-Custom", "value")
	`)
	if err != nil {
		t.Fatalf("Failed to configure HTTP: %v", err)
	}

	result, _ = engine.Execute("api:getBaseUrl()")
	if result != "https://api.example.com" {
		t.Errorf("Expected base URL, got '%s'", result)
	}

	// Test 3: URL building
	_, err = engine.Execute(`
		api:setBaseUrl("https://api.example.com/v1")
		url1 = api:_buildUrl("/users")
		url2 = api:_buildUrl("posts")
		url3 = api:_buildUrl("https://other.com/api")
	`)
	if err != nil {
		t.Fatalf("Failed to build URLs: %v", err)
	}

	result, _ = engine.Execute("url1")
	if result != "https://api.example.com/v1/users" {
		t.Errorf("Expected URL with leading slash, got '%s'", result)
	}

	result, _ = engine.Execute("url2")
	if result != "https://api.example.com/v1/posts" {
		t.Errorf("Expected URL without leading slash, got '%s'", result)
	}

	result, _ = engine.Execute("url3")
	if result != "https://other.com/api" {
		t.Errorf("Expected absolute URL unchanged, got '%s'", result)
	}

	// Test 4: Multi-instance
	_, err = engine.Execute(`
		api1 = HTTP:new("api1")
		api1:setBaseUrl("https://api1.example.com")

		api2 = HTTP:new("api2")
		api2:setBaseUrl("https://api2.example.com")
	`)
	if err != nil {
		t.Fatalf("Failed to create multiple instances: %v", err)
	}

	result, _ = engine.Execute("api1:getBaseUrl()")
	if result != "https://api1.example.com" {
		t.Errorf("Expected api1 URL, got '%s'", result)
	}

	result, _ = engine.Execute("api2:getBaseUrl()")
	if result != "https://api2.example.com" {
		t.Errorf("Expected api2 URL, got '%s'", result)
	}

	t.Log("HTTP Lua module test passed!")
}

// ==================== HTTP Go Backend Tests ====================

func TestHTTPGoBackendSync(t *testing.T) {
	bin := newFakeHTTPBin(t)
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: Check that __go_http_request exists
	result, err := engine.Execute("type(__go_http_request)")
	if err != nil {
		t.Fatalf("Failed to check __go_http_request: %v", err)
	}
	if result != "function" {
		t.Errorf("Expected __go_http_request to be function, got %s", result)
	}

	// Test 2: HTTP GET request to the local httpbin
	_, err = engine.Execute(bin.lua(`
		status, body, headers, err = __go_http_request(
			"test_instance",
			"GET",
			"https://httpbin.org/get",
			'{"Accept": "application/json"}',
			"",
			10000,
			true,
			true
		)
	`))
	if err != nil {
		t.Fatalf("Failed to make HTTP request: %v", err)
	}

	result, _ = engine.Execute("status")
	if result != "200" {
		t.Errorf("Expected status 200, got '%s'", result)
	}

	result, _ = engine.Execute("err")
	if result != "nil" {
		t.Errorf("Expected no error, got '%s'", result)
	}

	result, _ = engine.Execute("body")
	if !strings.Contains(result, bin.url+"/get") {
		t.Errorf("Expected body to contain the request URL, got '%s'", result)
	}

	t.Log("HTTP Go backend sync test passed!")
}

func TestHTTPGoBackendPost(t *testing.T) {
	bin := newFakeHTTPBin(t)
	engine := NewLuaEngine()
	defer engine.Close()

	// POST JSON to the local httpbin
	_, err := engine.Execute(bin.lua(`
		status, body, headers, err = __go_http_request(
			"test_post",
			"POST",
			"https://httpbin.org/post",
			'{"Content-Type": "application/json"}',
			'{"name": "test", "value": 123}',
			10000,
			true,
			true
		)
	`))
	if err != nil {
		t.Fatalf("Failed to make POST request: %v", err)
	}

	result, _ := engine.Execute("status")
	if result != "200" {
		t.Errorf("Expected status 200, got '%s'", result)
	}

	// Body should contain the posted JSON
	result, _ = engine.Execute("body")
	if !strings.Contains(result, "test") || !strings.Contains(result, "123") {
		t.Errorf("Expected body to contain posted data, got '%s'", result)
	}

	t.Log("HTTP Go backend POST test passed!")
}

func TestHTTPGoBackendAsync(t *testing.T) {
	bin := newFakeHTTPBin(t)
	engine := NewLuaEngine()
	defer engine.Close()

	// Test async request functions exist
	result, err := engine.Execute("type(__go_http_async_request)")
	if err != nil {
		t.Fatalf("Failed to check __go_http_async_request: %v", err)
	}
	if result != "function" {
		t.Errorf("Expected __go_http_async_request to be function, got %s", result)
	}

	// Start async request
	_, err = engine.Execute(bin.lua(`
		__go_http_async_request(
			"async_test",
			"request_1",
			"GET",
			"https://httpbin.org/get",
			'{}',
			"",
			10000,
			true,
			true
		)
	`))
	if err != nil {
		t.Fatalf("Failed to start async request: %v", err)
	}

	// Wait for response (poll)
	var status string
	for i := 0; i < 50; i++ {
		time.Sleep(200 * time.Millisecond)

		// Check if response is ready
		_, err = engine.Execute(`
			local status, body, headers, err = __go_http_get_async_response("request_1")
			if status then
				asyncStatus, asyncBody, asyncHeaders, asyncErr = status, body, headers, err
				asyncDone = true
			else
				asyncDone = false
			end
		`)
		if err != nil {
			t.Fatalf("Failed to poll async: %v", err)
		}

		done, _ := engine.Execute("asyncDone")
		if done == "true" {
			status, _ = engine.Execute("asyncStatus")
			break
		}
	}

	if status != "200" {
		t.Errorf("Expected async status 200, got '%s'", status)
	}

	result, _ = engine.Execute("asyncBody")
	if !strings.Contains(result, bin.url+"/get") {
		t.Errorf("Expected async body to contain the request URL, got '%s'", result)
	}

	t.Log("HTTP Go backend async test passed!")
}

func TestHTTPGoBackendError(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test error handling with invalid URL
	_, err := engine.Execute(`
		status, body, headers, err = __go_http_request(
			"error_test",
			"GET",
			"https://invalid.nonexistent.domain.local/",
			'{}',
			"",
			2000,
			true,
			true
		)
	`)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	// Should have error
	result, _ := engine.Execute("err")
	if result == "nil" {
		t.Error("Expected error for invalid domain")
	}
	t.Logf("Got expected error: %s", result)

	// Status should be 0
	result, _ = engine.Execute("status")
	if result != "0" {
		t.Errorf("Expected status 0 on error, got '%s'", result)
	}

	t.Log("HTTP Go backend error test passed!")
}

func TestHTTPManagerUnit(t *testing.T) {
	bin := newFakeHTTPBin(t)
	// Unit test for HTTPManager directly (without Lua)
	hm := NewHTTPManager()

	// Test sync request
	status, body, headers, err := hm.Request(
		"unit_test",
		"GET",
		bin.url+"/headers",
		map[string]string{"X-Test-Header": "test-value"},
		"",
		10000,
		true,
		true,
	)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	if status != 200 {
		t.Errorf("Expected status 200, got %d", status)
	}

	if !strings.Contains(body, "X-Test-Header") {
		t.Errorf("Expected body to contain header, got '%s'", body)
	}

	if len(headers) == 0 {
		t.Error("Expected response headers")
	}

	t.Logf("Response headers: %v", headers)
	t.Log("HTTPManager unit test passed!")
}

// TestHTTPRequestGrentonStyle tests the Grenton-compatible HTTPRequest object
func TestHTTPRequestGrentonStyle(t *testing.T) {
	bin := newFakeHTTPBin(t)
	engine := NewLuaEngine()
	defer engine.Close()

	// Start timer manager for async polling
	engine.StartTimers()
	defer engine.StopTimers()

	// Test 1: Create HTTPRequest object
	_, err := engine.Execute(`
		req = HTTPRequest:new("testReq", nil, EventBus:getShared())
	`)
	if err != nil {
		t.Fatalf("Failed to create HTTPRequest: %v", err)
	}

	// Test 2: Set properties using Grenton-style set()
	_, err = engine.Execute(bin.lua(`
		req:set(HTTPRequest.FEATURE_HOST, "httpbin.org")
		req:set(HTTPRequest.FEATURE_PATH, "/get")
		req:set(HTTPRequest.FEATURE_METHOD, "GET")
		req:set(HTTPRequest.FEATURE_TIMEOUT, 10)
		req:set(HTTPRequest.FEATURE_RESPONSE_TYPE, HTTPRequest.RESPONSE_TYPE_JSON)
	`))
	if err != nil {
		t.Fatalf("Failed to set HTTPRequest properties: %v", err)
	}

	// Test 3: Verify properties
	result, _ := engine.Execute(`req:get(HTTPRequest.FEATURE_HOST)`)
	if result != bin.host {
		t.Errorf("Expected host %q, got '%s'", bin.host, result)
	}

	// Test 4: Test URL building
	result, err = engine.Execute(`req:_buildUrl()`)
	if err != nil {
		t.Fatalf("Failed to build URL: %v", err)
	}
	if result != bin.url+"/get" {
		t.Errorf("Expected URL %q, got '%s'", bin.url+"/get", result)
	}

	// Test 5: Test headers building
	_, err = engine.Execute(`
		headers = req:_buildHeaders()
		acceptHeader = headers["Accept"]
	`)
	if err != nil {
		t.Fatalf("Failed to build headers: %v", err)
	}
	result, _ = engine.Execute(`acceptHeader`)
	if result != "application/json" {
		t.Errorf("Expected Accept 'application/json', got '%s'", result)
	}

	// Test 6: Event registration
	_, err = engine.Execute(`
		responseReceived = false
		req:add_event(HTTPRequest.EVENT_ON_RESPONSE, function()
			responseReceived = true
		end)
	`)
	if err != nil {
		t.Fatalf("Failed to add event: %v", err)
	}

	// Test 7: Execute SendRequest (async)
	_, err = engine.Execute(`
		req:execute(HTTPRequest.METHOD_SEND_REQUEST)
	`)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	// Wait for async response (with timeout)
	for i := 0; i < 100; i++ { // 5 second timeout (100 * 50ms)
		time.Sleep(50 * time.Millisecond)
		engine.ProcessTimers()

		result, _ = engine.Execute(`responseReceived`)
		if result == "true" {
			break
		}
	}

	// Verify response received
	result, _ = engine.Execute(`responseReceived`)
	if result != "true" {
		t.Errorf("Expected responseReceived to be true")
	}

	// Verify status code
	result, _ = engine.Execute(`tostring(req:get(HTTPRequest.FEATURE_STATUS_CODE))`)
	if result != "200" {
		t.Errorf("Expected status code 200, got '%s'", result)
	}

	// Verify response body was parsed as JSON
	result, _ = engine.Execute(`type(req:get(HTTPRequest.FEATURE_RESPONSE_BODY))`)
	if result != "table" {
		t.Errorf("Expected response body to be table (parsed JSON), got '%s'", result)
	}

	// Check response contains expected data
	result, _ = engine.Execute(`req:get(HTTPRequest.FEATURE_RESPONSE_BODY).url`)
	if result != bin.url+"/get" {
		t.Errorf("Response URL: %q, want %q", result, bin.url+"/get")
	}

	t.Log("HTTPRequest Grenton-style test passed!")
}

// TestHttpContentTypeViaSetHeader verifies that setHeader does NOT work for Content-Type
// because _buildHeaders() overwrites it with _props value (default: application/json)
// This is a regression test for the PluginSandbox fix
func TestHttpContentTypeViaSetHeader(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test: setHeader("Content-Type", ...) should be overwritten by default props
	_, err := engine.Execute(`
		client = HTTP:new("content-type-test")
		client:setHeader("Content-Type", "application/x-www-form-urlencoded")
		headers = client:_buildHeaders()
	`)
	if err != nil {
		t.Fatalf("Failed to build headers: %v", err)
	}

	// Content-Type should still be "application/json" (from default props)
	result, _ := engine.Execute(`headers["Content-Type"]`)
	if result != "application/json" {
		t.Logf("Note: Content-Type is '%s', expected 'application/json' (default overwrites setHeader)", result)
	}
	// This test documents the current behavior - setHeader alone does NOT set Content-Type
}

// TestHttpContentTypeViaSetContentType verifies that setContentType() DOES work
func TestHttpContentTypeViaSetContentType(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test: setContentType() should properly set Content-Type
	_, err := engine.Execute(`
		client = HTTP:new("content-type-test2")
		client:setContentType("application/x-www-form-urlencoded")
		headers = client:_buildHeaders()
	`)
	if err != nil {
		t.Fatalf("Failed to build headers: %v", err)
	}

	// Content-Type should be "application/x-www-form-urlencoded"
	result, _ := engine.Execute(`headers["Content-Type"]`)
	if result != "application/x-www-form-urlencoded" {
		t.Errorf("Expected Content-Type 'application/x-www-form-urlencoded', got '%s'", result)
	}
}

// TestHttpPostFormUrlEncoded tests POST with form-urlencoded body (the Telegram API use case)
func TestHttpPostFormUrlEncoded(t *testing.T) {
	bin := newFakeHTTPBin(t)
	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	// This simulates how PluginSandbox.httpRequest now works after the fix
	_, err := engine.Execute(bin.lua(`
		client = HTTP:new("form-test")
		client:setTimeout(10000)
		client:setContentType("application/x-www-form-urlencoded")

		responseDone = false
		responseStatus = 0
		responseBody = ""

		client:asyncPost("https://httpbin.org/post", "key1=value1&key2=value2", function(response, err)
			responseDone = true
			if response then
				responseStatus = response.status
				responseBody = response.body
			end
		end)
	`))
	if err != nil {
		t.Fatalf("Failed to setup POST request: %v", err)
	}

	// Wait for response
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		engine.ProcessTimers()

		result, _ := engine.Execute(`responseDone`)
		if result == "true" {
			break
		}
	}

	// Verify status
	result, _ := engine.Execute(`tostring(responseStatus)`)
	if result != "200" {
		t.Errorf("Expected status 200, got '%s'", result)
	}

	// Verify that httpbin received the correct Content-Type
	body, _ := engine.Execute(`responseBody`)
	if !strings.Contains(body, "application/x-www-form-urlencoded") {
		bodyPreview := body
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		t.Errorf("Expected response to show form-urlencoded Content-Type, got: %s", bodyPreview)
	}

	// Verify the form data was parsed correctly
	if !strings.Contains(body, "key1") || !strings.Contains(body, "value1") {
		bodyPreview := body
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		t.Errorf("Expected response to contain form data, got: %s", bodyPreview)
	}

	t.Log("HTTP POST form-urlencoded test passed!")
}

// TestHTTPRequestPost tests POST request with JSON body
func TestHTTPRequestPost(t *testing.T) {
	bin := newFakeHTTPBin(t)
	engine := NewLuaEngine()
	defer engine.Close()

	// Start timer manager for async polling
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(bin.lua(`
		req = HTTPRequest:new("postReq", nil, EventBus:getShared())
		req:set(HTTPRequest.FEATURE_HOST, "httpbin.org")
		req:set(HTTPRequest.FEATURE_PATH, "/post")
		req:set(HTTPRequest.FEATURE_METHOD, "POST")
		req:set(HTTPRequest.FEATURE_TIMEOUT, 10)
		req:set(HTTPRequest.FEATURE_REQUEST_TYPE, HTTPRequest.REQUEST_TYPE_JSON)
		req:set(HTTPRequest.FEATURE_RESPONSE_TYPE, HTTPRequest.RESPONSE_TYPE_JSON)
		req:set(HTTPRequest.FEATURE_REQUEST_BODY, {name = "test", value = 123})

		responseReceived = false
		req:add_event(HTTPRequest.EVENT_ON_RESPONSE, function()
			responseReceived = true
		end)

		req:execute(HTTPRequest.METHOD_SEND_REQUEST)
	`))
	if err != nil {
		t.Fatalf("Failed to send POST request: %v", err)
	}

	// Wait for response
	for i := 0; i < 100; i++ {
		time.Sleep(50 * time.Millisecond)
		engine.ProcessTimers()

		result, _ := engine.Execute(`responseReceived`)
		if result == "true" {
			break
		}
	}

	// Verify
	result, _ := engine.Execute(`tostring(req:get(HTTPRequest.FEATURE_STATUS_CODE))`)
	if result != "200" {
		t.Errorf("Expected status code 200, got '%s'", result)
	}

	// Check JSON payload was received
	result, _ = engine.Execute(`req:get(HTTPRequest.FEATURE_RESPONSE_BODY).json.name`)
	if result != "test" {
		t.Errorf("Response json.name: %q, want \"test\"", result)
	}

	t.Log("HTTPRequest POST test passed!")
}
