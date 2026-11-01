package cloud

import (
	"crypto/sha256"
	"encoding/json"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
)

// Remote devices (protocol section 5, "snapshot", "cmd"): vCLU sends the devices exposed with
// :remote() in Lua and runs the commands the cloud sends for them. Lua checks each command
// (Cloud._command); here are the limits of the connection and the at-most-once answers.

const (
	// objectsDebounce coalesces a burst of changes (a script exposing fifty devices) into one
	// snapshot.
	objectsDebounce = time.Second
	// objectsHoldMax ends a hold that was never released (a reload that failed half way).
	objectsHoldMax = 2 * time.Minute

	cmdTimeoutDefault = 7 * time.Second // a cmd without timeout_ms
	cmdTimeoutMax     = 30 * time.Second
	cmdRate           = 10 // commands a second per connection, bursts up to as many
	cmdInFlight       = 8  // commands running at once per connection
	cmdIDMax          = 64
	cmdPathMax        = 256
	replaysFor        = 10 * time.Minute // answers to cmd and notify_action are kept this long
	replaysMax        = 1024
)

var (
	cmdIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	cmdActorRe = regexp.MustCompile(`^cloud:[0-9]{1,18}$`)
	cmdActions = []string{proto.ActionOn, proto.ActionOff, proto.ActionOpen, proto.ActionClose, proto.ActionStop, proto.ActionExecute, proto.ActionSet}
)

// ObjectsChanged marks the device list changed: it is sent after the debounce. Called from the
// Lua loop on every change, so it never blocks and never calls Lua.
func (c *Client) ObjectsChanged() {
	select {
	case c.objKick <- struct{}{}:
	default:
	}
}

// ObjectsHold holds the device list back while Lua reloads, so that a list made half way through
// the scripts never reaches the cloud. ObjectsRelease ends it and sends the list at once.
func (c *Client) ObjectsHold() {
	c.mu.Lock()
	c.objHeldUntil = time.Now().Add(objectsHoldMax)
	c.mu.Unlock()
}

// ObjectsRelease ends ObjectsHold and sends the device list (when it changed).
func (c *Client) ObjectsRelease() {
	c.mu.Lock()
	c.objHeldUntil = time.Time{}
	c.mu.Unlock()
	select {
	case c.objNow <- struct{}{}:
	default:
	}
}

// objectsHeld says how long the device list is still held back; 0 when it is not.
func (c *Client) objectsHeld() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.objHeldUntil.IsZero() {
		return 0
	}
	return max(time.Until(c.objHeldUntil), 0)
}

// sendObjects runs for one connection with objects enabled: the device list after welcome, after
// each change (debounced) and on ObjectsRelease, each time only when it differs from the last one
// sent on this connection.
func (c *Client) sendObjects(t *tunnel, done <-chan struct{}) {
	var last [sha256.Size]byte
	sent := false
	var fire <-chan time.Time
	send := func() {
		if wait := c.objectsHeld(); wait > 0 {
			fire = time.After(wait)
			return
		}
		fire = nil
		list, err := c.opts.Objects()
		if err != nil {
			log.Printf("[Cloud] remote devices: %v", err)
			return
		}
		objs, sum := snapshotObjects(list)
		if sent && sum == last {
			return
		}
		parts := c.snapshotParts(objs)
		for _, p := range parts {
			if err := t.send(p); err != nil {
				log.Printf("[Cloud] snapshot part %d/%d not sent: %v", p.Part, p.Parts, err)
				return
			}
		}
		last, sent = sum, true
	}
	send()
	for {
		select {
		case <-done:
			return
		case <-c.objKick:
			if fire == nil {
				fire = time.After(c.opts.ObjectsDebounce)
			}
		case <-c.objNow:
			send()
		case <-fire:
			send()
		}
	}
}

// snapshotObjects sorts the list by path, keeps what fits the protocol's limits and returns it
// with a hash of its encoding.
func snapshotObjects(list []proto.Object) ([]proto.Object, [sha256.Size]byte) {
	list = slices.Clone(list)
	slices.SortStableFunc(list, func(a, b proto.Object) int { return strings.Compare(a.Path, b.Path) })
	if len(list) > proto.MaxObjects {
		log.Printf("[Cloud] %d remote devices, the cloud gets the first %d by path", len(list), proto.MaxObjects)
		list = list[:proto.MaxObjects]
	}
	h := sha256.New()
	out := list[:0]
	for _, o := range list {
		b, _ := json.Marshal(o)
		if len(b) > proto.SnapshotPartBytes/4 {
			log.Printf("[Cloud] remote device %.64q left out: its description is too long", o.Path)
			continue
		}
		h.Write(b)
		h.Write([]byte{'\n'})
		out = append(out, o)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return out, sum
}

// snapshotParts splits the list into snapshot messages of at most proto.SnapshotPartBytes each,
// all with the next Seq; past proto.MaxSnapshotParts the rest is left out.
func (c *Client) snapshotParts(objs []proto.Object) []proto.Snapshot {
	c.mu.Lock()
	c.objSeq++
	seq := c.objSeq
	c.mu.Unlock()
	head, _ := json.Marshal(proto.Snapshot{Type: proto.TypeSnapshot, BootID: c.bootID, Seq: seq,
		Part: proto.MaxSnapshotParts, Parts: proto.MaxSnapshotParts, Objects: []proto.Object{}})
	var parts [][]proto.Object
	cur, size := []proto.Object{}, len(head)
	for _, o := range objs {
		b, _ := json.Marshal(o)
		if len(cur) > 0 && size+len(b)+1 > proto.SnapshotPartBytes {
			parts = append(parts, cur)
			cur, size = []proto.Object{}, len(head)
		}
		cur = append(cur, o)
		size += len(b) + 1
	}
	parts = append(parts, cur)
	if len(parts) > proto.MaxSnapshotParts {
		n := 0
		for _, p := range parts[proto.MaxSnapshotParts:] {
			n += len(p)
		}
		log.Printf("[Cloud] remote devices do not fit %d snapshot parts, %d left out", proto.MaxSnapshotParts, n)
		parts = parts[:proto.MaxSnapshotParts]
	}
	out := make([]proto.Snapshot, len(parts))
	for i, p := range parts {
		out[i] = proto.Snapshot{Type: proto.TypeSnapshot, BootID: c.bootID, Seq: seq, Part: i + 1, Parts: len(parts), Objects: p}
	}
	return out
}

// cmdLimiter is a token bucket: cmdRate commands a second, bursts of as many.
type cmdLimiter struct {
	tokens float64
	last   time.Time
}

func (l *cmdLimiter) allow(now time.Time) bool {
	if l.last.IsZero() {
		l.tokens = cmdRate
	} else if d := now.Sub(l.last); d > 0 {
		l.tokens = min(cmdRate, l.tokens+d.Seconds()*cmdRate)
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// command answers one cmd. It runs on the read loop: the checks here are quick, the command
// itself runs on its own goroutine.
func (c *Client) command(t *tunnel, cmd proto.Cmd, done <-chan struct{}) {
	answer := func(reason string) {
		id := cmd.ID
		if len(id) > cmdIDMax {
			id = id[:cmdIDMax]
		}
		if err := t.send(proto.CmdResult{Type: proto.TypeCmdResult, ID: id, Reason: reason}); err != nil {
			log.Printf("[Cloud] cmd_result %.64q not sent: %v", id, err)
		}
	}
	switch {
	case !cmdIDRe.MatchString(cmd.ID):
		answer(proto.ReasonInvalid)
		return
	case !cmdActorRe.MatchString(cmd.Actor.User) || !c.isMember(cmd.Actor.User):
		answer(proto.ReasonUnknownActor)
		return
	case !slices.Contains(cmdActions, cmd.Action), cmd.Path == "", len(cmd.Path) > cmdPathMax,
		(cmd.Action == proto.ActionSet) != (cmd.Value != nil),
		cmd.Value != nil && !(*cmd.Value >= 0 && *cmd.Value <= 100):
		answer(proto.ReasonInvalid)
		return
	}
	now := c.clock()
	timeout := time.Duration(cmd.TimeoutMs) * time.Millisecond
	if cmd.TimeoutMs <= 0 {
		timeout = cmdTimeoutDefault
	}
	deadline := now.Add(min(timeout, cmdTimeoutMax))

	e, first := c.cmds.claim(cmd.ID)
	if !first {
		go func() { // a repeat: the first answer, once there is one
			select {
			case <-e.done:
				t.send(e.res)
			case <-done:
			}
		}()
		return
	}
	refuse := func(reason string) {
		res := proto.CmdResult{Type: proto.TypeCmdResult, ID: cmd.ID, Reason: reason}
		c.cmds.finish(cmd.ID, e, res, false)
		t.send(res)
	}
	if !t.cmdRate.allow(now) {
		refuse(proto.ReasonRateLimited)
		return
	}
	if t.cmdRunning.Add(1) > cmdInFlight {
		t.cmdRunning.Add(-1)
		refuse(proto.ReasonRateLimited)
		return
	}
	go func() {
		defer t.cmdRunning.Add(-1)
		var res proto.CmdResult
		switch {
		case !c.clock().Before(deadline):
			res = proto.CmdResult{Reason: proto.ReasonExpired}
		case c.opts.Command == nil:
			res = proto.CmdResult{Reason: proto.ReasonNotFound}
		default:
			res = c.opts.Command(cmd, deadline)
		}
		res.Type, res.ID = proto.TypeCmdResult, cmd.ID
		c.cmds.finish(cmd.ID, e, res, true)
		if err := t.send(res); err != nil {
			log.Printf("[Cloud] cmd_result %s not sent: %v", cmd.ID, err)
		}
	}()
}

// states answers one states request. Like a command it counts against the connection's rate and
// the commands in flight; reading runs on its own goroutine.
func (c *Client) states(t *tunnel, r proto.States) {
	answer := func(res proto.StatesResult) {
		res.Type, res.ID = proto.TypeStatesResult, r.ID
		if err := t.send(res); err != nil {
			log.Printf("[Cloud] states_result %.64q not sent: %v", r.ID, err)
		}
	}
	switch {
	case !cmdIDRe.MatchString(r.ID):
		r.ID = ""
		answer(proto.StatesResult{Reason: proto.ReasonInvalid})
		return
	case !cmdActorRe.MatchString(r.Actor.User) || !c.isMember(r.Actor.User):
		answer(proto.StatesResult{Reason: proto.ReasonUnknownActor})
		return
	case len(r.Paths) > proto.MaxStatePaths || c.opts.States == nil:
		answer(proto.StatesResult{Reason: proto.ReasonInvalid})
		return
	case !t.cmdRate.allow(c.clock()):
		answer(proto.StatesResult{Reason: proto.ReasonRateLimited})
		return
	}
	if t.cmdRunning.Add(1) > cmdInFlight {
		t.cmdRunning.Add(-1)
		answer(proto.StatesResult{Reason: proto.ReasonRateLimited})
		return
	}
	var paths []string
	for _, p := range r.Paths {
		if p != "" && len(p) <= cmdPathMax && !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	roles := r.Actor.Roles
	if roles == nil {
		roles = []string{}
	}
	go func() {
		defer t.cmdRunning.Add(-1)
		answer(proto.StatesResult{States: c.opts.States(paths, roles)})
	}()
}

// setMembers keeps who is in the home, from the members list, for cmd's actor check.
func (c *Client) setMembers(ms proto.Members) {
	users := make(map[string]bool, len(ms.Members))
	for _, m := range ms.Members {
		users[m.User] = true
	}
	c.mu.Lock()
	if c.members == nil || ms.Version >= c.membersVersion {
		c.members, c.membersVersion = users, ms.Version
	}
	c.mu.Unlock()
}

// isMember says whether the last members list on this connection names user; true before the
// first list (the cloud checked the person anyway).
func (c *Client) isMember(user string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.members == nil || c.members[user]
}
