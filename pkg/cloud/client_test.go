package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// fakeCloud answers the device API and accepts tunnels the way vclu cloud
// does (docs/protocol.md in vclu-cloud).
type fakeCloud struct {
	t  *testing.T
	ts *httptest.Server

	mu       sync.Mutex
	decision string // "", "approve" or "deny"
	token    string // the valid device token, once approved
	unpairs  []string
	conns    chan *fakeConn
	hold     chan struct{}           // when set, connections wait for it to close
	enable   func([]string) []string // what welcome enables of hello's caps; nil: all
}

type fakeConn struct {
	nc    *wsConn
	sess  *yamux.Session
	ctrl  *yamux.Stream
	r     *proto.Reader
	hello proto.Hello
}

func newFakeCloud(t *testing.T) *fakeCloud {
	f := &fakeCloud{t: t, conns: make(chan *fakeConn, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+proto.PathPairStart, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(proto.PairStartResponse{
			DeviceCode: "dev-code", UserCode: "KXQF-7MPA", VerifyURL: f.ts.URL + "/pair",
			VerifyURLFull: f.ts.URL + "/pair?code=KXQF-7MPA", Interval: 0, ExpiresIn: 600, ProtocolVersion: proto.Version,
		})
	})
	mux.HandleFunc("POST "+proto.PathPairPoll, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch f.decision {
		case "approve":
			f.token = "device-token"
			json.NewEncoder(w).Encode(proto.PairPollResponse{
				DeviceID: 7, DeviceToken: f.token, HomeID: 42, HomeName: "Dom testowy",
				ConnectURL: "ws" + strings.TrimPrefix(f.ts.URL, "http") + proto.PathConnect,
			})
			f.decision = "used"
		case "deny":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(proto.PairError{Error: proto.ErrAccessDenied})
		case "used":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(proto.PairError{Error: proto.ErrExpiredToken})
		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(proto.PairError{Error: proto.ErrAuthorizationPending})
		}
	})
	mux.HandleFunc("POST "+proto.PathUnpair, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.unpairs = append(f.unpairs, tok)
		if tok == "" || tok != f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.token = ""
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET "+proto.PathConnect, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.token != "" && r.Header.Get("Authorization") == "Bearer "+f.token
		hold := f.hold
		f.mu.Unlock()
		if hold != nil {
			<-hold
		}
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		nc := newWSConn(ws)
		ycfg := yamux.DefaultConfig()
		ycfg.LogOutput = io.Discard
		sess, err := yamux.Server(nc, ycfg)
		if err != nil {
			return
		}
		ctrl, err := sess.AcceptStream()
		if err != nil {
			return
		}
		fc := &fakeConn{nc: nc, sess: sess, ctrl: ctrl, r: proto.NewReader(ctrl)}
		m, err := fc.r.Read()
		if err != nil || m.Decode(&fc.hello) != nil {
			return
		}
		enable := fc.hello.Caps
		f.mu.Lock()
		if f.enable != nil {
			enable = f.enable(enable)
		}
		f.mu.Unlock()
		proto.Write(ctrl, proto.Welcome{Type: proto.TypeWelcome, Proto: proto.Version, HomeID: 42, HomeName: "Dom testowy", Enable: enable})
		f.conns <- fc
	})
	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	return f
}

func (f *fakeCloud) decide(d string) {
	f.mu.Lock()
	f.decision = d
	f.mu.Unlock()
}

func (f *fakeCloud) nextConn(t *testing.T) *fakeConn {
	t.Helper()
	select {
	case c := <-f.conns:
		t.Cleanup(func() { c.sess.Close() })
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("vCLU did not connect")
		return nil
	}
}

// read returns the next message of type typ the vCLU sent.
func (c *fakeConn) read(t *testing.T, typ string) proto.Message {
	t.Helper()
	c.ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		m, err := c.r.Read()
		if err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		if m.Type == typ {
			return m
		}
	}
}

func newTestClient(t *testing.T, f *fakeCloud) (*Client, string) {
	dir := t.TempDir()
	if err := config.UpdateMasterConfig(dir, func(c *config.VCLUConfig) error {
		c.Cloud = &config.VCLUCloudConfig{URL: f.ts.URL}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := New(Options{
		Dir: dir, Name: "vCLU test", Version: "1.6.0", Platform: "linux/arm64",
		Health:      func() proto.Health { return proto.Health{UptimeS: 5, MemMB: 30} },
		HealthEvery: time.Hour,
		Backoff:     func(int) time.Duration { return 10 * time.Millisecond },
	})
	c.Start()
	t.Cleanup(func() { c.Shutdown("test", 0) })
	return c, dir
}

func waitState(t *testing.T, c *Client, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := c.Status(); st.State == want {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state %q, want %q (%+v)", c.Status().State, want, c.Status())
	return Status{}
}

func savedToken(t *testing.T, dir string) string {
	t.Helper()
	cfg, err := config.ReadMasterConfig(dir)
	if err != nil || cfg == nil || cfg.Cloud == nil {
		t.Fatalf("config: %v", err)
	}
	return cfg.Cloud.Token
}

// pairAndConnect goes from an unpaired vCLU to a live tunnel.
func pairAndConnect(t *testing.T, f *fakeCloud) (*Client, string, *fakeConn) {
	t.Helper()
	c, dir := newTestClient(t, f)
	st, err := c.Pair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePairing || st.Pairing == nil || st.Pairing.UserCode != "KXQF-7MPA" {
		t.Fatalf("after Pair: %+v", st)
	}
	f.decide("approve")
	conn := f.nextConn(t)
	waitState(t, c, StateOnline)
	return c, dir, conn
}

func TestPairConnectAndHealth(t *testing.T) {
	f := newFakeCloud(t)
	c, dir, conn := pairAndConnect(t, f)

	if conn.hello.Proto != proto.Version || conn.hello.Version != "1.6.0" || conn.hello.BootID == "" {
		t.Errorf("hello = %+v", conn.hello)
	}
	var h proto.Health
	conn.read(t, proto.TypeHealth).Decode(&h)
	if h.UptimeS != 5 || h.MemMB != 30 {
		t.Errorf("health = %+v", h)
	}
	if savedToken(t, dir) != "device-token" {
		t.Error("token not saved")
	}
	st := c.Status()
	if st.HomeName != "Dom testowy" || st.HomeID != 42 {
		t.Errorf("status = %+v", st)
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "device-token") {
		t.Error("status shows the token")
	}
	if _, err := c.Pair(context.Background()); err != ErrPaired {
		t.Errorf("second Pair: %v, want ErrPaired", err)
	}
}

func TestPairingDenied(t *testing.T) {
	f := newFakeCloud(t)
	c, dir := newTestClient(t, f)
	if _, err := c.Pair(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.decide("deny")
	if st := waitState(t, c, StateOff); st.Error != ReasonDenied || st.Pairing != nil {
		t.Errorf("status = %+v", st)
	}
	if savedToken(t, dir) != "" {
		t.Error("a denied pairing saved a token")
	}
}

func TestCancelPairing(t *testing.T) {
	f := newFakeCloud(t)
	c, _ := newTestClient(t, f)
	c.Pair(context.Background())
	c.CancelPairing()
	if st := c.Status(); st.State != StateOff || st.Pairing != nil {
		t.Errorf("status = %+v", st)
	}
}

// Unpaired in the cloud: 4401 deletes the token and stops.
func TestRevokedInTheCloud(t *testing.T) {
	f := newFakeCloud(t)
	c, dir, conn := pairAndConnect(t, f)
	f.mu.Lock()
	f.token = ""
	f.mu.Unlock()
	proto.Write(conn.ctrl, proto.Unpair{Type: proto.TypeUnpair})
	conn.nc.closeWith(proto.CloseUnpaired, "unpaired")

	if st := waitState(t, c, StateOff); st.Error != ReasonRevoked {
		t.Errorf("status = %+v", st)
	}
	if savedToken(t, dir) != "" {
		t.Error("token kept after 4401")
	}
	cfg, _ := config.ReadMasterConfig(dir)
	if cfg.Cloud.URL != f.ts.URL {
		t.Error("cloud address lost with the token")
	}
}

// A token the cloud no longer knows (unpaired while vCLU was off) is refused
// with 401 before the upgrade: the same as 4401.
func TestRefusedTokenAtStart(t *testing.T) {
	f := newFakeCloud(t)
	_, dir, _ := pairAndConnect(t, f)
	f.mu.Lock()
	f.token = ""
	f.mu.Unlock()

	c2 := New(Options{Dir: dir, Backoff: func(int) time.Duration { return 10 * time.Millisecond }})
	c2.Start()
	defer c2.Shutdown("test", 0)
	if st := waitState(t, c2, StateOff); st.Error != ReasonRevoked {
		t.Errorf("status = %+v", st)
	}
}

func TestReplacedStops(t *testing.T) {
	f := newFakeCloud(t)
	c, dir, conn := pairAndConnect(t, f)
	conn.nc.closeWith(proto.CloseReplaced, "replaced")

	if st := waitState(t, c, StateStopped); st.Error != ReasonReplaced {
		t.Errorf("status = %+v", st)
	}
	select {
	case <-f.conns:
		t.Error("reconnected after 4409")
	case <-time.After(200 * time.Millisecond):
	}
	if savedToken(t, dir) == "" {
		t.Error("4409 deleted the token")
	}
	if err := c.Reconnect(); err != nil {
		t.Fatal(err)
	}
	f.nextConn(t)
	waitState(t, c, StateOnline)
}

// A cloud restart (reconnect, then 1001) and a dropped connection both
// reconnect.
func TestReconnects(t *testing.T) {
	f := newFakeCloud(t)
	c, _, conn := pairAndConnect(t, f)
	proto.Write(conn.ctrl, proto.Reconnect{Type: proto.TypeReconnect, AfterS: 0})
	conn.nc.closeWith(websocket.CloseGoingAway, "cloud restarting")
	conn = f.nextConn(t)
	waitState(t, c, StateOnline)

	conn.nc.ws.NetConn().Close() // no close frame: the network went away
	f.nextConn(t)
	waitState(t, c, StateOnline)
}

func TestShutdownSaysBye(t *testing.T) {
	f := newFakeCloud(t)
	c, _, conn := pairAndConnect(t, f)
	go c.Shutdown("update", 2*time.Minute)
	var bye proto.Bye
	conn.read(t, proto.TypeBye).Decode(&bye)
	if bye.Reason != "update" || bye.ExpectBackS != 120 {
		t.Errorf("bye = %+v", bye)
	}
}

// Switched off: bye "disabled", no reconnecting, the token kept; off after a
// restart too, and on again with the same token.
func TestSwitchOffAndOn(t *testing.T) {
	f := newFakeCloud(t)
	c, dir, conn := pairAndConnect(t, f)
	go c.SetEnabled(false)
	var bye proto.Bye
	conn.read(t, proto.TypeBye).Decode(&bye)
	if bye.Reason != proto.ByeDisabled {
		t.Errorf("bye = %+v", bye)
	}
	waitState(t, c, StateDisabled)
	if c.Enabled() || savedToken(t, dir) != "device-token" {
		t.Errorf("after switching off: enabled %v, token %q", c.Enabled(), savedToken(t, dir))
	}
	if err := c.Reconnect(); err != ErrDisabled {
		t.Errorf("Reconnect while off: %v", err)
	}

	again := New(Options{Dir: dir, Backoff: func(int) time.Duration { return 10 * time.Millisecond }, HealthEvery: time.Hour})
	again.Start()
	t.Cleanup(func() { again.Shutdown("test", 0) })
	if st := again.Status(); st.State != StateDisabled || st.HomeName == "" {
		t.Errorf("after a restart: %+v", st)
	}
	select {
	case <-f.conns:
		t.Fatal("connected although switched off")
	case <-time.After(100 * time.Millisecond):
	}

	if err := again.SetEnabledLater(true); err != nil {
		t.Fatal(err)
	}
	f.nextConn(t)
	waitState(t, again, StateOnline)
	if !again.Enabled() {
		t.Error("not enabled after switching on")
	}
}

func TestSwitchNeedsPairing(t *testing.T) {
	c, _ := newTestClient(t, newFakeCloud(t))
	if err := c.SetEnabled(false); err != ErrNotPaired {
		t.Errorf("SetEnabled unpaired: %v", err)
	}
}

func TestUnpairKillSwitch(t *testing.T) {
	f := newFakeCloud(t)
	c, dir, _ := pairAndConnect(t, f)
	if err := c.Unpair(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	unpairs := f.unpairs
	f.mu.Unlock()
	if len(unpairs) != 1 || unpairs[0] != "device-token" {
		t.Errorf("cloud got unpair calls %v", unpairs)
	}
	if savedToken(t, dir) != "" || c.Status().State != StateOff {
		t.Errorf("after Unpair: token %q, status %+v", savedToken(t, dir), c.Status())
	}
}

// The kill switch works without the cloud: the token goes, the error says
// the cloud was not told.
func TestUnpairWithoutTheCloud(t *testing.T) {
	f := newFakeCloud(t)
	c, dir, _ := pairAndConnect(t, f)
	f.ts.CloseClientConnections()
	f.ts.Close()
	if err := c.Unpair(context.Background()); err == nil {
		t.Error("no error although the cloud is down")
	}
	if savedToken(t, dir) != "" || c.Status().State != StateOff {
		t.Error("token kept when the cloud is down")
	}
}

func TestBackoff(t *testing.T) {
	for i, want := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		got := backoff(i)
		if got < want*time.Second || got > want*time.Second*6/5 {
			t.Errorf("backoff(%d) = %s, want %ds + up to 20%%", i, got, want)
		}
	}
}

func TestNotifyQueuedSentAndButtons(t *testing.T) {
	f := newFakeCloud(t)
	c, _ := newTestClient(t, f)
	var runs atomic.Int32
	c.opts.NotifyAction = func(a proto.NotifyAction) proto.NotifyActionResult {
		runs.Add(1)
		if a.Actor.Role != "owner" {
			return proto.NotifyActionResult{Reason: proto.ReasonForbidden}
		}
		return proto.NotifyActionResult{OK: true, Message: "Rozbrojono"}
	}
	if err := c.Notify(proto.Notify{ID: "n0", Title: "x"}); err != ErrNotPaired {
		t.Errorf("before pairing: %v", err)
	}

	if _, err := c.Pair(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Paired but not yet connected: it waits for the connection.
	hold := make(chan struct{})
	f.mu.Lock()
	f.hold = hold
	f.mu.Unlock()
	f.decide("approve")
	waitState(t, c, StateConnecting)
	if err := c.Notify(proto.Notify{ID: "n1", Title: "Czeka"}); err != nil {
		t.Fatal(err)
	}
	close(hold)
	conn := f.nextConn(t)
	waitState(t, c, StateOnline)
	if !slices.Contains(conn.hello.Caps, proto.CapNotify) {
		t.Errorf("hello caps %v", conn.hello.Caps)
	}
	var n proto.Notify
	conn.read(t, proto.TypeNotify).Decode(&n)
	if n.ID != "n1" || n.Title != "Czeka" {
		t.Errorf("queued notify = %+v", n)
	}
	if err := c.Notify(proto.Notify{ID: "n2", Title: "Alarm", Actions: []proto.NotifyButton{{ID: "1", Label: "Rozbrój"}}}); err != nil {
		t.Fatal(err)
	}
	conn.read(t, proto.TypeNotify).Decode(&n)
	if n.ID != "n2" || len(n.Actions) != 1 {
		t.Errorf("notify = %+v", n)
	}

	press := func(id, role string, deadline int64) proto.NotifyActionResult {
		t.Helper()
		proto.Write(conn.ctrl, proto.NotifyAction{Type: proto.TypeNotifyAction, ID: id, Notify: "n2", Action: "1",
			Actor: proto.Actor{User: "cloud:7", Role: role}, DeadlineMs: deadline})
		var r proto.NotifyActionResult
		conn.read(t, proto.TypeNotifyActionResult).Decode(&r)
		if r.ID != id {
			t.Fatalf("answer to %s, want %s", r.ID, id)
		}
		return r
	}
	later := time.Now().Add(time.Minute).UnixMilli()
	if r := press("a1", "owner", later); !r.OK || r.Message != "Rozbrojono" {
		t.Errorf("press = %+v", r)
	}
	// The same request again: the same answer, not run twice.
	if r := press("a1", "owner", later); !r.OK || runs.Load() != 1 {
		t.Errorf("repeat = %+v, runs %d", r, runs.Load())
	}
	if r := press("a2", "member", later); r.OK || r.Reason != proto.ReasonForbidden {
		t.Errorf("member = %+v", r)
	}
	if r := press("a3", "owner", time.Now().Add(-time.Second).UnixMilli()); r.Reason != proto.ReasonExpired || runs.Load() != 2 {
		t.Errorf("late = %+v, runs %d", r, runs.Load())
	}
}

func TestPresenceFromTheCloud(t *testing.T) {
	f := newFakeCloud(t)
	c, _ := newTestClient(t, f)
	got := make(chan proto.Presence, 2)
	c.opts.Presence = func(p proto.Presence) { got <- p }
	if _, ok := c.Presence(); ok {
		t.Error("presence before any list")
	}
	if _, err := c.Pair(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.decide("approve")
	conn := f.nextConn(t)
	if !slices.Contains(conn.hello.Caps, proto.CapPresence) {
		t.Errorf("hello caps %v", conn.hello.Caps)
	}
	proto.Write(conn.ctrl, proto.Presence{Type: proto.TypePresence, People: []proto.Person{{User: "cloud:7", Name: "Tytus", State: proto.StateHome, Since: 5}}})
	select {
	case p := <-got:
		if len(p.People) != 1 || p.People[0].State != proto.StateHome {
			t.Errorf("presence = %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no presence")
	}
	if p, ok := c.Presence(); !ok || p.People[0].Name != "Tytus" {
		t.Errorf("Presence() = %+v %v", p, ok)
	}
}

// The cloud opens a stream per HTTP request and vCLU's tunnel handler answers it; the remote
// dashboards are listed after welcome and again when they change.
func TestTunnelAndDashboards(t *testing.T) {
	f := newFakeCloud(t)
	c, _ := newTestClient(t, f)
	var mu sync.Mutex
	list := []proto.DashboardInfo{{ID: "home", Name: "Dom", Roles: []string{"owner", "admin", "member"}}}
	c.opts.Dashboards = func() []proto.DashboardInfo {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(list)
	}
	c.opts.Tunnel = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s as %s", r.Method, r.URL.Path, r.Header.Get(proto.HeaderActor))
	})
	got := make(chan proto.Members, 1)
	c.opts.Members = func(m proto.Members) { got <- m }
	if _, err := c.Pair(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.decide("approve")
	conn := f.nextConn(t)
	for _, cp := range []string{proto.CapHTTP, proto.CapDashboards, proto.CapLocal} {
		if !slices.Contains(conn.hello.Caps, cp) {
			t.Errorf("hello caps %v, want %s", conn.hello.Caps, cp)
		}
	}
	var d proto.Dashboards
	if err := conn.read(t, proto.TypeDashboards).Decode(&d); err != nil || len(d.Dashboards) != 1 || d.Dashboards[0].ID != "home" {
		t.Fatalf("dashboards after welcome: %+v %v", d, err)
	}

	// a request through the tunnel, the way the cloud's proxy sends it
	hc := &http.Client{Transport: &http.Transport{
		DialContext:       func(context.Context, string, string) (net.Conn, error) { return conn.sess.Open() },
		DisableKeepAlives: true,
	}}
	req, _ := http.NewRequest(http.MethodGet, "http://vclu/d/home", nil)
	req.Header.Set(proto.HeaderActor, "cloud:7")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "GET /d/home as cloud:7" {
		t.Errorf("tunnel answer %q", body)
	}

	// the members list reaches the local tokens
	proto.Write(conn.ctrl, proto.Members{Type: proto.TypeMembers, Version: 3, Members: []proto.Member{{User: "cloud:7", Role: "owner"}}})
	select {
	case m := <-got:
		if m.Version != 3 || m.Members[0].User != "cloud:7" {
			t.Errorf("members %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no members")
	}

	mu.Lock()
	list = append(list, proto.DashboardInfo{ID: "alarm", Name: "Alarm", Roles: []string{"owner"}, Pin: true})
	mu.Unlock()
	c.DashboardsChanged()
	if err := conn.read(t, proto.TypeDashboards).Decode(&d); err != nil || len(d.Dashboards) != 2 || !d.Dashboards[1].Pin {
		t.Fatalf("dashboards after a change: %+v %v", d, err)
	}
}
