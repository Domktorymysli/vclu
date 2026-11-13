package proto

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Hello{Type: TypeHello, Proto: Version, Version: "1.4.0", BootID: "b1", Caps: []string{CapDashboards}, Clock: Clock{NowMs: 5, Synced: true}}
	if err := Write(&buf, in); err != nil {
		t.Fatal(err)
	}
	Write(&buf, Bye{Type: TypeBye, Reason: "update"})
	buf.WriteString(`{"type":"from_the_future","x":1}` + "\n")

	r := NewReader(&buf)
	m, err := r.Read()
	if err != nil || m.Type != TypeHello {
		t.Fatalf("first: %v %v", m.Type, err)
	}
	var out Hello
	if err := m.Decode(&out); err != nil || out.BootID != "b1" || !out.Clock.Synced || out.Caps[0] != CapDashboards {
		t.Fatalf("decoded %+v, %v", out, err)
	}
	if m, _ := r.Read(); m.Type != TypeBye {
		t.Errorf("second type %q", m.Type)
	}
	if m, err := r.Read(); err != nil || m.Type != "from_the_future" {
		t.Errorf("unknown type: %q %v (must be returned, not an error)", m.Type, err)
	}
	if _, err := r.Read(); err != io.EOF {
		t.Errorf("end: %v, want EOF", err)
	}
}

func TestReaderLimits(t *testing.T) {
	big := `{"type":"x","pad":"` + strings.Repeat("a", MaxMessageSize) + `"}` + "\n"
	if _, err := NewReader(strings.NewReader(big)).Read(); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized: %v", err)
	}
	if _, err := NewReader(strings.NewReader("{\"a\":1}\n")).Read(); err == nil {
		t.Error("message without type accepted")
	}
	if _, err := NewReader(strings.NewReader("not json\n")).Read(); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := NewReader(strings.NewReader(`{"type":"hello"}`)).Read(); err != io.ErrUnexpectedEOF {
		t.Errorf("truncated: %v", err)
	}
}

func TestSnapshotAndCmdRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	snap := Snapshot{Type: TypeSnapshot, BootID: "b1", Seq: 2, Part: 1, Parts: 1, Objects: []Object{
		{Path: "CLU1.DOU1", Type: "light", Name: "Lampka", Area: "Biuro", Access: AccessFull, Roles: []string{"rodzina"}},
		{Path: "satel:1", Type: "switch", Name: "Alarm", Access: AccessPin},
	}}
	cmd := Cmd{Type: TypeCmd, ID: "c1", Path: "satel:1", Action: ActionOff, Pin: "1234", TimeoutMs: 7000,
		Actor: Actor{User: "cloud:7", Role: "owner", Roles: []string{"rodzina"}, Via: "voice"}}
	half := 50.0
	set := Cmd{Type: TypeCmd, ID: "c2", Path: "CLU1.ROL1", Action: ActionSet, Value: &half, TimeoutMs: 7000}
	for _, m := range []any{snap, cmd, CmdResult{Type: TypeCmdResult, ID: "c1", Reason: ReasonPinWrong}, set} {
		if err := Write(&buf, m); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(&buf)
	var s Snapshot
	if m, _ := r.Read(); m.Type != TypeSnapshot || m.Decode(&s) != nil || len(s.Objects) != 2 || s.Objects[1].Access != AccessPin || s.Objects[1].Roles != nil {
		t.Fatalf("snapshot %+v", s)
	}
	var c Cmd
	if m, _ := r.Read(); m.Type != TypeCmd || m.Decode(&c) != nil || c.Actor.Roles[0] != "rodzina" || c.Pin != "1234" || c.TimeoutMs != 7000 {
		t.Fatalf("cmd %+v", c)
	}
	var res CmdResult
	if m, _ := r.Read(); m.Type != TypeCmdResult || m.Decode(&res) != nil || res.OK || res.Reason != ReasonPinWrong {
		t.Fatalf("result %+v", res)
	}
	if c.Value != nil {
		t.Errorf("value without set: %v", *c.Value)
	}
	var sc Cmd
	if m, _ := r.Read(); m.Decode(&sc) != nil || sc.Value == nil || *sc.Value != 50 {
		t.Fatalf("set %+v", sc)
	}
}
