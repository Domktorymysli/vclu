package plugins

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// KVEntry represents a single key-value entry with metadata
type KVEntry struct {
	Value    interface{} `json:"v"`
	Secure   bool        `json:"secure,omitempty"`
	TTL      int64       `json:"ttl,omitempty"`     // TTL in seconds (0 = permanent)
	ExpireAt int64       `json:"exp,omitempty"`     // Unix timestamp when entry expires
	Created  int64       `json:"created,omitempty"` // Unix timestamp when created
}

// KVStoreMeta represents metadata for the store file
type KVStoreMeta struct {
	Version int   `json:"version"`
	Updated int64 `json:"updated"`
}

// KVStoreFile represents the JSON file structure
type KVStoreFile struct {
	Meta    KVStoreMeta         `json:"_meta"`
	Entries map[string]*KVEntry `json:"entries"`
}

// PluginKVStore manages key-value storage for a single plugin
type PluginKVStore struct {
	pluginID string
	filePath string
	data     map[string]*KVEntry
	mu       sync.RWMutex
}

// KVStoreManager manages KV stores for all plugins
type KVStoreManager struct {
	baseDir string
	stores  map[string]*PluginKVStore
	mu      sync.RWMutex
}

// NewKVStoreManager creates a new KV store manager
func NewKVStoreManager(baseDir string) *KVStoreManager {
	kvDir := filepath.Join(baseDir, "kv")

	// Ensure KV directory exists
	if err := os.MkdirAll(kvDir, 0755); err != nil {
		log.Printf("[KV] Warning: Failed to create KV directory: %v", err)
	}

	return &KVStoreManager{
		baseDir: kvDir,
		stores:  make(map[string]*PluginKVStore),
	}
}

// GetStore returns the KV store for a specific plugin (creates if not exists)
func (m *KVStoreManager) GetStore(pluginID string) *PluginKVStore {
	m.mu.Lock()
	defer m.mu.Unlock()

	if store, exists := m.stores[pluginID]; exists {
		return store
	}

	// Create new store
	store := m.createStore(pluginID)
	m.stores[pluginID] = store
	return store
}

// createStore creates a new plugin KV store (internal, no lock)
func (m *KVStoreManager) createStore(pluginID string) *PluginKVStore {
	// Sanitize plugin ID for filename
	safeID := sanitizePluginID(pluginID)
	filePath := filepath.Join(m.baseDir, safeID+".json")

	store := &PluginKVStore{
		pluginID: pluginID,
		filePath: filePath,
		data:     make(map[string]*KVEntry),
	}

	// Load existing data if file exists
	if err := store.load(); err != nil {
		log.Printf("[KV] Warning: Failed to load store for %s: %v", pluginID, err)
	}

	return store
}

// sanitizePluginID converts plugin ID to safe filename
// @vclu/weather -> vclu--weather
func sanitizePluginID(pluginID string) string {
	s := strings.TrimPrefix(pluginID, "@")
	s = strings.ReplaceAll(s, "/", "--")
	s = strings.ReplaceAll(s, "\\", "--")
	return s
}

// load reads the store from disk
func (s *PluginKVStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file yet, start empty
		}
		return fmt.Errorf("failed to read file: %w", err)
	}

	var file KVStoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if file.Entries != nil {
		s.data = file.Entries
	}

	log.Printf("[KV] Loaded %d keys for plugin %s", len(s.data), s.pluginID)
	return nil
}

// save writes the store to disk (atomic write)
func (s *PluginKVStore) save() error {
	// No lock here - caller must hold lock

	file := KVStoreFile{
		Meta: KVStoreMeta{
			Version: 1,
			Updated: time.Now().Unix(),
		},
		Entries: s.data,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Atomic write: write to temp file, then rename.
	// 0600 — plugins store credentials here (API tokens etc.); the rename
	// also fixes permissions of stores created by older versions.
	tempPath := s.filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempPath, s.filePath); err != nil {
		os.Remove(tempPath) // Cleanup on failure
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// isExpired checks if an entry has expired
func (e *KVEntry) isExpired() bool {
	if e.ExpireAt == 0 {
		return false // No expiration
	}
	return time.Now().Unix() > e.ExpireAt
}

// Get retrieves a value from the store
// Returns (value, exists)
func (s *PluginKVStore) Get(key string) (interface{}, bool) {
	s.mu.RLock()
	entry, exists := s.data[key]
	s.mu.RUnlock()

	if !exists {
		return nil, false
	}

	// Check expiration (lazy expiration)
	if entry.isExpired() {
		// Remove expired entry
		s.Delete(key)
		return nil, false
	}

	return entry.Value, true
}

// Set stores a value in the store
func (s *PluginKVStore) Set(key string, value interface{}, secure bool, ttl int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()

	entry := &KVEntry{
		Value:   value,
		Secure:  secure,
		TTL:     ttl,
		Created: now,
	}

	if ttl > 0 {
		entry.ExpireAt = now + ttl
	}

	s.data[key] = entry

	// Log (redact secure values)
	if secure {
		log.Printf("[KV] Set %s.%s = [REDACTED] (secure, ttl=%d)", s.pluginID, key, ttl)
	} else {
		log.Printf("[KV] Set %s.%s (ttl=%d)", s.pluginID, key, ttl)
	}

	return s.save()
}

// Delete removes a key from the store
func (s *PluginKVStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data[key]; !exists {
		return nil // Already deleted
	}

	delete(s.data, key)
	log.Printf("[KV] Delete %s.%s", s.pluginID, key)

	return s.save()
}

// Has checks if a key exists (and is not expired)
func (s *PluginKVStore) Has(key string) bool {
	_, exists := s.Get(key) // Uses Get to handle expiration
	return exists
}

// List returns all keys matching a prefix
func (s *PluginKVStore) List(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	now := time.Now().Unix()

	for key, entry := range s.data {
		// Skip expired entries
		if entry.ExpireAt > 0 && now > entry.ExpireAt {
			continue
		}

		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}

	return keys
}

// GetAll returns all non-expired entries
// Secure values are replaced with "[SECURE]" placeholder
func (s *PluginKVStore) GetAll() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]interface{})
	now := time.Now().Unix()

	for key, entry := range s.data {
		// Skip expired entries
		if entry.ExpireAt > 0 && now > entry.ExpireAt {
			continue
		}

		if entry.Secure {
			result[key] = "[SECURE]"
		} else {
			result[key] = entry.Value
		}
	}

	return result
}

// CleanupExpired removes all expired entries
func (s *PluginKVStore) CleanupExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	count := 0

	for key, entry := range s.data {
		if entry.ExpireAt > 0 && now > entry.ExpireAt {
			delete(s.data, key)
			count++
		}
	}

	if count > 0 {
		log.Printf("[KV] Cleaned up %d expired entries for %s", count, s.pluginID)
		s.save()
	}

	return count
}

// Clear removes all entries (for plugin unload)
func (s *PluginKVStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string]*KVEntry)
	return s.save()
}

// Stats returns storage statistics
func (s *PluginKVStore) Stats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.data)
	secure := 0
	withTTL := 0
	now := time.Now().Unix()
	expired := 0

	for _, entry := range s.data {
		if entry.Secure {
			secure++
		}
		if entry.TTL > 0 {
			withTTL++
		}
		if entry.ExpireAt > 0 && now > entry.ExpireAt {
			expired++
		}
	}

	return map[string]interface{}{
		"total":   total,
		"secure":  secure,
		"withTTL": withTTL,
		"expired": expired,
	}
}
