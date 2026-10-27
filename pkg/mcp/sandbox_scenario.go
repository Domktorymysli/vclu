package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
	vclulua "github.com/Domktorymysli/vclu/pkg/lua"
	lua "github.com/yuin/gopher-lua"
)

const (
	maxScenarioSteps = 200
	maxAdvance       = 31 * 24 * time.Hour
	// sandboxObjectsVersion must match SandboxObjects.VERSION in Lua.
	sandboxObjectsVersion = 1
)

// SandboxScenario is everything vclu_draft_test can drive besides the drafts.
type SandboxScenario struct {
	Events []InjectEvent
	// At is the virtual start time; zero means now.
	At    time.Time
	Steps []SandboxStep
	// Fixtures replace the live object snapshot when non-nil (CI, unit tests).
	Fixtures []ObjectDescriptor
	// Timezone overrides the zone taken from the live runtime (e.g. "Europe/Warsaw").
	Timezone string
	// Verbose keeps what the runtime prints before the drafts load.
	Verbose bool
}

// ObjectDescriptor rebuilds one integration object in the sandbox.
type ObjectDescriptor struct {
	Path   string         `json:"path"`
	Kind   string         `json:"kind"`
	State  any            `json:"state"`
	Config map[string]any `json:"config,omitempty"`
}

// SandboxStep is one scenario step; exactly one operation per step.
type SandboxStep struct {
	Set            *string          `json:"set,omitempty"`
	Value          json.RawMessage  `json:"value,omitempty"`
	Emit           *string          `json:"emit,omitempty"`
	Data           map[string]any   `json:"data,omitempty"`
	Advance        *float64         `json:"advance,omitempty"` // seconds
	Click          *string          `json:"click,omitempty"`   // Smart Panel or one of its buttons
	Input          string           `json:"input,omitempty"`   // btn1..btn4, btn1:long, swipe:up...
	MQTT           *string          `json:"mqtt,omitempty"`    // a message on this topic, to subscriptions
	Payload        *string          `json:"payload,omitempty"`
	Reload         *bool            `json:"reload,omitempty"`
	ExpectState    map[string]any   `json:"expect_state,omitempty"`
	ExpectCluCalls *[]CluCallExpect `json:"expect_clu_calls,omitempty"`
	ExpectHistory  *HistoryExpect   `json:"expect_history,omitempty"`
}

// HistoryExpect lists every recorded change of Path in the scenario, oldest
// first; each item checks the fields it names (value, old, origin, actor).
type HistoryExpect struct {
	Path    string           `json:"path"`
	Changes []map[string]any `json:"changes"`
}

// CluCallExpect matches a recorded CluCall; an empty Target matches any CLU.
type CluCallExpect struct {
	Target string `json:"target,omitempty"`
	Script string `json:"script"`
}

func (s SandboxStep) op() string {
	var ops []string
	if s.Set != nil {
		ops = append(ops, "set")
	}
	if s.Emit != nil {
		ops = append(ops, "emit")
	}
	if s.Advance != nil {
		ops = append(ops, "advance")
	}
	if s.Click != nil {
		ops = append(ops, "click")
	}
	if s.MQTT != nil {
		ops = append(ops, "mqtt")
	}
	if s.Reload != nil {
		ops = append(ops, "reload")
	}
	if s.ExpectState != nil {
		ops = append(ops, "expect_state")
	}
	if s.ExpectCluCalls != nil {
		ops = append(ops, "expect_clu_calls")
	}
	if s.ExpectHistory != nil {
		ops = append(ops, "expect_history")
	}
	if len(ops) != 1 {
		return ""
	}
	return ops[0]
}

// ParseSteps decodes and validates scenario steps; unknown fields, a step
// without exactly one operation and out-of-range values are rejected.
func ParseSteps(raw []byte) ([]SandboxStep, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("steps must be an array: %w", err)
	}
	if len(items) > maxScenarioSteps {
		return nil, fmt.Errorf("too many steps (%d, max %d)", len(items), maxScenarioSteps)
	}
	steps := make([]SandboxStep, 0, len(items))
	for i, item := range items {
		var s SandboxStep
		dec := json.NewDecoder(bytes.NewReader(item))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		op := s.op()
		switch {
		case op == "":
			return nil, fmt.Errorf("step %d: needs exactly one of set, emit, advance, click, mqtt, reload, expect_state, expect_clu_calls, expect_history", i+1)
		case op == "set" && (*s.Set == "" || len(s.Value) == 0):
			return nil, fmt.Errorf("step %d: set needs a path and a value", i+1)
		case op != "set" && len(s.Value) > 0:
			return nil, fmt.Errorf("step %d: value is only allowed with set", i+1)
		case op != "emit" && s.Data != nil:
			return nil, fmt.Errorf("step %d: data is only allowed with emit", i+1)
		case op == "emit" && *s.Emit == "":
			return nil, fmt.Errorf("step %d: emit needs an event name", i+1)
		case op == "click" && (*s.Click == "" || s.Input == ""):
			return nil, fmt.Errorf("step %d: click needs a panel and an input", i+1)
		case op != "click" && s.Input != "":
			return nil, fmt.Errorf("step %d: input is only allowed with click", i+1)
		case op == "mqtt" && (*s.MQTT == "" || s.Payload == nil):
			return nil, fmt.Errorf("step %d: mqtt needs a topic and a payload", i+1)
		case op != "mqtt" && s.Payload != nil:
			return nil, fmt.Errorf("step %d: payload is only allowed with mqtt", i+1)
		case op == "advance" && (*s.Advance <= 0 || *s.Advance > maxAdvance.Seconds()):
			return nil, fmt.Errorf("step %d: advance must be between 0 and %s", i+1, maxAdvance)
		case op == "reload" && !*s.Reload:
			return nil, fmt.Errorf("step %d: reload must be true", i+1)
		case op == "expect_history" && (s.ExpectHistory.Path == "" || s.ExpectHistory.Changes == nil):
			return nil, fmt.Errorf("step %d: expect_history needs path and changes", i+1)
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// SetObjectSource sets the provider of the live object snapshot: the JSON
// returned by SandboxObjects.export() in the production runtime.
func (se *SandboxEngine) SetObjectSource(fn func() (string, error)) {
	se.objectSource = fn
}

// resolveObjects picks fixtures or the live snapshot once per scenario; the
// live timezone is used either way.
func (run *sandboxRun) resolveObjects(fixtures []ObjectDescriptor) error {
	if fixtures != nil {
		data, err := json.Marshal(fixtures)
		if err != nil {
			return fmt.Errorf("fixtures: %w", err)
		}
		run.objects = string(data)
	}
	if run.se.objectSource == nil {
		return nil
	}
	raw, err := run.se.objectSource()
	if err != nil {
		run.warn("live object snapshot unavailable: %v", err)
		return nil
	}
	var snap struct {
		Version  int               `json:"version"`
		Timezone string            `json:"timezone"`
		Objects  []json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		run.warn("live object snapshot unreadable: %v", err)
		return nil
	}
	run.timezone = snap.Timezone
	if fixtures != nil {
		return nil
	}
	if snap.Version != sandboxObjectsVersion {
		run.warn("live object snapshot version %d, expected %d; integration objects skipped", snap.Version, sandboxObjectsVersion)
		return nil
	}
	data, _ := json.Marshal(snap.Objects)
	run.objects = string(data)
	return nil
}

func (run *sandboxRun) warn(format string, args ...any) {
	if len(run.result.Logs) < maxSandboxLogLines {
		run.result.Logs = append(run.result.Logs, "[WARN] sandbox: "+fmt.Sprintf(format, args...))
	}
}

// loadObjects registers integration objects and the sandbox helpers.
func (run *sandboxRun) loadObjects(L *lua.LState) error {
	if run.objects != "" {
		var list []any
		if err := json.Unmarshal([]byte(run.objects), &list); err != nil {
			return fmt.Errorf("objects: %w", err)
		}
		load := L.GetField(L.GetGlobal("SandboxObjects"), "load")
		if err := L.CallByParam(lua.P{Fn: load, NRet: 1, Protect: true}, goToLua(L, list)); err != nil {
			return fmt.Errorf("objects: %w", err)
		}
		warnings := L.Get(-1)
		L.Pop(1)
		if run.loads == 1 {
			if t, ok := warnings.(*lua.LTable); ok {
				t.ForEach(func(_, w lua.LValue) { run.warn("%s", w.String()) })
			}
		}
	}
	return L.DoString(sandboxHelpers)
}

// runSteps executes the scenario steps; failed expectations are collected in
// result.Errors and do not stop later steps. Returns the current state, which
// differs from L after a reload.
func (run *sandboxRun) runSteps(L *lua.LState, steps []SandboxStep, cluMark int) (*lua.LState, error) {
	result := run.result
	fail := func(i int, op, format string, args ...any) {
		result.Errors = append(result.Errors, fmt.Sprintf("step %d (%s): %s", i+1, op, fmt.Sprintf(format, args...)))
	}

	for i, s := range steps {
		if err := run.ctx.Err(); err != nil {
			return L, err
		}
		op := s.op()
		run.loggedErrors = run.loggedErrors[:0]
		switch op {
		case "set":
			var v any
			if err := json.Unmarshal(s.Value, &v); err != nil {
				fail(i, op, "value: %v", err)
				continue
			}
			fn := L.GetGlobal("__sandbox_set")
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, lua.LString(*s.Set), goToLua(L, v)); err != nil {
				fail(i, op, "%v", err)
			}

		case "emit":
			if err := run.se.injectEvent(L, InjectEvent{Name: *s.Emit, Data: s.Data}); err != nil {
				fail(i, op, "%v", err)
			}

		case "click":
			// what a CLU sends when a panel button is pressed; drawing happens on timers,
			// so an advance step afterwards shows the frame in clu_calls
			rp, _ := L.GetGlobal("RemotePanel").(*lua.LTable)
			if rp == nil {
				fail(i, op, "RemotePanel is not loaded")
				continue
			}
			if err := L.CallByParam(lua.P{Fn: L.GetField(rp, "click"), NRet: 1, Protect: true},
				lua.LString(*s.Click), lua.LString(s.Input)); err != nil {
				fail(i, op, "%v", err)
				continue
			}
			ok := L.Get(-1)
			L.Pop(1)
			if ok != lua.LTrue {
				fail(i, op, "no panel %q handles input %q (a Panel.new on its RemotePanel, from the OM import?)", *s.Click, s.Input)
			}

		case "mqtt":
			// as the broker delivers it, to every client (MQTT and MQTT:new ones);
			// the clients are blocked here, their subscriptions are kept
			if L.GetGlobal("MQTT") == lua.LNil {
				fail(i, op, "MQTT is not loaded")
				continue
			}
			if err := L.CallByParam(lua.P{Fn: L.GetGlobal("__sandbox_mqtt"), NRet: 0, Protect: true},
				lua.LString(*s.MQTT), lua.LString(*s.Payload)); err != nil {
				fail(i, op, "%v", err)
			}

		case "advance":
			d := time.Duration(*s.Advance * float64(time.Second))
			// A stopped advance leaves due timers behind; later steps would be meaningless
			if err := run.timers.advance(L, d, func(err error) { fail(i, op, "%v", err) }); err != nil {
				return L, fmt.Errorf("step %d (advance): %w", i+1, err)
			}

		case "reload":
			run.history.BeginReload()
			// as LuaEngine does before a reload: pending persist() writes land in KV
			if err := L.DoString(`if Persist then Persist.flushAll() end`); err != nil {
				fail(i, op, "flush: %v", err)
			}
			L.Close()
			var err error
			if L, err = run.load(); err != nil {
				return L, fmt.Errorf("step %d (reload): %w", i+1, err)
			}

		case "expect_state":
			paths := make([]string, 0, len(s.ExpectState))
			for p := range s.ExpectState {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			fn := L.GetGlobal("__sandbox_state")
			for _, p := range paths {
				if err := L.CallByParam(lua.P{Fn: fn, NRet: 2, Protect: true}, lua.LString(p)); err != nil {
					fail(i, op, "%s: %v", p, err)
					continue
				}
				found := L.Get(-1) == lua.LTrue
				got := luaValueToGo(L.Get(-2))
				L.Pop(2)
				switch {
				case !found:
					fail(i, op, "no object '%s'", p)
				case !stateEqual(got, s.ExpectState[p]):
					fail(i, op, "%s = %s, want %s", p, jsonText(got), jsonText(s.ExpectState[p]))
				}
			}

		case "expect_history":
			fn := L.GetGlobal("__sandbox_history_path")
			path := s.ExpectHistory.Path
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, lua.LString(path)); err == nil {
				path = L.Get(-1).String()
				L.Pop(1)
			}
			entries := run.history.Query(history.Query{Path: path, Limit: history.MaxQueryLimit})
			if msg := matchHistory(entries, s.ExpectHistory.Changes); msg != "" {
				fail(i, op, "%s: %s", path, msg)
			}

		case "expect_clu_calls":
			got := result.CluCalls[cluMark:]
			cluMark = len(result.CluCalls)
			if result.CluCallsDropped > 0 {
				fail(i, op, "clu_calls truncated at %d calls (%d dropped), cannot compare", maxCluCalls, result.CluCallsDropped)
			} else if msg := matchCluCalls(got, *s.ExpectCluCalls); msg != "" {
				fail(i, op, "%s", msg)
			}
		}
		// Handlers on EventBus/StateBus only log their errors; the step fails
		for _, e := range run.loggedErrors {
			fail(i, op, "runtime logged an error: %s", e)
		}
	}
	return L, nil
}

// matchCluCalls compares calls recorded since the previous expect_clu_calls
// step (or since the drafts were loaded, inject_events included) with the
// expected list, in order.
func matchCluCalls(got []CluCall, want []CluCallExpect) string {
	ok := len(got) == len(want)
	for i := 0; ok && i < len(want); i++ {
		ok = got[i].Script == want[i].Script && (want[i].Target == "" || got[i].Target == want[i].Target)
	}
	if ok {
		return ""
	}
	const shown = 10
	var lines []string
	for i, c := range got {
		if i == shown {
			lines = append(lines, fmt.Sprintf("... %d more", len(got)-shown))
			break
		}
		lines = append(lines, c.Target+" "+c.Script)
	}
	return fmt.Sprintf("got %d calls [%s], want %s", len(got), strings.Join(lines, "; "), jsonText(want))
}

// matchHistory compares entries (newest first, as Query returns them) with
// the expected changes, oldest first; only named fields are checked.
func matchHistory(entries []history.Entry, want []map[string]any) string {
	got := make([]map[string]any, len(entries))
	for i, e := range entries {
		got[len(entries)-1-i] = map[string]any{"value": e.Value, "old": e.Old, "origin": e.Origin, "actor": e.Actor}
	}
	ok := len(got) == len(want)
	for i := 0; ok && i < len(want); i++ {
		for k, v := range want[i] {
			if _, known := got[i][k]; !known || !stateEqual(got[i][k], v) {
				ok = false
			}
		}
	}
	if ok {
		return ""
	}
	return fmt.Sprintf("got %s, want %s", jsonText(got), jsonText(want))
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// goToLua converts decoded JSON values into Lua values.
func goToLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case int:
		return lua.LNumber(x)
	case int64:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case []any:
		t := L.NewTable()
		for i, item := range x {
			t.RawSetInt(i+1, goToLua(L, item)) // Append would skip nil and close the hole
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, item := range x {
			t.RawSetString(k, goToLua(L, item))
		}
		return t
	default:
		return lua.LString(fmt.Sprint(x))
	}
}

// sandboxKV is an in-memory KV for one scenario: it survives a reload step,
// never touches disk and ignores TTL.
type sandboxKV struct {
	data map[string]map[string]any
}

func newSandboxKV() *sandboxKV {
	return &sandboxKV{data: map[string]map[string]any{}}
}

func (kv *sandboxKV) store(id string) map[string]any {
	if kv.data[id] == nil {
		kv.data[id] = map[string]any{}
	}
	return kv.data[id]
}

func (kv *sandboxKV) install(L *lua.LState) {
	L.SetGlobal("__go_kv_get", L.NewFunction(func(L *lua.LState) int {
		v, ok := kv.store(L.CheckString(1))[L.CheckString(2)]
		if !ok {
			L.Push(lua.LNil)
			L.Push(lua.LFalse)
			return 2
		}
		L.Push(goToLua(L, v))
		L.Push(lua.LTrue)
		return 2
	}))
	L.SetGlobal("__go_kv_set", L.NewFunction(func(L *lua.LState) int {
		v, err := vclulua.LuaToGo(L.Get(3))
		if err != nil {
			L.Push(lua.LString("kv: " + err.Error()))
			return 1
		}
		kv.store(L.CheckString(1))[L.CheckString(2)] = v
		L.Push(lua.LNil)
		return 1
	}))
	L.SetGlobal("__go_kv_delete", L.NewFunction(func(L *lua.LState) int {
		delete(kv.store(L.CheckString(1)), L.CheckString(2))
		L.Push(lua.LNil)
		return 1
	}))
	L.SetGlobal("__go_kv_has", L.NewFunction(func(L *lua.LState) int {
		_, ok := kv.store(L.CheckString(1))[L.CheckString(2)]
		L.Push(lua.LBool(ok))
		return 1
	}))
	L.SetGlobal("__go_kv_list", L.NewFunction(func(L *lua.LState) int {
		prefix := L.OptString(2, "")
		var keys []string
		for k := range kv.store(L.CheckString(1)) {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		t := L.NewTable()
		for _, k := range keys {
			t.Append(lua.LString(k))
		}
		L.Push(t)
		return 1
	}))
	L.SetGlobal("__go_kv_get_all", L.NewFunction(func(L *lua.LState) int {
		t := L.NewTable()
		for k, v := range kv.store(L.CheckString(1)) {
			t.RawSetString(k, goToLua(L, v))
		}
		L.Push(t)
		return 1
	}))
}
