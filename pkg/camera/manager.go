// Package camera provides a snapshot proxy for IP cameras.
// Cameras are registered from Lua with URL + credentials.
// Go fetches snapshots (with Digest Auth if needed) and serves
// them via /api/camera/:id/snapshot — no credentials exposed to browser.
package camera

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Camera holds the config for a single camera.
type Camera struct {
	ID       string
	URL      string
	Auth     string // "digest", "basic", or "" (none)
	Username string
	Password string
}

// CachedSnapshot holds a fetched image with expiry.
type CachedSnapshot struct {
	Data        []byte
	ContentType string
	FetchedAt   time.Time
}

// Manager manages camera registrations and snapshot fetching.
type Manager struct {
	mu       sync.RWMutex
	cameras  map[string]*Camera
	cache    map[string]*CachedSnapshot
	cacheTTL time.Duration
	client   *http.Client
}

// NewManager creates a new camera manager.
func NewManager() *Manager {
	return &Manager{
		cameras:  make(map[string]*Camera),
		cache:    make(map[string]*CachedSnapshot),
		cacheTTL: 3 * time.Second,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

// Register adds or updates a camera.
func (m *Manager) Register(cam *Camera) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cameras[cam.ID] = cam
	delete(m.cache, cam.ID) // invalidate cache
	log.Printf("[CAMERA] registered: %s (%s auth)", cam.ID, cam.Auth)
}

// Unregister removes a camera.
func (m *Manager) Unregister(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cameras, id)
	delete(m.cache, id)
}

// Clear removes all cameras (called on Lua reload).
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cameras = make(map[string]*Camera)
	m.cache = make(map[string]*CachedSnapshot)
}

// List returns all registered camera IDs.
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.cameras))
	for id := range m.cameras {
		ids = append(ids, id)
	}
	return ids
}

// GetSnapshot returns the cached or freshly fetched snapshot for a camera.
func (m *Manager) GetSnapshot(id string) ([]byte, string, error) {
	m.mu.RLock()
	cam, ok := m.cameras[id]
	if !ok {
		m.mu.RUnlock()
		return nil, "", fmt.Errorf("camera not found: %s", id)
	}

	// Check cache
	if cached, ok := m.cache[id]; ok && time.Since(cached.FetchedAt) < m.cacheTTL {
		m.mu.RUnlock()
		return cached.Data, cached.ContentType, nil
	}
	m.mu.RUnlock()

	// Fetch fresh snapshot
	data, contentType, err := m.fetchSnapshot(cam)
	if err != nil {
		return nil, "", err
	}

	// Cache it
	m.mu.Lock()
	m.cache[id] = &CachedSnapshot{
		Data:        data,
		ContentType: contentType,
		FetchedAt:   time.Now(),
	}
	m.mu.Unlock()

	return data, contentType, nil
}

// fetchSnapshot fetches a snapshot from the camera, handling auth.
func (m *Manager) fetchSnapshot(cam *Camera) ([]byte, string, error) {
	switch cam.Auth {
	case "digest":
		return m.fetchDigest(cam)
	case "basic":
		return m.fetchBasic(cam)
	default:
		return m.fetchSimple(cam.URL)
	}
}

func (m *Manager) fetchSimple(url string) ([]byte, string, error) {
	resp, err := m.client.Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}

	return data, resp.Header.Get("Content-Type"), nil
}

func (m *Manager) fetchBasic(cam *Camera) ([]byte, string, error) {
	req, err := http.NewRequest("GET", cam.URL, nil)
	if err != nil {
		return nil, "", err
	}
	req.SetBasicAuth(cam.Username, cam.Password)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}

	return data, resp.Header.Get("Content-Type"), nil
}

func (m *Manager) fetchDigest(cam *Camera) ([]byte, string, error) {
	// Step 1: Initial request to get WWW-Authenticate challenge
	req, err := http.NewRequest("GET", cam.URL, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("digest challenge failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		// No auth required — re-fetch normally
		return m.fetchSimple(cam.URL)
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		return nil, "", fmt.Errorf("401 but no WWW-Authenticate header")
	}

	// Step 2: Parse challenge and build digest response
	params := parseDigestChallenge(wwwAuth)
	realm := params["realm"]
	nonce := params["nonce"]
	qop := params["qop"]
	opaque := params["opaque"]

	// Generate cnonce
	cnonceBytes := make([]byte, 8)
	rand.Read(cnonceBytes)
	cnonce := hex.EncodeToString(cnonceBytes)
	nc := "00000001"

	// Extract URI from URL
	uri := extractURI(cam.URL)

	// Compute digest
	ha1 := md5hex(cam.Username + ":" + realm + ":" + cam.Password)
	ha2 := md5hex("GET:" + uri)

	var response string
	if qop == "auth" || strings.Contains(qop, "auth") {
		response = md5hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	} else {
		response = md5hex(ha1 + ":" + nonce + ":" + ha2)
	}

	// Step 3: Authenticated request
	req2, err := http.NewRequest("GET", cam.URL, nil)
	if err != nil {
		return nil, "", err
	}

	authHeader := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		cam.Username, realm, nonce, uri, response)
	if qop != "" {
		authHeader += fmt.Sprintf(`, qop=auth, nc=%s, cnonce="%s"`, nc, cnonce)
	}
	if opaque != "" {
		authHeader += fmt.Sprintf(`, opaque="%s"`, opaque)
	}
	req2.Header.Set("Authorization", authHeader)

	resp2, err := m.client.Do(req2)
	if err != nil {
		return nil, "", fmt.Errorf("digest auth request failed: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("digest auth HTTP %d", resp2.StatusCode)
	}

	data, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}

	return data, resp2.Header.Get("Content-Type"), nil
}

// parseDigestChallenge parses WWW-Authenticate: Digest realm="...", nonce="...", ...
func parseDigestChallenge(header string) map[string]string {
	params := make(map[string]string)
	header = strings.TrimPrefix(header, "Digest ")
	header = strings.TrimPrefix(header, "digest ")

	// Simple parser: split by comma, then by =
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		val = strings.Trim(val, `"`)
		params[key] = val
	}

	return params
}

// extractURI extracts the path+query from a URL string.
func extractURI(rawURL string) string {
	// Find :// then next /
	idx := strings.Index(rawURL, "://")
	if idx < 0 {
		return rawURL
	}
	rest := rawURL[idx+3:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "/"
	}
	return rest[slash:]
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}
