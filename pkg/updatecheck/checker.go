// Package updatecheck asks the update service whether a newer vCLU release is out and
// keeps the last good answer for the panel. It sends only the instance id, version, os
// and arch, and nothing at all while the check is off.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the update service; tests pass an httptest server instead.
const DefaultBaseURL = "https://update.vclu.pl"

const (
	timeout     = 10 * time.Second
	maxBodySize = 1 << 20
)

// Release is one release in the answer; fields the service adds later are ignored.
type Release struct {
	Version     string             `json:"version"`
	Name        string             `json:"name"`
	PublishedAt string             `json:"published_at"`
	URL         string             `json:"url"`
	Notes       map[string]string  `json:"notes,omitempty"`   // "en", "pl": markdown
	Summary     map[string]Summary `json:"summary,omitempty"` // "en", "pl"
}

// Summary is a release in short: a highlights line and what to do before updating, markdown.
type Summary struct {
	Highlights   string   `json:"highlights,omitempty"`
	BeforeUpdate []string `json:"before_update,omitempty"`
}

// Answer is the service's answer to a check.
type Answer struct {
	Current         string    `json:"current"`
	UpdateAvailable bool      `json:"update_available"`
	Latest          *Release  `json:"latest"`
	Releases        []Release `json:"releases"` // newer than ours, newest first
}

// Clock lets tests drive the schedule.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options configure a Checker; only Dir and Version are required.
type Options struct {
	Dir     string      // data directory holding the instance id
	Version string      // the build's version, sent as it is
	Enabled func() bool // read before every check; nil means on
	BaseURL string      // DefaultBaseURL when empty
	Client  *http.Client
	Clock   Clock
	Logf    func(format string, args ...any)
}

// Checker runs the checks; one Run per Checker.
type Checker struct {
	opts Options
	wake chan struct{}

	mu        sync.Mutex
	id        string
	last      *Answer
	checkedAt time.Time
	logged    map[string]bool // errors logged once: a 400, a failed id write
}

// New returns a Checker; Run starts it.
func New(opts Options) *Checker {
	if opts.Version = strings.TrimSpace(opts.Version); opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	if opts.Client == nil {
		opts.Client = &http.Client{ // no cookie jar
			Timeout: timeout,
			// A redirect fails the check: the request goes only where it was sent, over HTTPS.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	return &Checker{opts: opts, wake: make(chan struct{}, 1), logged: map[string]bool{}}
}

// FirstDelay is 1-10 minutes, so a power cut does not make every house ask at once.
func FirstDelay() time.Duration {
	return time.Minute + rand.N(9*time.Minute+1)
}

// NextDelay is 6 h ± 30 min.
func NextDelay() time.Duration {
	return 6*time.Hour - 30*time.Minute + rand.N(time.Hour+1)
}

// Run checks after FirstDelay and then every NextDelay until ctx ends. A failed check
// waits for the next regular one; checks never overlap.
func (c *Checker) Run(ctx context.Context) {
	delay := FirstDelay()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			delay = FirstDelay()
			continue
		case <-c.opts.Clock.After(delay):
		}
		if c.enabled() {
			c.check(ctx)
		}
		delay = NextDelay()
	}
}

// Reschedule moves the next check to FirstDelay from now; call it when the check is turned on.
func (c *Checker) Reschedule() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Version is the version sent to the service.
func (c *Checker) Version() string { return c.opts.Version }

// Last returns the last good answer and its time; nil before the first one. Do not modify it.
func (c *Checker) Last() (*Answer, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last, c.checkedAt
}

func (c *Checker) enabled() bool {
	return c.opts.Enabled == nil || c.opts.Enabled()
}

func (c *Checker) check(ctx context.Context) {
	id, err := c.instanceID()
	if id == "" {
		c.debugf("no instance id: %v", err)
		return
	}
	a, err := c.fetch(ctx, id)
	if err != nil {
		c.debugf("check failed: %v", err)
		return
	}
	c.mu.Lock()
	c.last, c.checkedAt = a, c.opts.Clock.Now()
	c.mu.Unlock()
}

func (c *Checker) fetch(ctx context.Context, id string) (*Answer, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	q := url.Values{"id": {id}, "version": {c.opts.Version}, "os": {runtime.GOOS}}
	if a := Arch(); a != "" {
		q.Set("arch", a)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.opts.BaseURL+"/v1/check?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vclu/"+c.opts.Version)
	resp, err := c.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxBodySize)
	if resp.StatusCode == http.StatusBadRequest {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(body).Decode(&e) // the status says enough without the reason
		c.logOnce("400", "[Update] the update service refused the check (%q): a vCLU bug, please report it", e.Error)
		return nil, fmt.Errorf("HTTP 400 %q", e.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var a Answer
	if err := json.NewDecoder(body).Decode(&a); err != nil {
		return nil, fmt.Errorf("bad answer: %w", err)
	}
	return &a, nil
}

// instanceID loads the id once; when the file cannot be written the id lives until restart.
func (c *Checker) instanceID() (string, error) {
	c.mu.Lock()
	id := c.id
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	id, err := LoadOrCreateID(c.opts.Dir)
	if err != nil && id != "" {
		c.logOnce("id", "[Update] instance id not saved, a restart makes a new one: %v", err)
	}
	c.mu.Lock()
	c.id = id
	c.mu.Unlock()
	return id, err
}

func (c *Checker) logOnce(key, format string, args ...any) {
	c.mu.Lock()
	done := c.logged[key]
	c.logged[key] = true
	c.mu.Unlock()
	if !done {
		c.opts.Logf(format, args...)
	}
}

func (c *Checker) debugf(format string, args ...any) {
	c.opts.Logf("[DEBUG] [Update] "+format, args...)
}

// Arch is the release asset's arch: GOARCH, or armv6/armv7 for arm.
func Arch() string {
	goarm := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "GOARM" {
				goarm = s.Value
			}
		}
	}
	return archFor(runtime.GOARCH, goarm)
}

func archFor(goarch, goarm string) string {
	if goarch != "arm" {
		return goarch
	}
	goarm, _, _ = strings.Cut(goarm, ",") // "7,softfloat"
	if goarm == "" {
		return "" // not a release asset: arch is left out
	}
	return "armv" + goarm
}
