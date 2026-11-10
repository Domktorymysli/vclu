// Package cloud connects vCLU to vclu cloud: pairing (an RFC 8628 device
// grant), the tunnel (one WebSocket carrying a yamux session) with its
// control channel, and the kill switch. The protocol and its types live in
// github.com/Domktorymysli/vclu-cloud/proto.
//
// The house never depends on it: every failure here only changes Status.
package cloud

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/Domktorymysli/vclu/pkg/config"
)

type State string

const (
	StateOff        State = "off"        // not paired
	StatePairing    State = "pairing"    // showing a code, waiting for the person to confirm it
	StateConnecting State = "connecting" // paired, dialing the cloud
	StateOnline     State = "online"
	StateOffline    State = "offline"  // paired, retrying with backoff
	StateStopped    State = "stopped"  // paired, not retrying until Reconnect (see Status.Error)
	StateDisabled   State = "disabled" // paired, switched off with SetEnabled(false)
)

// Reasons in Status.Error that the panel translates; anything else is a
// plain error message.
const (
	ReasonRevoked  = "revoked"         // unpaired in the cloud: the token is gone
	ReasonReplaced = "replaced"        // another vCLU connected with this token
	ReasonUpdate   = "update_required" // the cloud no longer speaks this protocol version
	ReasonDenied   = "pairing_denied"
	ReasonExpired  = "pairing_expired"
)

// Status is what the panel shows. It never contains the token.
type Status struct {
	State    State     `json:"state"`
	URL      string    `json:"url"`
	HomeID   int64     `json:"homeId,omitempty"`
	HomeName string    `json:"homeName,omitempty"`
	Since    time.Time `json:"since,omitzero"`
	Error    string    `json:"error,omitempty"`
	Pairing  *Pairing  `json:"pairing,omitempty"`
}

// Pairing is the code the person enters in the cloud.
type Pairing struct {
	UserCode      string    `json:"userCode"`
	VerifyURL     string    `json:"verifyUrl"`
	VerifyURLFull string    `json:"verifyUrlFull"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

var ErrPaired = errors.New("cloud: already paired; disconnect first")

type Options struct {
	Dir      string // holds .vclu.json, where the token is kept
	Name     string // shown in the cloud when confirming the pairing
	Version  string
	Platform string
	Health   func() proto.Health // the house's own state, sent every HealthEvery
	// NotifyAction runs a button of a notification sent with Notify, for the
	// person who pressed it (Cloud._action in Lua). Called on its own
	// goroutine; nil answers not_found.
	NotifyAction func(proto.NotifyAction) proto.NotifyActionResult
	// Presence is told who is at home each time the cloud sends the list
	// (Cloud._presence in Lua), in order, on the connection's goroutine.
	Presence func(proto.Presence)
	// Tunnel serves the HTTP requests the cloud opens through the tunnel
	// (protocol section 7): the remote dashboards. nil offers no HTTP.
	Tunnel http.Handler
	// Dashboards lists the dashboards served remotely, for the dashboards
	// message (section 8) after welcome and after DashboardsChanged.
	Dashboards func() []proto.DashboardInfo
	// Members is given each members list from the cloud, in order (section
	// 6a). With Tunnel serving proto.PathLocalToken it offers local tokens
	// for the app on the LAN, which the lists keep valid.
	Members func(proto.Members)
	// Forgotten is called once vCLU drops a pairing: unpaired here, revoked by the cloud, or
	// replaced by a new one. What rests on the cloud's members (local tokens for the app) must
	// end with it, not with the next list or a week of silence.
	Forgotten func()
	// Objects lists the devices exposed with :remote() in Lua, for the snapshot message after
	// welcome and after ObjectsChanged (protocol section 5). Called on its own goroutine.
	Objects func() ([]proto.Object, error)
	// Command runs a cmd for one of them as the person (Cloud._command in Lua), not after
	// deadline. Called on its own goroutine, at most once per cmd id; nil answers not_found.
	Command func(cmd proto.Cmd, deadline time.Time) proto.CmdResult
	// States reads what the devices at paths are doing, for a person with roles (Cloud._states
	// in Lua): only those exposed with :remote() that the roles reach. Called on its own
	// goroutine; nil does not offer states.
	States func(paths, roles []string) []proto.State
	// Metrics are the metrics Lua registers with cloud=true, sent as statistics when the cloud
	// enables them for the home (metrics.go). nil offers none.
	Metrics MetricsSource

	HTTP            *http.Client
	ObjectsDebounce time.Duration                   // default objectsDebounce
	HealthEvery     time.Duration                   // default proto.HealthIntervalSeconds
	PingEvery       time.Duration                   // round trips for the cloud page, default a minute
	DashboardsEvery time.Duration                   // how often roles given by functions are checked, default dashboardsEvery
	MetricsFirst    time.Duration                   // statistics after welcome, default metricsFirst
	MetricsEvery    time.Duration                   // and then, default metricsEvery
	Backoff         func(attempt int) time.Duration // default 1, 2, 4, 8, 16, then 30 s, with jitter
}

type Client struct {
	opts   Options
	bootID string

	op sync.Mutex // one Pair / Unpair / Reconnect / Shutdown at a time

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc // of the background pairing or connection loop
	done   chan struct{}      // closed when it has ended
	tun    *tunnel            // the live connection, nil when there is none

	presence *proto.Presence // the last list from the cloud, nil before the first
	queue    []queued        // notifications waiting for a connection

	// answers by request id, for repeats: each runs at most once
	actions *replays[proto.NotifyActionResult]
	cmds    *replays[proto.CmdResult]

	members        map[string]bool // users in the last members list of this connection, nil before one
	membersVersion int64

	objKick      chan struct{} // ObjectsChanged: send the device list after the debounce
	objNow       chan struct{} // ObjectsRelease: send it now
	objHeldUntil time.Time     // ObjectsHold, under mu
	objSeq       int64         // Seq of the last snapshot, under mu

	clock func() time.Time // for commands' deadlines and rate; tests replace it

	link *linkLog // pings and outages for the cloud page

	metricsSt MetricsStatus // under mu

	history metricsHistory // statistics read back from the cloud (metrics_history.go)
}

// queued is a notification sent while the connection was down.
type queued struct {
	n  proto.Notify
	at time.Time
}

// Notifications wait this long for the connection to come back, at most
// maxQueued of them; older ones would only confuse ("Alarm!" an hour late).
const (
	queueFor  = 10 * time.Minute
	maxQueued = 20
)

var (
	ErrNotPaired = errors.New("cloud: not paired")
	ErrNoNotify  = errors.New("cloud: this cloud does not take notifications")
	ErrDisabled  = errors.New("cloud: switched off")
)

func New(opts Options) *Client {
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if opts.HealthEvery == 0 {
		opts.HealthEvery = proto.HealthIntervalSeconds * time.Second
	}
	if opts.Backoff == nil {
		opts.Backoff = backoff
	}
	if opts.Name == "" {
		opts.Name = "vCLU"
	}
	if opts.ObjectsDebounce == 0 {
		opts.ObjectsDebounce = objectsDebounce
	}
	if opts.PingEvery == 0 {
		opts.PingEvery = time.Minute
	}
	if opts.DashboardsEvery == 0 {
		opts.DashboardsEvery = dashboardsEvery
	}
	if opts.MetricsFirst == 0 {
		opts.MetricsFirst = metricsFirst
	}
	if opts.MetricsEvery == 0 {
		opts.MetricsEvery = metricsEvery
	}
	return &Client{
		link: newLinkLog(time.Now()),
		opts: opts, bootID: randomID(),
		actions: newReplays[proto.NotifyActionResult](replaysFor, replaysMax),
		cmds:    newReplays[proto.CmdResult](replaysFor, replaysMax),
		objKick: make(chan struct{}, 1), objNow: make(chan struct{}, 1),
		clock: time.Now,
	}
}

// Start connects when vCLU is paired. It returns at once.
func (c *Client) Start() {
	c.op.Lock()
	defer c.op.Unlock()
	cfg := c.config()
	c.setStatus(func(s *Status) { *s = Status{State: StateOff, URL: cfg.BaseURL()} })
	if !cfg.IsPaired() {
		return
	}
	c.setStatus(func(s *Status) { s.Since, s.HomeID, s.HomeName = time.Now(), cfg.HomeID, cfg.HomeName })
	if cfg.Disabled {
		c.setStatus(func(s *Status) { s.State = StateDisabled })
		return
	}
	c.setStatus(func(s *Status) { s.State = StateConnecting })
	c.launch(c.run)
}

// Enabled reports whether vCLU is paired and not switched off.
func (c *Client) Enabled() bool {
	cfg := c.config()
	return cfg.IsPaired() && !cfg.Disabled
}

// SetEnabled switches the connection off or on again without unpairing: off
// says bye ("disabled", so the cloud sends no offline alert) and closes the
// tunnel; on dials again with the same token. It is saved, so it survives a
// restart.
func (c *Client) SetEnabled(on bool) error {
	if err := c.saveEnabled(on); err != nil {
		return err
	}
	c.applyEnabled()
	return nil
}

// SetEnabledLater saves the switch and applies it in the background: for Lua,
// whose code may run on the tunnel that switching off waits for.
func (c *Client) SetEnabledLater(on bool) error {
	if err := c.saveEnabled(on); err != nil {
		return err
	}
	go c.applyEnabled()
	return nil
}

func (c *Client) saveEnabled(on bool) error {
	if cfg := c.config(); !cfg.IsPaired() {
		return ErrNotPaired
	}
	err := config.UpdateMasterConfig(c.opts.Dir, func(v *config.VCLUConfig) error {
		if v.Cloud != nil {
			v.Cloud.Disabled = !on
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("cloud: saving the switch: %w", err)
	}
	return nil
}

// applyEnabled brings the connection in line with the saved switch.
func (c *Client) applyEnabled() {
	c.op.Lock()
	defer c.op.Unlock()
	cfg := c.config()
	if !cfg.IsPaired() {
		return
	}
	off := c.Status().State == StateDisabled
	switch {
	case cfg.Disabled && !off:
		c.mu.Lock()
		t := c.tun
		c.mu.Unlock()
		if t != nil {
			t.send(proto.Bye{Type: proto.TypeBye, Reason: proto.ByeDisabled})
		}
		c.stop()
		c.link.up(time.Now()) // switched off on purpose: not an outage
		log.Printf("[Cloud] switched off")
		c.setStatus(func(s *Status) { s.State, s.Since, s.Error = StateDisabled, time.Now(), "" })
	case !cfg.Disabled && off:
		log.Printf("[Cloud] switched on")
		c.setStatus(func(s *Status) { s.State, s.Since, s.Error = StateConnecting, time.Now(), "" })
		c.launch(c.run)
	}
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.status
	if st.Pairing != nil {
		p := *st.Pairing
		st.Pairing = &p
	}
	return st
}

// Pair asks the cloud for a code and waits for the person to confirm it in
// the background; the code is in the returned Status. Once confirmed, the
// token is saved and the tunnel comes up.
func (c *Client) Pair(ctx context.Context) (Status, error) {
	c.op.Lock()
	defer c.op.Unlock()
	cfg := c.config()
	if cfg.IsPaired() {
		return c.Status(), ErrPaired
	}
	c.stop()
	base := strings.TrimRight(cfg.BaseURL(), "/")
	var start proto.PairStartResponse
	status, perr, err := c.post(ctx, base+proto.PathPairStart, "", proto.PairStartRequest{
		Name: c.opts.Name, Version: c.opts.Version, Platform: c.opts.Platform,
	}, &start)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("cloud refused pairing: HTTP %d %s", status, perr)
	}
	if err != nil {
		c.setStatus(func(s *Status) { *s = Status{State: StateOff, URL: base, Error: err.Error()} })
		return c.Status(), err
	}
	c.setStatus(func(s *Status) {
		*s = Status{State: StatePairing, URL: base, Since: time.Now(), Pairing: &Pairing{
			UserCode: start.UserCode, VerifyURL: start.VerifyURL, VerifyURLFull: start.VerifyURLFull,
			ExpiresAt: time.Now().Add(time.Duration(start.ExpiresIn) * time.Second),
		}}
	})
	log.Printf("[Cloud] pairing started at %s", start.VerifyURL) // never the code: the log is readable with the API key
	c.launch(func(ctx context.Context) {
		if c.waitForApproval(ctx, base, start) {
			c.run(ctx)
		}
	})
	return c.Status(), nil
}

// CancelPairing stops waiting for a code to be confirmed.
func (c *Client) CancelPairing() {
	c.op.Lock()
	defer c.op.Unlock()
	if c.Status().State != StatePairing {
		return
	}
	c.stop()
	c.setStatus(func(s *Status) { s.State, s.Pairing, s.Error = StateOff, nil, "" })
}

// Unpair is the kill switch: it closes the tunnel, tells the cloud to forget
// this vCLU and deletes the token. The token is deleted even when the cloud
// cannot be reached; the error then says the cloud was not told.
func (c *Client) Unpair(ctx context.Context) error {
	c.op.Lock()
	defer c.op.Unlock()
	c.stop()
	cfg := c.config()
	var cloudErr error
	if cfg.IsPaired() {
		status, _, err := c.post(ctx, strings.TrimRight(cfg.BaseURL(), "/")+proto.PathUnpair, cfg.Token, nil, nil)
		switch {
		case err != nil:
			cloudErr = fmt.Errorf("disconnected here, but the cloud could not be told: %w", err)
		case status != http.StatusNoContent && status != http.StatusUnauthorized: // 401: the cloud had already forgotten it
			cloudErr = fmt.Errorf("disconnected here, but the cloud answered HTTP %d", status)
		}
	}
	if err := c.forget(""); err != nil {
		return err
	}
	log.Printf("[Cloud] disconnected from the cloud")
	return cloudErr
}

// Reconnect restarts the connection loop of a paired vCLU, e.g. after it was
// stopped because another vCLU took over the token.
func (c *Client) Reconnect() error {
	c.op.Lock()
	defer c.op.Unlock()
	cfg := c.config()
	if !cfg.IsPaired() {
		return errors.New("cloud: not paired")
	}
	if cfg.Disabled {
		return ErrDisabled
	}
	c.stop()
	c.launch(c.run)
	return nil
}

// Shutdown announces a planned restart (bye) and closes the tunnel.
func (c *Client) Shutdown(reason string, expectBack time.Duration) {
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	t := c.tun
	c.mu.Unlock()
	if t != nil {
		t.send(proto.Bye{Type: proto.TypeBye, Reason: reason, ExpectBackS: int(expectBack / time.Second)})
	}
	c.stop()
}

// --- background work ---

func (c *Client) launch(fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.mu.Lock()
	c.cancel, c.done = cancel, done
	c.mu.Unlock()
	go func() {
		defer close(done)
		fn(ctx)
	}()
}

// stop ends the background pairing or connection and waits for it.
func (c *Client) stop() {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel, c.done = nil, nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (c *Client) waitForApproval(ctx context.Context, base string, start proto.PairStartResponse) bool {
	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 100 * time.Millisecond // only a test cloud says 0
	}
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	fail := func(reason string) bool {
		c.setStatus(func(s *Status) { s.State, s.Pairing, s.Error = StateOff, nil, reason })
		log.Printf("[Cloud] pairing ended: %s", reason)
		return false
	}
	for {
		if !sleep(ctx, interval) {
			return false
		}
		if time.Now().After(deadline) {
			return fail(ReasonExpired)
		}
		var res proto.PairPollResponse
		status, perr, err := c.post(ctx, base+proto.PathPairPoll, "", proto.PairPollRequest{DeviceCode: start.DeviceCode}, &res)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Printf("[Cloud] pairing poll: %v", err) // keep trying until the code expires
			continue
		}
		if status == http.StatusOK {
			return c.paired(base, res) == nil
		}
		switch perr {
		case proto.ErrAuthorizationPending:
		case proto.ErrSlowDown:
			interval += 5 * time.Second
		case proto.ErrAccessDenied:
			return fail(ReasonDenied)
		case proto.ErrExpiredToken:
			return fail(ReasonExpired)
		default:
			return fail(fmt.Sprintf("pairing failed: HTTP %d %s", status, perr))
		}
	}
}

func (c *Client) paired(base string, res proto.PairPollResponse) error {
	err := config.UpdateMasterConfig(c.opts.Dir, func(cfg *config.VCLUConfig) error {
		url, port := "", 0
		if cfg.Cloud != nil {
			url, port = cfg.Cloud.URL, cfg.Cloud.LocalPort
		}
		cfg.Cloud = &config.VCLUCloudConfig{
			URL: url, DeviceID: res.DeviceID, Token: res.DeviceToken, ConnectURL: res.ConnectURL,
			HomeID: res.HomeID, HomeName: res.HomeName, PairedAt: time.Now().UTC().Format(time.RFC3339),
			LocalPort: port,
		}
		return nil
	})
	if err == nil && c.opts.Forgotten != nil {
		c.opts.Forgotten() // the old pairing's members are not the new home's
	}
	if err != nil {
		c.setStatus(func(s *Status) { s.State, s.Pairing, s.Error = StateOff, nil, "could not save the token: "+err.Error() })
		return err
	}
	c.setStatus(func(s *Status) {
		*s = Status{State: StateConnecting, URL: base, HomeID: res.HomeID, HomeName: res.HomeName, Since: time.Now()}
	})
	log.Printf("[Cloud] paired with home %q", res.HomeName)
	return nil
}

var (
	errRevoked     = errors.New(ReasonRevoked)
	errReplaced    = errors.New(ReasonReplaced)
	errUnsupported = errors.New(ReasonUpdate)
)

type reconnectAfter struct{ d time.Duration }

func (r *reconnectAfter) Error() string { return fmt.Sprintf("cloud asked to reconnect in %s", r.d) }

// run keeps the tunnel up until ctx ends or the cloud says to stop.
func (c *Client) run(ctx context.Context) {
	attempt := 0
	for {
		cfg := c.config()
		if !cfg.IsPaired() || cfg.Disabled {
			return
		}
		began := time.Now()
		err := c.connect(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, errRevoked):
			log.Printf("[Cloud] the cloud unpaired this vCLU; token deleted")
			if ferr := c.forget(ReasonRevoked); ferr != nil {
				log.Printf("[Cloud] %v", ferr)
			}
			return
		case errors.Is(err, errReplaced), errors.Is(err, errUnsupported):
			log.Printf("[Cloud] stopped: %v", err)
			c.setStatus(func(s *Status) { s.State, s.Since, s.Error = StateStopped, time.Now(), err.Error() })
			c.link.down(time.Now(), err.Error())
			return
		}
		if time.Since(began) >= time.Minute {
			attempt = 0
		}
		delay := c.opts.Backoff(attempt)
		var ra *reconnectAfter
		if errors.As(err, &ra) && ra.d > 0 {
			delay = ra.d
		}
		attempt++
		c.setStatus(func(s *Status) {
			if s.State != StateOffline {
				s.Since = time.Now()
			}
			s.State, s.Error = StateOffline, err.Error()
		})
		c.link.down(time.Now(), err.Error())
		log.Printf("[Cloud] %v; retrying in %s", err, delay.Round(100*time.Millisecond))
		if !sleep(ctx, delay) {
			return
		}
	}
}

// tunnel is one live connection.
type tunnel struct {
	nc      *wsConn
	sess    *yamux.Session
	mu      sync.Mutex
	ctrl    *yamux.Stream
	notify  bool // the cloud enabled notify in welcome
	dashes  bool // ... and dashboards
	dashMu  sync.Mutex
	dashOld []byte // the dashboards message last sent
	objects bool   // ... and objects (snapshot)

	metricsAck    chan proto.MetricsAck    // answers to metrics, for sendMetrics
	metricsPoints chan proto.MetricsPoints // answers to metrics_query, for MetricsHistory
	metrics       bool                     // the cloud takes statistics
	metricsQuery  bool                     // ... and answers metrics_query
	home          int64                    // the home of this connection

	cmdRate    cmdLimiter   // used by the read loop only
	cmdRunning atomic.Int32 // commands running now
}

func (t *tunnel) send(v any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ctrl.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return proto.Write(t.ctrl, v)
}

// connect holds one connection until it ends and says why it ended.
func (c *Client) connect(ctx context.Context, cfg config.VCLUCloudConfig) error {
	c.setStatus(func(s *Status) {
		if s.State != StateOffline {
			s.State, s.Since = StateConnecting, time.Now()
		}
	})
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second}
	ws, resp, err := dialer.DialContext(ctx, cfg.ConnectURL+fmt.Sprintf("?v=%d", proto.Version),
		http.Header{"Authorization": {"Bearer " + cfg.Token}})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized {
				return errRevoked
			}
			return fmt.Errorf("cloud refused the connection: HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("cannot reach the cloud: %w", err)
	}
	nc := newWSConn(ws)
	defer nc.Close()
	stopWatch := context.AfterFunc(ctx, func() { nc.Close() })
	defer stopWatch()

	ycfg := yamux.DefaultConfig()
	ycfg.LogOutput = io.Discard
	ycfg.KeepAliveInterval = 30 * time.Second
	ycfg.ConnectionWriteTimeout = 15 * time.Second
	sess, err := yamux.Client(nc, ycfg)
	if err != nil {
		return err
	}
	defer sess.Close()
	ctrl, err := sess.OpenStream()
	if err != nil {
		return err
	}
	t := &tunnel{nc: nc, sess: sess, ctrl: ctrl, metricsAck: make(chan proto.MetricsAck, 1), metricsPoints: make(chan proto.MetricsPoints, 1)}
	r := proto.NewReader(ctrl)

	if err := t.send(proto.Hello{
		Type: proto.TypeHello, Proto: proto.Version, Version: c.opts.Version, BootID: c.bootID,
		Caps:  c.caps(),
		Clock: proto.Clock{NowMs: time.Now().UnixMilli(), Synced: clockLooksSet()},
	}); err != nil {
		return c.ended(t, err)
	}
	ctrl.SetReadDeadline(time.Now().Add(15 * time.Second))
	m, err := r.Read()
	if err != nil {
		return c.ended(t, err)
	}
	var w proto.Welcome
	if m.Type != proto.TypeWelcome || m.Decode(&w) != nil {
		return fmt.Errorf("expected welcome, got %q", m.Type)
	}
	ctrl.SetReadDeadline(time.Time{})
	if w.HomeName != cfg.HomeName && w.HomeName != "" {
		config.UpdateMasterConfig(c.opts.Dir, func(v *config.VCLUConfig) error {
			if v.Cloud != nil && v.Cloud.Token == cfg.Token {
				v.Cloud.HomeName = w.HomeName
			}
			return nil
		})
	}

	t.notify = slices.Contains(w.Enable, proto.CapNotify)
	t.dashes = slices.Contains(w.Enable, proto.CapDashboards) && c.opts.Dashboards != nil
	t.objects = slices.Contains(w.Enable, proto.CapObjects) && c.opts.Objects != nil
	t.metrics = c.opts.Metrics != nil && slices.Contains(w.Enable, proto.CapMetrics) && w.Metrics != nil
	t.metricsQuery = t.metrics && w.Metrics.Query
	t.home = w.HomeID
	if c.opts.Tunnel != nil && slices.Contains(w.Enable, proto.CapHTTP) {
		// The cloud opens a stream per request; the session is their listener.
		hs := &http.Server{Handler: c.opts.Tunnel, ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
		go hs.Serve(sess)
		defer hs.Close()
	}
	c.mu.Lock()
	c.tun = t
	c.members, c.membersVersion = nil, 0 // this connection sends its own
	pending := c.queue
	c.queue = nil
	c.status.State, c.status.Since, c.status.Error = StateOnline, time.Now(), ""
	c.status.HomeID, c.status.HomeName = w.HomeID, w.HomeName
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.tun = nil
		c.mu.Unlock()
	}()
	c.link.up(time.Now())
	log.Printf("[Cloud] connected (home %q)", w.HomeName)
	for _, q := range pending {
		if time.Since(q.at) > queueFor {
			log.Printf("[Cloud] notification %s dropped: waited too long for the connection", q.n.ID)
			continue
		}
		if !t.notify {
			log.Printf("[Cloud] notification %s dropped: %v", q.n.ID, ErrNoNotify)
			continue
		}
		t.send(q.n)
	}

	if t.dashes {
		c.sendDashboards(t)
	}

	done := make(chan struct{})
	defer close(done)
	go c.sendHealth(t, done)
	go c.pingLoop(t, done)
	if t.objects {
		go c.sendObjects(t, done)
	}
	if t.dashes {
		go c.watchDashboards(t, done)
	}
	if t.metrics {
		go c.sendMetrics(t, w.HomeID, *w.Metrics, done)
	}

	var after time.Duration
	unpaired := false
	for {
		m, err := r.Read()
		if err != nil {
			err = c.ended(t, err)
			if unpaired && !errors.Is(err, errRevoked) {
				err = errRevoked
			}
			if after > 0 && !errors.Is(err, errRevoked) {
				return &reconnectAfter{d: after}
			}
			return err
		}
		switch m.Type {
		case proto.TypeReconnect:
			var rc proto.Reconnect
			if m.Decode(&rc) == nil {
				after = time.Duration(rc.AfterS)*time.Second + time.Duration(rand.Int64N(int64(time.Second)))
			}
		case proto.TypeUnpair:
			unpaired = true
		case proto.TypePresence:
			var p proto.Presence
			if m.Decode(&p) == nil {
				c.mu.Lock()
				c.presence = &p
				c.mu.Unlock()
				if c.opts.Presence != nil {
					c.opts.Presence(p)
				}
			}
		case proto.TypeMembers:
			var ms proto.Members
			if m.Decode(&ms) == nil {
				c.setMembers(ms)
				if c.opts.Members != nil {
					c.opts.Members(ms)
				}
			}
		case proto.TypeNotifyAction:
			var a proto.NotifyAction
			if m.Decode(&a) == nil {
				c.notifyAction(t, a, done)
			}
		case proto.TypeCmd:
			var cmd proto.Cmd
			if err := m.Decode(&cmd); err != nil {
				log.Printf("[Cloud] bad cmd: %v", err)
			} else {
				c.command(t, cmd, done)
			}
		case proto.TypeStates:
			var r proto.States
			if err := m.Decode(&r); err != nil {
				log.Printf("[Cloud] bad states: %v", err)
			} else {
				c.states(t, r)
			}
		case proto.TypeMetricsAck:
			var a proto.MetricsAck
			if m.Decode(&a) == nil {
				select {
				case t.metricsAck <- a:
				default: // nobody waits for it: a late answer
				}
			}
		case proto.TypeMetricsPoints:
			var p proto.MetricsPoints
			if m.Decode(&p) == nil {
				select {
				case t.metricsPoints <- p:
				default:
				}
			}
		case proto.TypeError:
			var e proto.Error
			if m.Decode(&e) == nil && e.ID != "" {
				log.Printf("[Cloud] notification %s refused: %s %s", e.ID, e.Code, e.Message)
			} else {
				log.Printf("[Cloud] error from the cloud: %s", m.Raw)
			}
		default:
			// Unknown types are ignored: a newer cloud may send messages this vCLU does not know.
		}
	}
}

// caps are the capabilities offered in hello.
func (c *Client) caps() []string {
	caps := []string{proto.CapNotify, proto.CapPresence}
	if c.opts.Tunnel != nil {
		caps = append(caps, proto.CapHTTP)
	}
	if c.opts.Dashboards != nil {
		caps = append(caps, proto.CapDashboards)
	}
	if c.opts.Tunnel != nil && c.opts.Members != nil {
		caps = append(caps, proto.CapLocal)
	}
	if c.opts.Objects != nil {
		caps = append(caps, proto.CapObjects)
	}
	if c.opts.Command != nil {
		caps = append(caps, proto.CapCmd)
	}
	if c.opts.Metrics != nil {
		caps = append(caps, proto.CapMetrics)
	}
	if c.opts.States != nil {
		caps = append(caps, proto.CapStates)
	}
	return caps
}

func (c *Client) dashboardsMessage() proto.Dashboards {
	list := c.opts.Dashboards()
	if list == nil {
		list = []proto.DashboardInfo{}
	}
	if len(list) > proto.MaxDashboards {
		log.Printf("[Cloud] %d remote dashboards, the cloud lists the first %d", len(list), proto.MaxDashboards)
		list = list[:proto.MaxDashboards]
	}
	return proto.Dashboards{Type: proto.TypeDashboards, Dashboards: list}
}

func (c *Client) sendDashboards(t *tunnel) {
	c.sendDashboardsIf(t, false)
}

// dashboardsEvery is how often the list is computed again for changes without a Lua reload.
const dashboardsEvery = 15 * time.Second

// sendDashboardsIf sends the list, when changed is true only if it differs from the one last sent.
func (c *Client) sendDashboardsIf(t *tunnel, changed bool) {
	m := c.dashboardsMessage()
	b, _ := json.Marshal(m)
	t.dashMu.Lock()
	defer t.dashMu.Unlock()
	if changed && bytes.Equal(b, t.dashOld) {
		return
	}
	if err := t.send(m); err != nil {
		log.Printf("[Cloud] remote dashboards not sent: %v", err)
		return
	}
	t.dashOld = b
}

// watchDashboards sends the list again when it changed without a Lua reload: roles given by a
// function (dash:roles(fn)) or a dashboard rebuilt from a script.
func (c *Client) watchDashboards(t *tunnel, done <-chan struct{}) {
	tick := time.NewTicker(c.opts.DashboardsEvery)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			c.sendDashboardsIf(t, true)
		}
	}
}

// DashboardsChanged sends the list of remote dashboards again (after a Lua
// reload); the cloud keeps it only when it changed.
func (c *Client) DashboardsChanged() {
	c.mu.Lock()
	t := c.tun
	c.mu.Unlock()
	if t != nil && t.dashes {
		c.sendDashboards(t)
	}
}

// Notify sends a notification to the members' phones (protocol section 5,
// notify). While the connection is down it waits for it, up to queueFor; the
// cloud answers only when it refuses one, and that goes to the log.
func (c *Client) Notify(n proto.Notify) error {
	n.Type = proto.TypeNotify
	if n.ID == "" || n.Title == "" {
		return errors.New("cloud: a notification needs an id and a title")
	}
	c.mu.Lock()
	if c.status.State == StateOff || c.status.State == StatePairing {
		c.mu.Unlock()
		return ErrNotPaired
	}
	t := c.tun
	if t == nil {
		if len(c.queue) >= maxQueued {
			log.Printf("[Cloud] notification %s dropped: queue full", c.queue[0].n.ID)
			c.queue = c.queue[1:]
		}
		c.queue = append(c.queue, queued{n: n, at: time.Now()})
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	if !t.notify {
		return ErrNoNotify
	}
	if err := t.send(n); err != nil {
		// The connection is going down: wait for the next one.
		c.mu.Lock()
		c.queue = append(c.queue, queued{n: n, at: time.Now()})
		c.mu.Unlock()
	}
	return nil
}

// Presence returns the last presence list from the cloud; ok is false before
// the first (or without a cloud).
func (c *Client) Presence() (proto.Presence, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.presence == nil {
		return proto.Presence{}, false
	}
	return *c.presence, true
}

// notifyAction runs a notification's button at most once per request id: a repeat waits for
// the first answer, even while the button still runs. It returns at once.
func (c *Client) notifyAction(t *tunnel, a proto.NotifyAction, done <-chan struct{}) {
	e, first := c.actions.claim(a.ID)
	if !first {
		go func() {
			select {
			case <-e.done:
				t.send(e.res)
			case <-done:
			}
		}()
		return
	}
	go func() {
		res := proto.NotifyActionResult{Reason: proto.ReasonNotFound}
		switch {
		case a.DeadlineMs > 0 && time.Now().UnixMilli() > a.DeadlineMs:
			res = proto.NotifyActionResult{Reason: proto.ReasonExpired}
		case c.opts.NotifyAction != nil:
			res = c.opts.NotifyAction(a)
		}
		res.Type, res.ID = proto.TypeNotifyActionResult, a.ID
		c.actions.finish(a.ID, e, res, true)
		t.send(res)
	}()
}

// ended turns the end of a connection into the reason the cloud gave.
func (c *Client) ended(t *tunnel, err error) error {
	select {
	case <-t.sess.CloseChan():
	case <-time.After(2 * time.Second):
	}
	switch code := t.nc.closeCode(); code {
	case proto.CloseUnpaired:
		return errRevoked
	case proto.CloseReplaced:
		return errReplaced
	case proto.CloseUnsupported:
		return errUnsupported
	case 0:
		return fmt.Errorf("connection lost: %w", err)
	default:
		return fmt.Errorf("cloud closed the connection (%d)", code)
	}
}

func (c *Client) sendHealth(t *tunnel, done <-chan struct{}) {
	tick := time.NewTicker(c.opts.HealthEvery)
	defer tick.Stop()
	for {
		h := proto.Health{}
		if c.opts.Health != nil {
			h = c.opts.Health()
		}
		h.Type = proto.TypeHealth
		if err := t.send(h); err != nil {
			return
		}
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

// --- helpers ---

func (c *Client) config() config.VCLUCloudConfig {
	cfg, err := config.ReadMasterConfig(c.opts.Dir)
	if err != nil {
		log.Printf("[Cloud] reading .vclu.json: %v", err)
	}
	if cfg == nil || cfg.Cloud == nil {
		return config.VCLUCloudConfig{}
	}
	return *cfg.Cloud
}

// forget deletes the token, keeping the cloud address and the LAN listener's port, and ends
// what rested on the pairing (Options.Forgotten).
func (c *Client) forget(reason string) error {
	err := config.UpdateMasterConfig(c.opts.Dir, func(cfg *config.VCLUConfig) error {
		if cfg.Cloud != nil {
			cfg.Cloud = &config.VCLUCloudConfig{URL: cfg.Cloud.URL, LocalPort: cfg.Cloud.LocalPort}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("cloud: deleting the token: %w", err)
	}
	if c.opts.Forgotten != nil {
		c.opts.Forgotten()
	}
	c.setStatus(func(s *Status) {
		*s = Status{State: StateOff, URL: s.URL, Since: time.Now(), Error: reason}
	})
	c.link.up(time.Now())
	return nil
}

func (c *Client) setStatus(fn func(*Status)) {
	c.mu.Lock()
	fn(&c.status)
	c.mu.Unlock()
}

// post sends a JSON request to the cloud. It returns the HTTP status and,
// for an error answer, the protocol error code.
func (c *Client) post(ctx context.Context, url, token string, in, out any) (int, string, error) {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.opts.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		var perr proto.PairError
		json.Unmarshal(raw, &perr)
		return resp.StatusCode, perr.Error, nil
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, "", fmt.Errorf("unexpected answer from the cloud: %w", err)
		}
	}
	return resp.StatusCode, "", nil
}

// backoff is 1, 2, 4, 8, 16, then 30 s, each up to 20% longer at random.
func backoff(attempt int) time.Duration {
	d := 30 * time.Second
	if attempt < 5 {
		d = time.Second << attempt
	}
	return d + time.Duration(rand.Int64N(int64(d/5)))
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// clockLooksSet guesses whether the clock was synced: a Pi has no RTC and
// boots with a date from the past until NTP corrects it.
func clockLooksSet() bool { return time.Now().Year() >= 2026 }

func randomID() string {
	b := make([]byte, 8)
	crand.Read(b)
	return hex.EncodeToString(b)
}

// pingLoop times a round trip through the tunnel each PingEvery, for the cloud page.
func (c *Client) pingLoop(t *tunnel, done <-chan struct{}) {
	tick := time.NewTicker(c.opts.PingEvery)
	defer tick.Stop()
	for {
		rtt, err := t.sess.Ping()
		select {
		case <-done:
			return // the connection ended: its outage says so, not a failed ping
		default:
		}
		c.link.ping(time.Now(), rtt, err == nil)
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

// Link reports the pings and outages of the last day, for the cloud page.
func (c *Client) Link() LinkReport { return c.link.report(time.Now()) }
