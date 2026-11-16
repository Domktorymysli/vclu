// Package camera provides a snapshot proxy for IP cameras.
// Cameras are registered from Lua with URL + credentials.
// Go fetches snapshots (with Digest Auth if needed) and serves
// them via /api/camera/:id/snapshot — no credentials exposed to browser.
package camera

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/httpdigest"
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
	req, err := http.NewRequest("GET", cam.URL, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := httpdigest.Do(m.client, req, cam.Username, cam.Password)
	if err != nil {
		return nil, "", fmt.Errorf("digest auth request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("digest auth HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}

	return data, resp.Header.Get("Content-Type"), nil
}
