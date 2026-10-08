package i18n

import (
	"encoding/json"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	DefaultLang = "pl" // Polish as default for Grenton users
	CookieName  = "lang"
)

var (
	translations = make(map[string]map[string]interface{})
	mu           sync.RWMutex
	loaded       bool
)

// Load loads all translation files from the locales directory
func Load(localesDir string) error {
	mu.Lock()
	defer mu.Unlock()

	files, err := os.ReadDir(localesDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			lang := strings.TrimSuffix(file.Name(), ".json")
			filePath := filepath.Join(localesDir, file.Name())

			data, err := os.ReadFile(filePath)
			if err != nil {
				log.Printf("Failed to read translation file %s: %v", filePath, err)
				continue
			}

			var trans map[string]interface{}
			if err := json.Unmarshal(data, &trans); err != nil {
				log.Printf("Failed to parse translation file %s: %v", filePath, err)
				continue
			}

			translations[lang] = trans
			log.Printf("Loaded translations for: %s", lang)
		}
	}

	loaded = true
	return nil
}

// Get returns a translation for the given key and language
// Key format: "section.key" e.g., "setup.title"
func Get(lang, key string) string {
	mu.RLock()
	defer mu.RUnlock()

	// Fallback to default language if requested not found
	trans, ok := translations[lang]
	if !ok {
		trans, ok = translations[DefaultLang]
		if !ok {
			return key
		}
	}

	// Split key by dots and traverse the map
	parts := strings.Split(key, ".")
	var current interface{} = trans

	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return key
		}
	}

	if str, ok := current.(string); ok {
		return str
	}

	return key
}

// GetAll returns all translations for a language as a flat map
// This is useful for passing to templates
func GetAll(lang string) map[string]string {
	mu.RLock()
	defer mu.RUnlock()

	trans, ok := translations[lang]
	if !ok {
		trans, ok = translations[DefaultLang]
		if !ok {
			return make(map[string]string)
		}
	}

	flat := make(map[string]string)
	flatten("", trans, flat)
	return flat
}

// flatten recursively flattens a nested map into dot-notation keys
func flatten(prefix string, m map[string]interface{}, result map[string]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		switch val := v.(type) {
		case string:
			result[key] = val
		case map[string]interface{}:
			flatten(key, val, result)
		}
	}
}

// GetSection returns all translations for a section as a map
func GetSection(lang, section string) map[string]string {
	mu.RLock()
	defer mu.RUnlock()

	trans, ok := translations[lang]
	if !ok {
		trans, ok = translations[DefaultLang]
		if !ok {
			return make(map[string]string)
		}
	}

	sectionData, ok := trans[section].(map[string]interface{})
	if !ok {
		return make(map[string]string)
	}

	result := make(map[string]string)
	for k, v := range sectionData {
		if str, ok := v.(string); ok {
			result[k] = str
		}
	}
	return result
}

// TemplateFuncs returns template functions for translations
func TemplateFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(key string) string {
			return Get(lang, key)
		},
		"T": func(key string) template.HTML {
			return template.HTML(Get(lang, key))
		},
	}
}

// AvailableLanguages returns a list of available languages
func AvailableLanguages() []string {
	mu.RLock()
	defer mu.RUnlock()

	langs := make([]string, 0, len(translations))
	for lang := range translations {
		langs = append(langs, lang)
	}
	return langs
}

// IsLoaded returns whether translations have been loaded
func IsLoaded() bool {
	mu.RLock()
	defer mu.RUnlock()
	return loaded
}
