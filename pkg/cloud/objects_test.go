package cloud

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
)

// fakeDevices stands in for ExposedObjects:remoteList() and counts the calls.
type fakeDevices struct {
	mu    sync.Mutex
	list  []proto.Object
	calls atomic.Int32
}

func (d *fakeDevices) objects() ([]proto.Object, error) {
	d.calls.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.list), nil
}

func (d *fakeDevices) set(list []proto.Object) {
	d.mu.Lock()
	d.list = list
	d.mu.Unlock()
}

// connectWith pairs a client whose options were set by setup and returns the live connection.
func connectWith(t *testing.T, f *fakeCloud, setup func(c *Client)) (*Client, *fakeConn) {
	t.Helper()
	c, _ := newTestClient(t, f)
	c.opts.ObjectsDebounce = 10 * time.Millisecond
	setup(c)
	if _, err := c.Pair(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.decide("approve")
	conn := f.nextConn(t)
	waitState(t, c, StateOnline)
	return c, conn
}

func readSnapshot(t *testing.T, conn *fakeConn) proto.Snapshot {
	t.Helper()
	var s proto.Snapshot
	m := conn.read(t, proto.TypeSnapshot)
	if err := m.Decode(&s); err != nil {
		t.Fatal(err)
	}
	if len(m.Raw) > proto.SnapshotPartBytes {
		t.Errorf("snapshot part of %d bytes, over %d", len(m.Raw), proto.SnapshotPartBytes)
	}
	return s
}

// roundTrip waits until the read loop handled everything sent before it: an invalid cmd is
// answered on the read loop itself.
func roundTrip(t *testing.T, conn *fakeConn) {
	t.Helper()
	proto.Write(conn.ctrl, proto.Cmd{Type: proto.TypeCmd, ID: "sync!"})
	for {
		var r proto.CmdResult
		conn.read(t, proto.TypeCmdResult).Decode(&r)
		if r.ID == "sync!" {
			return
		}
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSnapshotAfterWelcomeAndOnChange(t *testing.T) {
	f := newFakeCloud(t)
	dev := &fakeDevices{list: []proto.Object{
		{Path: "vclu:lamp", Type: "light", Name: "Lampa", Access: proto.AccessFull},
		{Path: "vclu:gate", Type: "switch", Name: "Brama", Access: proto.AccessConfirm, Roles: []string{"domownicy"}},
	}}
	c, conn := connectWith(t, f, func(c *Client) {
		c.opts.Objects = dev.objects
		c.opts.Command = func(proto.Cmd, time.Time) proto.CmdResult { return proto.CmdResult{OK: true} }
	})
	for _, cp := range []string{proto.CapObjects, proto.CapCmd} {
		if !slices.Contains(conn.hello.Caps, cp) {
			t.Errorf("hello caps %v, want %s", conn.hello.Caps, cp)
		}
	}
	s := readSnapshot(t, conn)
	if s.BootID != conn.hello.BootID || s.Seq != 1 || s.Part != 1 || s.Parts != 1 || len(s.Objects) != 2 ||
		s.Objects[0].Path != "vclu:gate" || s.Objects[0].Roles[0] != "domownicy" {
		t.Fatalf("snapshot after welcome = %+v", s)
	}

	// nothing changed: nothing sent
	calls := dev.calls.Load()
	c.ObjectsChanged()
	eventually(t, "the list asked again", func() bool { return dev.calls.Load() > calls })
	// a change: sent with the next seq
	dev.set([]proto.Object{{Path: "vclu:lamp", Type: "light", Name: "Lampa w salonie", Access: proto.AccessFull}})
	c.ObjectsChanged()
	if s := readSnapshot(t, conn); s.Seq != 2 || len(s.Objects) != 1 || s.Objects[0].Name != "Lampa w salonie" {
		t.Errorf("snapshot after a change = %+v", s)
	}
	// none at all is a list too: the cloud empties its own
	dev.set(nil)
	c.ObjectsChanged()
	if s := readSnapshot(t, conn); s.Seq != 3 || s.Parts != 1 || s.Objects == nil || len(s.Objects) != 0 {
		t.Errorf("empty snapshot = %+v", s)
	}
}

func TestSnapshotOnlyWhenEnabled(t *testing.T) {
	f := newFakeCloud(t)
	f.enable = func(caps []string) []string {
		return slices.DeleteFunc(slices.Clone(caps), func(c string) bool { return c == proto.CapObjects })
	}
	dev := &fakeDevices{list: []proto.Object{{Path: "vclu:lamp", Type: "light", Access: proto.AccessFull}}}
	c, conn := connectWith(t, f, func(c *Client) { c.opts.Objects = dev.objects })
	c.ObjectsChanged()
	c.ObjectsRelease()
	roundTrip(t, conn)
	time.Sleep(50 * time.Millisecond)
	if n := dev.calls.Load(); n != 0 {
		t.Errorf("the list was made %d times although the cloud did not enable objects", n)
	}
}

func TestSnapshotParts(t *testing.T) {
	f := newFakeCloud(t)
	var list []proto.Object
	for i := range 600 {
		list = append(list, proto.Object{Path: fmt.Sprintf("vclu:dev%03d", 599-i), Type: "switch",
			Name: strings.Repeat("Urządzenie ", 20), Area: "Salon", Access: proto.AccessFull})
	}
	dev := &fakeDevices{list: list}
	_, conn := connectWith(t, f, func(c *Client) { c.opts.Objects = dev.objects })

	first := readSnapshot(t, conn)
	if first.Parts < 2 {
		t.Fatalf("%d parts for 500 long names", first.Parts)
	}
	got := slices.Clone(first.Objects)
	for p := 2; p <= first.Parts; p++ {
		s := readSnapshot(t, conn)
		if s.Seq != first.Seq || s.Part != p || s.Parts != first.Parts || s.BootID != first.BootID {
			t.Fatalf("part %d = seq %d part %d/%d", p, s.Seq, s.Part, s.Parts)
		}
		got = append(got, s.Objects...)
	}
	if len(got) != proto.MaxObjects || got[0].Path != "vclu:dev000" || got[len(got)-1].Path != "vclu:dev499" {
		t.Errorf("%d objects, %s .. %s; want the first %d by path", len(got), got[0].Path, got[len(got)-1].Path, proto.MaxObjects)
	}
}

// During a Lua reload the list is held; the release sends it once.
func TestSnapshotHeldDuringReload(t *testing.T) {
	f := newFakeCloud(t)
	dev := &fakeDevices{list: []proto.Object{{Path: "vclu:lamp", Type: "light", Access: proto.AccessFull}}}
	var cl *Client
	_, conn := connectWith(t, f, func(c *Client) {
		cl = c
		c.opts.Objects = dev.objects
		c.ObjectsHold() // a reload that began before the connection
	})
	c := cl
	c.ObjectsChanged()
	roundTrip(t, conn)
	time.Sleep(50 * time.Millisecond)
	if n := dev.calls.Load(); n != 0 {
		t.Fatalf("the list was made %d times while held", n)
	}
	dev.set([]proto.Object{{Path: "vclu:lamp", Type: "light", Access: proto.AccessFull}, {Path: "vclu:tv", Type: "switch", Access: proto.AccessFull}})
	c.ObjectsRelease()
	if s := readSnapshot(t, conn); s.Seq != 1 || len(s.Objects) != 2 {
		t.Errorf("snapshot after the release = %+v", s)
	}
}

// stepClock is the clock of commands; each call can move it on.
type stepClock struct {
	mu   sync.Mutex
	t    time.Time
	step time.Duration
}

func (s *stepClock) now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.t
	s.t = s.t.Add(s.step)
	return t
}

func (s *stepClock) add(d time.Duration) {
	s.mu.Lock()
	s.t = s.t.Add(d)
	s.mu.Unlock()
}

func sendCmd(t *testing.T, conn *fakeConn, cmd proto.Cmd) {
	t.Helper()
	cmd.Type = proto.TypeCmd
	if cmd.Actor.User == "" {
		cmd.Actor.User = "cloud:7"
	}
	if cmd.Path == "" {
		cmd.Path = "vclu:lamp"
	}
	if cmd.Action == "" {
		cmd.Action = proto.ActionOn
	}
	if err := proto.Write(conn.ctrl, cmd); err != nil {
		t.Fatal(err)
	}
}

func readResult(t *testing.T, conn *fakeConn) proto.CmdResult {
	t.Helper()
	var r proto.CmdResult
	if err := conn.read(t, proto.TypeCmdResult).Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCmdChecksAndResult(t *testing.T) {
	f := newFakeCloud(t)
	clk := &stepClock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	var got []proto.Cmd
	var deadlines []time.Time
	_, conn := connectWith(t, f, func(c *Client) {
		c.clock = clk.now
		c.opts.Command = func(cmd proto.Cmd, deadline time.Time) proto.CmdResult {
			mu.Lock()
			got, deadlines = append(got, cmd), append(deadlines, deadline)
			mu.Unlock()
			return proto.CmdResult{OK: true, Message: "Włączono"}
		}
	})

	for _, tc := range []struct {
		cmd  proto.Cmd
		want string
	}{
		{proto.Cmd{ID: ""}, proto.ReasonInvalid},
		{proto.Cmd{ID: "a b"}, proto.ReasonInvalid},
		{proto.Cmd{ID: strings.Repeat("x", 65)}, proto.ReasonInvalid},
		{proto.Cmd{ID: "c1", Actor: proto.Actor{User: "anna"}}, proto.ReasonUnknownActor},
		{proto.Cmd{ID: "c2", Actor: proto.Actor{User: "cloud:1234567890123456789"}}, proto.ReasonUnknownActor},
		{proto.Cmd{ID: "c3", Action: "setValue"}, proto.ReasonInvalid},
		{proto.Cmd{ID: "c4", Path: strings.Repeat("p", 257)}, proto.ReasonInvalid},
		{proto.Cmd{ID: "c5", Action: proto.ActionSet}, proto.ReasonInvalid},                    // set without a level
		{proto.Cmd{ID: "c6", Value: level(50)}, proto.ReasonInvalid},                           // a level without set
		{proto.Cmd{ID: "c7", Action: proto.ActionSet, Value: level(101)}, proto.ReasonInvalid}, // out of range
	} {
		sendCmd(t, conn, tc.cmd)
		if r := readResult(t, conn); r.OK || r.Reason != tc.want {
			t.Errorf("%+v = %+v, want %s", tc.cmd, r, tc.want)
		}
	}

	sendCmd(t, conn, proto.Cmd{ID: "ok-1", Pin: "0420", Confirmed: true, Actor: proto.Actor{User: "cloud:7", Roles: []string{"domownicy"}}})
	if r := readResult(t, conn); !r.OK || r.ID != "ok-1" || r.Message != "Włączono" || r.Type != proto.TypeCmdResult {
		t.Errorf("result = %+v", r)
	}
	sendCmd(t, conn, proto.Cmd{ID: "ok-2", TimeoutMs: 3_600_000})
	readResult(t, conn)
	mu.Lock()
	if len(got) != 2 || got[0].Pin != "0420" || !got[0].Confirmed || got[0].Actor.Roles[0] != "domownicy" {
		t.Errorf("Command got %+v", got)
	}
	// the deadline is local: from when vCLU read it, default 7 s, at most 30 s
	if d := deadlines[0].Sub(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)); d != cmdTimeoutDefault {
		t.Errorf("deadline %s after the start, want %s", d, cmdTimeoutDefault)
	}
	if d := deadlines[1].Sub(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)); d != cmdTimeoutMax {
		t.Errorf("deadline %s after the start, want at most %s", d, cmdTimeoutMax)
	}
	mu.Unlock()

	// someone the members list does not name
	proto.Write(conn.ctrl, proto.Members{Type: proto.TypeMembers, Version: 2, Members: []proto.Member{{User: "cloud:7", Role: "owner"}}})
	sendCmd(t, conn, proto.Cmd{ID: "m1", Actor: proto.Actor{User: "cloud:9"}})
	if r := readResult(t, conn); r.Reason != proto.ReasonUnknownActor {
		t.Errorf("not a member = %+v", r)
	}
	sendCmd(t, conn, proto.Cmd{ID: "m2", Actor: proto.Actor{User: "cloud:7"}})
	if r := readResult(t, conn); !r.OK {
		t.Errorf("a member = %+v", r)
	}
}

func TestCmdRateLimit(t *testing.T) {
	f := newFakeCloud(t)
	clk := &stepClock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	var runs atomic.Int32
	_, conn := connectWith(t, f, func(c *Client) {
		c.clock = clk.now
		c.opts.Command = func(proto.Cmd, time.Time) proto.CmdResult { runs.Add(1); return proto.CmdResult{OK: true} }
	})
	for i := range cmdRate {
		sendCmd(t, conn, proto.Cmd{ID: fmt.Sprintf("r%d", i)})
		if r := readResult(t, conn); !r.OK {
			t.Fatalf("command %d = %+v", i, r)
		}
	}
	sendCmd(t, conn, proto.Cmd{ID: "over"})
	if r := readResult(t, conn); r.Reason != proto.ReasonRateLimited || runs.Load() != cmdRate {
		t.Errorf("command %d in the same second = %+v, runs %d", cmdRate+1, r, runs.Load())
	}
	clk.add(time.Second)
	// the refused one did not run, so its id is free to try again
	sendCmd(t, conn, proto.Cmd{ID: "over"})
	if r := readResult(t, conn); !r.OK {
		t.Errorf("a second later = %+v", r)
	}
}

func TestCmdInFlightAndDuplicates(t *testing.T) {
	f := newFakeCloud(t)
	release := make(chan struct{})
	var running, runs atomic.Int32
	_, conn := connectWith(t, f, func(c *Client) {
		c.opts.Command = func(cmd proto.Cmd, _ time.Time) proto.CmdResult {
			runs.Add(1)
			running.Add(1)
			<-release
			return proto.CmdResult{OK: true, Message: cmd.ID}
		}
	})
	sendCmd(t, conn, proto.Cmd{ID: "dup"})
	sendCmd(t, conn, proto.Cmd{ID: "dup"}) // the cloud's retry while the first still runs
	for i := 1; i < cmdInFlight; i++ {
		sendCmd(t, conn, proto.Cmd{ID: fmt.Sprintf("f%d", i)})
	}
	eventually(t, "8 commands running", func() bool { return running.Load() == cmdInFlight })
	sendCmd(t, conn, proto.Cmd{ID: "ninth"})
	if r := readResult(t, conn); r.ID != "ninth" || r.Reason != proto.ReasonRateLimited {
		t.Errorf("a ninth command at once = %+v", r)
	}
	close(release)
	dups := 0
	for range cmdInFlight + 1 {
		r := readResult(t, conn)
		if !r.OK || r.Message != r.ID {
			t.Errorf("result %+v", r)
		}
		if r.ID == "dup" {
			dups++
		}
	}
	if dups != 2 || runs.Load() != cmdInFlight {
		t.Errorf("dup answered %d times, %d runs; want 2 answers and one run each", dups, runs.Load())
	}
	// a repeat after it finished: the stored answer
	sendCmd(t, conn, proto.Cmd{ID: "dup"})
	if r := readResult(t, conn); r.ID != "dup" || !r.OK || runs.Load() != cmdInFlight {
		t.Errorf("late repeat = %+v, runs %d", r, runs.Load())
	}
}

// A command that waited past its deadline before it could run is not run.
func TestCmdExpired(t *testing.T) {
	f := newFakeCloud(t)
	clk := &stepClock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), step: time.Second}
	var runs atomic.Int32
	_, conn := connectWith(t, f, func(c *Client) {
		c.clock = clk.now
		c.opts.Command = func(proto.Cmd, time.Time) proto.CmdResult { runs.Add(1); return proto.CmdResult{OK: true} }
	})
	sendCmd(t, conn, proto.Cmd{ID: "late", TimeoutMs: 500})
	if r := readResult(t, conn); r.Reason != proto.ReasonExpired || runs.Load() != 0 {
		t.Errorf("late = %+v, runs %d", r, runs.Load())
	}
}

// Two notify_action with one id at the same time run the button once.
func TestNotifyActionRepeatWhileRunning(t *testing.T) {
	f := newFakeCloud(t)
	release := make(chan struct{})
	var runs atomic.Int32
	_, conn := connectWith(t, f, func(c *Client) {
		c.opts.NotifyAction = func(proto.NotifyAction) proto.NotifyActionResult {
			runs.Add(1)
			<-release
			return proto.NotifyActionResult{OK: true, Message: "Brama otwarta"}
		}
	})
	a := proto.NotifyAction{Type: proto.TypeNotifyAction, ID: "a1", Notify: "n1", Action: "1",
		Actor: proto.Actor{User: "cloud:7"}, DeadlineMs: time.Now().Add(time.Minute).UnixMilli()}
	proto.Write(conn.ctrl, a)
	proto.Write(conn.ctrl, a)
	eventually(t, "the button running", func() bool { return runs.Load() == 1 })
	roundTrip(t, conn) // the read loop has seen the repeat
	close(release)
	for range 2 {
		var r proto.NotifyActionResult
		conn.read(t, proto.TypeNotifyActionResult).Decode(&r)
		if r.ID != "a1" || !r.OK || r.Message != "Brama otwarta" {
			t.Errorf("answer %+v", r)
		}
	}
	if n := runs.Load(); n != 1 {
		t.Errorf("the button ran %d times", n)
	}
}

func level(v float64) *float64 { return &v }
