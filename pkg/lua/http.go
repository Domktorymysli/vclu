package lua

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// HTTPManager manages HTTP client instances for Lua
type HTTPManager struct {
	mu             sync.RWMutex
	clients        map[string]*http.Client
	asyncResponses map[string]*asyncHTTPResponse
	asyncMu        sync.Mutex
	pendingAsync   chan string         // channel for completed async request IDs
	onRequest      func(hasError bool) // callback for metrics
}

// asyncHTTPResponse holds the response from an async HTTP request
type asyncHTTPResponse struct {
	Status  int
	Body    string
	Headers map[string]string
	Error   string
}

// NewHTTPManager creates a new HTTP manager
func NewHTTPManager() *HTTPManager {
	return &HTTPManager{
		clients:        make(map[string]*http.Client),
		asyncResponses: make(map[string]*asyncHTTPResponse),
		pendingAsync:   make(chan string, 100),
	}
}

// SetOnRequest sets the callback for HTTP request metrics
func (hm *HTTPManager) SetOnRequest(fn func(hasError bool)) {
	hm.mu.Lock()
	hm.onRequest = fn
	hm.mu.Unlock()
}

// getOrCreateClient gets or creates an HTTP client for the given instance
func (hm *HTTPManager) getOrCreateClient(instanceID string, timeout int, verifySSL, followRedirects bool) *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Generate cache key based on settings
	cacheKey := fmt.Sprintf("%s_%d_%v_%v", instanceID, timeout, verifySSL, followRedirects)

	// Return existing client if available
	if client, ok := hm.clients[cacheKey]; ok {
		return client
	}

	// Create new client with specified settings
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !verifySSL,
		},
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     30 * time.Second,
	}

	client := &http.Client{
		Timeout:   time.Duration(timeout) * time.Millisecond,
		Transport: transport,
	}

	if !followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	hm.clients[cacheKey] = client
	return client
}

// Request performs a synchronous HTTP request
func (hm *HTTPManager) Request(instanceID, method, url string, headers map[string]string, body string, timeout int, verifySSL, followRedirects bool) (int, string, map[string]string, error) {
	client := hm.getOrCreateClient(instanceID, timeout, verifySSL, followRedirects)

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		hm.recordRequest(true)
		return 0, "", nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// Perform request
	resp, err := client.Do(req)
	if err != nil {
		hm.recordRequest(true)
		return 0, "", nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		hm.recordRequest(true)
		return resp.StatusCode, "", nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Extract response headers
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	hm.recordRequest(false)
	return resp.StatusCode, string(respBody), respHeaders, nil
}

// recordRequest calls the metrics callback if set
func (hm *HTTPManager) recordRequest(hasError bool) {
	hm.mu.RLock()
	fn := hm.onRequest
	hm.mu.RUnlock()
	if fn != nil {
		fn(hasError)
	}
}

// AsyncRequest performs an asynchronous HTTP request
func (hm *HTTPManager) AsyncRequest(instanceID, requestID, method, url string, headers map[string]string, body string, timeout int, verifySSL, followRedirects bool) {
	go func() {
		status, respBody, respHeaders, err := hm.Request(instanceID, method, url, headers, body, timeout, verifySSL, followRedirects)

		hm.asyncMu.Lock()
		if err != nil {
			hm.asyncResponses[requestID] = &asyncHTTPResponse{
				Status: 0,
				Error:  err.Error(),
			}
		} else {
			hm.asyncResponses[requestID] = &asyncHTTPResponse{
				Status:  status,
				Body:    respBody,
				Headers: respHeaders,
			}
		}
		hm.asyncMu.Unlock()

		// Signal completion
		select {
		case hm.pendingAsync <- requestID:
		default:
			log.Printf("[HTTP] Warning: async response channel full, dropping request %s", requestID)
		}
	}()
}

// GetAsyncResponse retrieves and removes an async response
func (hm *HTTPManager) GetAsyncResponse(requestID string) *asyncHTTPResponse {
	hm.asyncMu.Lock()
	defer hm.asyncMu.Unlock()

	resp, ok := hm.asyncResponses[requestID]
	if ok {
		delete(hm.asyncResponses, requestID)
	}
	return resp
}

// GetPendingAsyncID returns a pending async request ID (non-blocking)
func (hm *HTTPManager) GetPendingAsyncID() string {
	select {
	case id := <-hm.pendingAsync:
		return id
	default:
		return ""
	}
}

// registerHTTPFunctions registers Go HTTP functions in Lua
func registerHTTPFunctions(L *lua.LState, hm *HTTPManager) {
	// __go_http_request(instanceId, method, url, headersJson, body, timeout, verifySSL, followRedirects)
	// Returns: status, body, headersJson, error
	L.SetGlobal("__go_http_request", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		method := L.CheckString(2)
		url := L.CheckString(3)
		headersJSON := L.OptString(4, "{}")
		body := L.OptString(5, "")
		timeout := L.OptInt(6, 30000)
		verifySSL := L.OptBool(7, true)
		followRedirects := L.OptBool(8, true)

		// Parse headers JSON
		var headers map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			headers = make(map[string]string)
		}

		status, respBody, respHeaders, err := hm.Request(instanceID, method, url, headers, body, timeout, verifySSL, followRedirects)

		L.Push(lua.LNumber(status))
		L.Push(lua.LString(respBody))

		// Convert response headers to JSON
		respHeadersJSON, _ := json.Marshal(respHeaders)
		L.Push(lua.LString(string(respHeadersJSON)))

		if err != nil {
			L.Push(lua.LString(err.Error()))
		} else {
			L.Push(lua.LNil)
		}

		return 4
	}))

	// __go_http_async_request(instanceId, requestId, method, url, headersJson, body, timeout, verifySSL, followRedirects)
	// Returns immediately, result retrieved later via __go_http_get_async_response
	L.SetGlobal("__go_http_async_request", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		requestID := L.CheckString(2)
		method := L.CheckString(3)
		url := L.CheckString(4)
		headersJSON := L.OptString(5, "{}")
		body := L.OptString(6, "")
		timeout := L.OptInt(7, 30000)
		verifySSL := L.OptBool(8, true)
		followRedirects := L.OptBool(9, true)

		// Parse headers JSON
		var headers map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			headers = make(map[string]string)
		}

		hm.AsyncRequest(instanceID, requestID, method, url, headers, body, timeout, verifySSL, followRedirects)

		return 0
	}))

	// __go_http_get_async_response(requestId)
	// Returns: status, body, headersJson, error (or nil if not ready)
	L.SetGlobal("__go_http_get_async_response", L.NewFunction(func(L *lua.LState) int {
		requestID := L.CheckString(1)

		resp := hm.GetAsyncResponse(requestID)
		if resp == nil {
			L.Push(lua.LNil)
			return 1
		}

		L.Push(lua.LNumber(resp.Status))
		L.Push(lua.LString(resp.Body))

		respHeadersJSON, _ := json.Marshal(resp.Headers)
		L.Push(lua.LString(string(respHeadersJSON)))

		if resp.Error != "" {
			L.Push(lua.LString(resp.Error))
		} else {
			L.Push(lua.LNil)
		}

		return 4
	}))

	// __go_http_poll_async() - returns requestId of completed async request (or nil)
	L.SetGlobal("__go_http_poll_async", L.NewFunction(func(L *lua.LState) int {
		id := hm.GetPendingAsyncID()
		if id == "" {
			L.Push(lua.LNil)
		} else {
			L.Push(lua.LString(id))
		}
		return 1
	}))
}
