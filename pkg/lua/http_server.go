package lua

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	glua "github.com/yuin/gopher-lua"
)

// HTTPServerManager manages the embedded HTTP server for Lua
type HTTPServerManager struct {
	mu           sync.RWMutex
	server       *http.Server
	config       HTTPServerConfig
	endpoints    map[string]*HTTPEndpoint
	requestQueue chan *HTTPServerRequest
	responses    map[string]chan *HTTPServerResponse
	responsesMu  sync.Mutex
	running      bool
	nextReqID    int
	onRequest    func(hasError bool) // callback for metrics
}

// HTTPServerConfig holds server configuration
type HTTPServerConfig struct {
	Port     int
	Username string
	Password string
}

// HTTPEndpoint represents a registered endpoint
type HTTPEndpoint struct {
	Path         string
	Methods      map[string]bool
	CallbackName string
	Username     string
	Password     string
	Public       bool
}

// HTTPServerRequest represents an incoming HTTP request
type HTTPServerRequest struct {
	ID         string            `json:"id"`
	Path       string            `json:"path"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Query      map[string]string `json:"query"`
	RemoteAddr string            `json:"remoteAddr"`
}

// HTTPServerResponse represents a response to send
type HTTPServerResponse struct {
	Status  int               `json:"status"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
}

// NewHTTPServerManager creates a new HTTP server manager
func NewHTTPServerManager() *HTTPServerManager {
	return &HTTPServerManager{
		endpoints:    make(map[string]*HTTPEndpoint),
		requestQueue: make(chan *HTTPServerRequest, 100),
		responses:    make(map[string]chan *HTTPServerResponse),
	}
}

// SetOnRequest sets the callback for HTTP request metrics
func (sm *HTTPServerManager) SetOnRequest(fn func(hasError bool)) {
	sm.mu.Lock()
	sm.onRequest = fn
	sm.mu.Unlock()
}

// Start starts the HTTP server on the specified port
func (sm *HTTPServerManager) Start(port int, username, password string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.running {
		return fmt.Errorf("server already running")
	}

	sm.config = HTTPServerConfig{
		Port:     port,
		Username: username,
		Password: password,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", sm.handleRequest)

	sm.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("[HTTP-SERVER] Starting on port %d", port)
		if err := sm.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[HTTP-SERVER] Error: %v", err)
		}
	}()

	sm.running = true
	return nil
}

// Stop stops the HTTP server
func (sm *HTTPServerManager) Stop() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.running {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if sm.server != nil {
		if err := sm.server.Shutdown(ctx); err != nil {
			log.Printf("[HTTP-SERVER] Shutdown error: %v", err)
		}
	}

	sm.running = false
	sm.endpoints = make(map[string]*HTTPEndpoint)
	log.Printf("[HTTP-SERVER] Stopped")
	return nil
}

// IsRunning returns whether the server is running
func (sm *HTTPServerManager) IsRunning() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.running
}

// AddEndpoint registers an endpoint
func (sm *HTTPServerManager) AddEndpoint(path, methods, callbackName, username, password string, public bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	methodMap := make(map[string]bool)
	for _, m := range strings.Split(methods, ",") {
		methodMap[strings.TrimSpace(strings.ToUpper(m))] = true
	}

	sm.endpoints[path] = &HTTPEndpoint{
		Path:         path,
		Methods:      methodMap,
		CallbackName: callbackName,
		Username:     username,
		Password:     password,
		Public:       public,
	}

	log.Printf("[HTTP-SERVER] Registered endpoint: %s [%s]", path, methods)
}

// RemoveEndpoint removes an endpoint
func (sm *HTTPServerManager) RemoveEndpoint(path string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.endpoints, path)
	log.Printf("[HTTP-SERVER] Removed endpoint: %s", path)
}

// handleRequest handles incoming HTTP requests
func (sm *HTTPServerManager) handleRequest(w http.ResponseWriter, r *http.Request) {
	sm.mu.RLock()
	endpoint, exists := sm.endpoints[r.URL.Path]
	config := sm.config
	sm.mu.RUnlock()

	// Check if endpoint exists
	if !exists {
		http.NotFound(w, r)
		sm.recordRequest(true)
		return
	}

	// Check method
	if !endpoint.Methods[r.Method] {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		sm.recordRequest(true)
		return
	}

	// Check auth
	if !sm.checkAuth(r, endpoint, config) {
		w.Header().Set("WWW-Authenticate", `Basic realm="vCLU"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		sm.recordRequest(true)
		return
	}

	// Read body
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		sm.recordRequest(true)
		return
	}

	// Build headers map
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	// Build query map
	query := make(map[string]string)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	// Create request object
	sm.mu.Lock()
	sm.nextReqID++
	reqID := fmt.Sprintf("req_%d_%d", time.Now().UnixNano(), sm.nextReqID)
	sm.mu.Unlock()

	req := &HTTPServerRequest{
		ID:         reqID,
		Path:       r.URL.Path,
		Method:     r.Method,
		Headers:    headers,
		Body:       string(body),
		Query:      query,
		RemoteAddr: r.RemoteAddr,
	}

	// Create response channel
	respChan := make(chan *HTTPServerResponse, 1)
	sm.responsesMu.Lock()
	sm.responses[reqID] = respChan
	sm.responsesMu.Unlock()

	// Queue request for Lua processing
	select {
	case sm.requestQueue <- req:
		// Queued successfully
	default:
		log.Printf("[HTTP-SERVER] Request queue full, rejecting request")
		http.Error(w, "Server Busy", http.StatusServiceUnavailable)
		sm.responsesMu.Lock()
		delete(sm.responses, reqID)
		sm.responsesMu.Unlock()
		sm.recordRequest(true)
		return
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		// Set headers
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.Status)
		w.Write([]byte(resp.Body))
		sm.recordRequest(false)

	case <-time.After(30 * time.Second):
		http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
		sm.recordRequest(true)
	}

	// Cleanup
	sm.responsesMu.Lock()
	delete(sm.responses, reqID)
	sm.responsesMu.Unlock()
}

// checkAuth checks Basic Auth credentials
func (sm *HTTPServerManager) checkAuth(r *http.Request, ep *HTTPEndpoint, config HTTPServerConfig) bool {
	// Public endpoint - skip auth
	if ep.Public {
		return true
	}

	// Determine which credentials to use
	username, password := config.Username, config.Password
	if ep.Username != "" {
		username, password = ep.Username, ep.Password
	}

	// No auth configured - allow
	if username == "" {
		return true
	}

	// Check Basic Auth header
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1
}

// GetPendingRequest returns next pending request (non-blocking)
func (sm *HTTPServerManager) GetPendingRequest() *HTTPServerRequest {
	select {
	case req := <-sm.requestQueue:
		return req
	default:
		return nil
	}
}

// Respond sends a response for a request
func (sm *HTTPServerManager) Respond(reqID string, status int, body string, headers map[string]string) bool {
	sm.responsesMu.Lock()
	respChan, exists := sm.responses[reqID]
	sm.responsesMu.Unlock()

	if !exists {
		return false
	}

	resp := &HTTPServerResponse{
		Status:  status,
		Body:    body,
		Headers: headers,
	}

	select {
	case respChan <- resp:
		return true
	default:
		return false
	}
}

// GetEndpointCallback returns the callback name for an endpoint
func (sm *HTTPServerManager) GetEndpointCallback(path string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if ep, ok := sm.endpoints[path]; ok {
		return ep.CallbackName
	}
	return ""
}

// recordRequest calls the metrics callback if set
func (sm *HTTPServerManager) recordRequest(hasError bool) {
	sm.mu.RLock()
	fn := sm.onRequest
	sm.mu.RUnlock()
	if fn != nil {
		fn(hasError)
	}
}

// registerHTTPServerFunctions registers Go HTTP server functions in Lua
func registerHTTPServerFunctions(L *glua.LState, sm *HTTPServerManager) {
	// __go_http_server_start(port, username, password) -> success, error
	L.SetGlobal("__go_http_server_start", L.NewFunction(func(L *glua.LState) int {
		port := L.OptInt(1, 8081)
		username := L.OptString(2, "")
		password := L.OptString(3, "")

		err := sm.Start(port, username, password)
		if err != nil {
			L.Push(glua.LFalse)
			L.Push(glua.LString(err.Error()))
			return 2
		}

		L.Push(glua.LTrue)
		L.Push(glua.LNil)
		return 2
	}))

	// __go_http_server_stop()
	L.SetGlobal("__go_http_server_stop", L.NewFunction(func(L *glua.LState) int {
		sm.Stop()
		return 0
	}))

	// __go_http_server_is_running() -> bool
	L.SetGlobal("__go_http_server_is_running", L.NewFunction(func(L *glua.LState) int {
		L.Push(glua.LBool(sm.IsRunning()))
		return 1
	}))

	// __go_http_server_add_endpoint(path, methods, callback, username, password, public)
	L.SetGlobal("__go_http_server_add_endpoint", L.NewFunction(func(L *glua.LState) int {
		path := L.CheckString(1)
		methods := L.CheckString(2)
		callback := L.CheckString(3)
		username := L.OptString(4, "")
		password := L.OptString(5, "")
		public := L.OptBool(6, false)

		sm.AddEndpoint(path, methods, callback, username, password, public)
		return 0
	}))

	// __go_http_server_remove_endpoint(path)
	L.SetGlobal("__go_http_server_remove_endpoint", L.NewFunction(func(L *glua.LState) int {
		path := L.CheckString(1)
		sm.RemoveEndpoint(path)
		return 0
	}))

	// __go_http_server_poll() -> request JSON or nil
	L.SetGlobal("__go_http_server_poll", L.NewFunction(func(L *glua.LState) int {
		req := sm.GetPendingRequest()
		if req == nil {
			L.Push(glua.LNil)
			return 1
		}

		// Return as JSON string for easy parsing in Lua
		jsonBytes, err := json.Marshal(req)
		if err != nil {
			L.Push(glua.LNil)
			return 1
		}

		L.Push(glua.LString(string(jsonBytes)))
		return 1
	}))

	// __go_http_server_respond(reqId, status, body, headersJson)
	L.SetGlobal("__go_http_server_respond", L.NewFunction(func(L *glua.LState) int {
		reqID := L.CheckString(1)
		status := L.OptInt(2, 200)
		body := L.OptString(3, "")
		headersJSON := L.OptString(4, "{}")

		var headers map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			headers = make(map[string]string)
		}

		success := sm.Respond(reqID, status, body, headers)
		L.Push(glua.LBool(success))
		return 1
	}))

	// __go_http_server_get_callback(path) -> callback name or nil
	L.SetGlobal("__go_http_server_get_callback", L.NewFunction(func(L *glua.LState) int {
		path := L.CheckString(1)
		callback := sm.GetEndpointCallback(path)
		if callback == "" {
			L.Push(glua.LNil)
			return 1
		}
		L.Push(glua.LString(callback))
		return 1
	}))
}
