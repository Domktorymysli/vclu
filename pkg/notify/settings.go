package notify

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// mute is a muted key; a zero until is forever.
type mute struct {
	until  time.Time
	title  string
	msg    string
	params map[string]string
}

func (s *Store) loadConfig() {
	s.apply(nil)
	if s.opts.ConfigDir == "" {
		return
	}
	mc, err := config.ReadMasterConfig(s.opts.ConfigDir)
	if err != nil {
		s.opts.Logf("[NOTIFY] settings not read, using defaults: %v", err)
		return
	}
	if mc != nil {
		s.apply(mc.Notifications)
	}
}

// apply takes a section as the settings; nil is the defaults.
func (s *Store) apply(nc *config.VCLUNotificationsConfig) {
	sources, muted := map[string]bool{}, map[string]mute{}
	if nc != nil {
		for id, on := range nc.Sources {
			sources[id] = on
		}
		for k, m := range nc.Muted {
			if m.Until == "forever" {
				muted[k] = mute{title: m.Title, msg: m.Msg, params: m.Params}
			} else if t, err := time.Parse(time.RFC3339, m.Until); err == nil {
				muted[k] = mute{until: t, title: m.Title, msg: m.Msg, params: m.Params}
			}
		}
	}
	s.mu.Lock()
	s.sources, s.muted = sources, muted
	s.mu.Unlock()
}

// section is the settings as a section, for a store without .vclu.json.
func (s *Store) section() *config.VCLUNotificationsConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	nc := &config.VCLUNotificationsConfig{Sources: map[string]bool{}, Muted: map[string]config.VCLUNotificationMute{}}
	for id, on := range s.sources {
		nc.Sources[id] = on
	}
	for k, m := range s.muted {
		until := "forever"
		if !m.until.IsZero() {
			until = m.until.Format(time.RFC3339)
		}
		nc.Muted[k] = config.VCLUNotificationMute{Until: until, Title: m.title, Msg: m.msg, Params: m.params}
	}
	return nc
}

// saveConfig changes the section (in .vclu.json when there is one), dropping expired mutes.
func (s *Store) saveConfig(fn func(*config.VCLUNotificationsConfig)) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	change := func(nc *config.VCLUNotificationsConfig) {
		fn(nc)
		now := s.opts.Now()
		for k, m := range nc.Muted {
			if t, err := time.Parse(time.RFC3339, m.Until); m.Until != "forever" && (err != nil || !now.Before(t)) {
				delete(nc.Muted, k)
			}
		}
	}
	if s.opts.ConfigDir == "" {
		nc := s.section()
		change(nc)
		s.apply(nc)
		return nil
	}
	var saved *config.VCLUNotificationsConfig
	err := config.UpdateMasterConfig(s.opts.ConfigDir, func(mc *config.VCLUConfig) error {
		if mc.Notifications == nil {
			mc.Notifications = &config.VCLUNotificationsConfig{}
		}
		change(mc.Notifications)
		saved = mc.Notifications
		return nil
	})
	if err != nil {
		return err
	}
	s.apply(saved)
	return nil
}

func (s *Store) isMuted(key string, now time.Time) bool {
	m, ok := s.muted[key]
	return ok && key != "" && (m.until.IsZero() || now.Before(m.until))
}

// Mute adds nothing with key for d (0: forever) and removes its entry.
func (s *Store) Mute(key string, d time.Duration) error {
	if key == "" {
		return errors.New("no key")
	}
	s.mu.Lock()
	until := "forever"
	if d > 0 {
		until = s.opts.Now().Add(d).UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	title, msg, params := key, "", map[string]string(nil)
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.Key == key {
			title, msg, params = e.Title, e.Msg, e.Params
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	s.changed()
	s.mu.Unlock()
	return s.saveConfig(func(nc *config.VCLUNotificationsConfig) {
		if nc.Muted == nil {
			nc.Muted = map[string]config.VCLUNotificationMute{}
		}
		nc.Muted[key] = config.VCLUNotificationMute{Until: until, Title: title, Msg: msg, Params: params}
	})
}

// Unmute lets key add entries again.
func (s *Store) Unmute(key string) error {
	return s.saveConfig(func(nc *config.VCLUNotificationsConfig) { delete(nc.Muted, key) })
}

// SetSources switches the sources given and keeps the rest; an unknown id is an error. The
// states of a source turned off are resolved: nothing would update them.
func (s *Store) SetSources(on map[string]bool) error {
	for id := range on {
		if _, ok := Lookup(id); !ok {
			return fmt.Errorf("unknown source %q", id)
		}
	}
	err := s.saveConfig(func(nc *config.VCLUNotificationsConfig) {
		if nc.Sources == nil {
			nc.Sources = map[string]bool{}
		}
		for id, v := range on {
			nc.Sources[id] = v
		}
	})
	if err != nil {
		return err
	}
	s.Resolve(func(e Entry) bool { v, set := on[e.Source]; return set && !v })
	return nil
}

// Source is a source, whether it adds entries and whether it does by default.
type Source struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Default bool   `json:"default"`
}

// Muted is a muted key: until is RFC 3339 or "forever".
type Muted struct {
	Key    string            `json:"key"`
	Until  string            `json:"until"`
	Title  string            `json:"title"`
	Msg    string            `json:"msg,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

// Settings is GET /api/notifications/settings.
type Settings struct {
	Sources []Source `json:"sources"`
	Muted   []Muted  `json:"muted"`
}

// Settings gives the sources and the keys muted now.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	out := Settings{Sources: []Source{}, Muted: []Muted{}}
	for _, src := range Sources {
		out.Sources = append(out.Sources, Source{ID: src.ID, Enabled: s.enabled(src.ID), Default: src.DefaultOn})
	}
	for k, m := range s.muted {
		if !m.until.IsZero() && !now.Before(m.until) {
			continue
		}
		e := Muted{Key: k, Until: "forever", Title: m.title, Msg: m.msg, Params: m.params}
		if !m.until.IsZero() {
			e.Until = m.until.UTC().Format(time.RFC3339)
		}
		if e.Title == "" {
			e.Title = k
		}
		out.Muted = append(out.Muted, e)
	}
	sort.Slice(out.Muted, func(i, j int) bool { return out.Muted[i].Key < out.Muted[j].Key })
	return out
}
