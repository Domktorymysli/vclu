package proto

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Control channel message types (docs/protocol.md, section 5).
const (
	TypeHello      = "hello"      // vCLU → cloud, first message
	TypeWelcome    = "welcome"    // cloud → vCLU, answer to hello
	TypeHealth     = "health"     // vCLU → cloud, every HealthInterval
	TypeBye        = "bye"        // vCLU → cloud, before a planned restart
	TypeReconnect  = "reconnect"  // cloud → vCLU, before a cloud restart
	TypeUnpair     = "unpair"     // either side: the pairing is gone
	TypeError      = "error"      // either side: a request that could not be handled
	TypeDashboards = "dashboards" // vCLU → cloud, after welcome and every Lua reload

	TypeNotify             = "notify"               // vCLU → cloud: a notification for the members
	TypeNotifyAction       = "notify_action"        // cloud → vCLU: a person tapped one of its actions
	TypeNotifyActionResult = "notify_action_result" // vCLU → cloud, answer to notify_action
	TypePresence           = "presence"             // cloud → vCLU: who is at home, after welcome and on every change
	TypeMembers            = "members"              // cloud → vCLU: the home's people and their apps, after welcome and on every change

	TypeSnapshot  = "snapshot"   // vCLU → cloud: the devices exposed with :remote(), after welcome and on every change
	TypeCmd       = "cmd"        // cloud → vCLU: a command for one of them
	TypeCmdResult = "cmd_result" // vCLU → cloud, answer to cmd

	TypeStates       = "states"        // cloud → vCLU: the state of some of them, for a person
	TypeStatesResult = "states_result" // vCLU → cloud, answer to states

	TypeMetrics    = "metrics"     // vCLU → cloud: one-minute aggregates of the series Lua marks cloud=true
	TypeMetricsAck = "metrics_ack" // cloud → vCLU, answer to metrics

	TypeMetricsQuery  = "metrics_query"  // vCLU → cloud: stored buckets of a series, for a chart longer than vCLU keeps
	TypeMetricsPoints = "metrics_points" // cloud → vCLU, answer to metrics_query
)

// Capabilities a vCLU can offer in Hello.Caps.
const (
	CapDashboards = "dashboards"
	CapObjects    = "objects"
	CapCmd        = "cmd"
	CapHTTP       = "http"
	CapHistory    = "history"
	CapNotify     = "notify"
	CapPresence   = "presence"
	CapLocal      = "local" // local tokens for the app on the LAN (PathLocalToken), checked against Members
	CapStates     = "states"
	CapMetrics    = "metrics" // enabled only for homes whose flag allows it; Welcome.Metrics has the limits
)

// HealthIntervalSeconds is how often vCLU sends Health.
const HealthIntervalSeconds = 60

// WebSocket close codes (section 4).
const (
	CloseProtocolError  = 4400 // reconnect with backoff
	CloseUnpaired       = 4401 // token revoked: delete it, stop
	CloseReplaced       = 4409 // another connection with this token took over: stop
	CloseUnsupported    = 4426 // protocol version not supported: stop, ask for an update
	CloseRateLimited    = 4429 // reconnect after the delay in the reason
	CloseServerStopping = 1001 // reconnect with backoff
)

type Hello struct {
	Type    string   `json:"type"`
	Proto   int      `json:"proto"`
	Version string   `json:"version"`
	BootID  string   `json:"boot_id"`
	Caps    []string `json:"caps"`
	Clock   Clock    `json:"clock"`
}

type Clock struct {
	NowMs  int64 `json:"now_ms"`
	Synced bool  `json:"synced"` // false until NTP synced (a Pi has no RTC)
}

type Welcome struct {
	Type     string   `json:"type"`
	Proto    int      `json:"proto"`
	HomeID   int64    `json:"home_id"`
	HomeName string   `json:"home_name"`
	ServerMs int64    `json:"server_ms"`
	Enable   []string `json:"enable"` // capabilities turned on for this home
	// Metrics are this home's limits, present when CapMetrics is enabled.
	Metrics *MetricsLimits `json:"metrics,omitempty"`
}

type Health struct {
	Type         string            `json:"type"`
	UptimeS      int64             `json:"uptime_s"`
	CLUs         []CLUHealth       `json:"clus,omitempty"`
	Integrations map[string]string `json:"integrations,omitempty"` // name → "ok", "error", "off"
	MemMB        int               `json:"mem_mb,omitempty"`
}

type CLUHealth struct {
	ID string `json:"id"`
	OK bool   `json:"ok"`
}

type Bye struct {
	Type        string `json:"type"`
	Reason      string `json:"reason"` // "update", "restart", "shutdown", ByeDisabled
	ExpectBackS int    `json:"expect_back_s,omitempty"`
}

// ByeDisabled: the owner switched the cloud off in vCLU. It stays paired and
// offline until switched on again; the cloud does not alert about it.
const ByeDisabled = "disabled"

type Reconnect struct {
	Type   string `json:"type"`
	AfterS int    `json:"after_s"`
}

type Unpair struct {
	Type string `json:"type"`
}

// Dashboards lists the dashboards vCLU serves remotely (section 8). The cloud
// shows them as tiles; vCLU checks every request itself.
type Dashboards struct {
	Type       string          `json:"type"`
	Dashboards []DashboardInfo `json:"dashboards"`
}

type DashboardInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Icon     string   `json:"icon,omitempty"`
	Roles    []string `json:"roles"`              // roles that see it, expanded by vCLU
	Readonly []string `json:"readonly,omitempty"` // roles that see it without acting
	Pin      bool     `json:"pin,omitempty"`      // remote viewers also enter the dashboard PIN
}

// MaxDashboards bounds the list in one Dashboards message.
const MaxDashboards = 100

// Notify asks the cloud to tell the home's members (section 5, "notify").
// The cloud picks the recipients, sends the push and remembers who got it,
// so that only they can run its actions. Over the limits it answers with an
// Error carrying the same ID.
type Notify struct {
	Type     string         `json:"type"`
	ID       string         `json:"id"` // unique per vCLU; notify_action names it
	Title    string         `json:"title"`
	Body     string         `json:"body,omitempty"`
	Priority string         `json:"priority,omitempty"` // PriorityNormal (default) or PriorityHigh
	To       string         `json:"to,omitempty"`       // ToAll (default), ToAdmins or ToUsers
	Users    []string       `json:"users,omitempty"`    // "cloud:<id>", with ToUsers
	Tag      string         `json:"tag,omitempty"`      // a newer notification with the same tag replaces the older
	Open     string         `json:"open,omitempty"`     // what a tap opens: "/d/<dashboard>"
	Actions  []NotifyButton `json:"actions,omitempty"`  // at most MaxNotifyActions
}

const (
	PriorityNormal = "normal"
	PriorityHigh   = "high" // sounds and shows at once (an alarm, a leak)

	ToAll    = "all"
	ToAdmins = "admins" // the owner and the admins
	ToUsers  = "users"  // the members listed in Notify.Users

	MaxNotifyActions = 3 // what Android shows
)

// NotifyButton is a button on the notification. vCLU runs it when the
// cloud sends NotifyAction.
type NotifyButton struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// NotifyAction runs a notification's button for the person who tapped it,
// after the cloud checked they were among its recipients. vCLU checks the
// rest (who may do it, whether it still means anything) and answers with
// NotifyActionResult, at most once per ID, like cmd.
type NotifyAction struct {
	Type       string `json:"type"`
	ID         string `json:"id"`     // this request
	Notify     string `json:"notify"` // Notify.ID
	Action     string `json:"action"` // NotifyButton.ID
	Actor      Actor  `json:"actor"`
	DeadlineMs int64  `json:"deadline_ms"`
}

type Actor struct {
	User string `json:"user"`           // "cloud:<id>"
	Name string `json:"name,omitempty"` // as in members
	Role string `json:"role,omitempty"` // owner, admin, member
	// Roles are the home's own roles the person has, for Cmd: vCLU checks a device's :roles()
	// against them. The cloud is their only source, as for dashboards.
	Roles []string `json:"roles,omitempty"`
	Via   string   `json:"via,omitempty"` // "app", "voice", "home_app"
	// App is the home's app that acts for the person (Via "home_app"): a third-party program with
	// a token of its own, so vCLU's history tells it from the person's phone.
	App *ActorApp `json:"app,omitempty"`
}

// ActorApp is a home app: its id in the cloud and the name its member gave it.
type ActorApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type NotifyActionResult struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`  // with !OK: ReasonForbidden, ReasonNotFound, ReasonExpired, ReasonError
	Message string `json:"message,omitempty"` // shown to the person (Polish), either way
}

// Presence is everyone of the home who shares it from the app, each time in
// full: vCLU compares it with the last one. Members who do not share it are
// not listed. The phone decides "home" from a geofence it keeps itself; the
// cloud never learns where the home or the phone is.
type Presence struct {
	Type   string   `json:"type"`
	People []Person `json:"people"`
}

type Person struct {
	User  string `json:"user"` // "cloud:<id>"
	Name  string `json:"name"`
	State string `json:"state"` // StateHome or StateAway
	Since int64  `json:"since"` // unix seconds of the last change
}

const (
	StateHome = "home"
	StateAway = "away"
)

// Members is everyone of the home, each time in full (section 6a). vCLU keeps
// the last one on disk and accepts a local token only while its person and
// installation are listed, so signing a phone out or removing a member ends
// its access on the LAN with the next Members. Version only grows: vCLU
// ignores one older than what it has.
type Members struct {
	Type    string   `json:"type"`
	Version int64    `json:"version"`
	Members []Member `json:"members"`
}

type Member struct {
	User string `json:"user"` // "cloud:<id>"
	Name string `json:"name"`
	Role string `json:"role"` // "owner", "admin", "member"
	// Roles are the home's own roles the person has (section 6a): vCLU checks a dashboard's
	// :roles() against them for the app on the LAN, where the cloud is not in the way.
	Roles []string `json:"roles,omitempty"`
	Apps  []App    `json:"apps,omitempty"`
}

// App is an installation of the vCLU app with a local token for this home.
type App struct {
	Install string `json:"install"` // 32 hex characters, stable for one signed-in app
	Client  string `json:"client"`  // "vclu-app"
}

// ClientApp is App.Client of the official app.
const ClientApp = "vclu-app"

// Snapshot is every device vCLU exposes with :remote() (section 5, "snapshot"), in full each
// time, split into Parts when it does not fit one message: the cloud replaces its list once it
// has all parts of one Seq. No values yet. vCLU checks every Cmd itself; the list only tells the
// cloud what to offer to whom.
type Snapshot struct {
	Type    string   `json:"type"`
	BootID  string   `json:"boot_id"` // vCLU's, as in Hello
	Seq     int64    `json:"seq"`     // grows with every snapshot of one BootID
	Part    int      `json:"part"`    // 1..Parts
	Parts   int      `json:"parts"`
	Objects []Object `json:"objects"`
}

// Object is one device in a Snapshot.
type Object struct {
	Path   string   `json:"path"` // stable while the device exists: the id commands name
	Type   string   `json:"type"` // the expose type: "light", "switch", "dimmer", "cover", "scene", ...
	Name   string   `json:"name"`
	Area   string   `json:"area,omitempty"`
	Floor  string   `json:"floor,omitempty"`
	Access string   `json:"access"`          // AccessFull, AccessReadonly, AccessConfirm or AccessPin
	Roles  []string `json:"roles,omitempty"` // the home's roles that reach it; none: every member
	// Pulse: a switch that only pulses (a gate, an intercom's door release): vCLU turns on,
	// open and close into one pulse, and off does nothing
	Pulse bool `json:"pulse,omitempty"`
}

// Object.Access. The cloud reads anything else as AccessReadonly.
const (
	AccessFull     = "full"
	AccessReadonly = "readonly" // seen, never commanded
	AccessConfirm  = "confirm"  // a command needs Cmd.Confirmed: the person said yes
	AccessPin      = "pin"      // a command needs the device's PIN, which only vCLU knows
)

// Limits of a Snapshot.
const (
	MaxObjects        = 500
	MaxSnapshotParts  = 16
	SnapshotPartBytes = 48 << 10 // vCLU splits by encoded size, below MaxMessageSize
)

// Cmd asks vCLU to act on a device for a person. vCLU checks :remote(), the person's roles,
// readonly, confirmation and the PIN, then calls the device's own method; it answers with
// CmdResult at most once per ID, and the cloud never sends an ID again.
type Cmd struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	Action string `json:"action"` // ActionOn, ...
	// Value is ActionSet's level, 0..100: a dimmer's brightness, a cover's position (100 open).
	Value     *float64 `json:"value,omitempty"`
	Actor     Actor    `json:"actor"`
	Confirmed bool     `json:"confirmed,omitempty"`
	Pin       string   `json:"pin,omitempty"` // as the person typed it; never stored or logged
	// TimeoutMs is how long the cloud waits, from when vCLU reads the command: a command still
	// queued after it is not run. Relative, so vCLU's clock does not matter.
	TimeoutMs int64 `json:"timeout_ms"`
}

// Cmd.Action, and the devices they fit.
const (
	ActionOn      = "on"      // switch, light, dimmer
	ActionOff     = "off"     // switch, light, dimmer
	ActionOpen    = "open"    // cover
	ActionClose   = "close"   // cover
	ActionStop    = "stop"    // cover
	ActionExecute = "execute" // scene
	ActionSet     = "set"     // dimmer (brightness), cover (position): with Cmd.Value
)

type CmdResult struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`  // with !OK
	Message string `json:"message,omitempty"` // shown to the person (Polish), either way
}

// States asks vCLU what some of the devices of the snapshot are doing now, for a person: vCLU
// answers for those exposed with :remote() whose roles reach actor.roles, and leaves the rest
// out. Read only; nothing runs.
type States struct {
	Type  string   `json:"type"`
	ID    string   `json:"id"`
	Paths []string `json:"paths"` // at most MaxStatePaths
	Actor Actor    `json:"actor"`
}

// MaxStatePaths bounds one States request, so its answer fits a control message.
const MaxStatePaths = 100

type StatesResult struct {
	Type   string  `json:"type"`
	ID     string  `json:"id"`
	States []State `json:"states,omitempty"`
	Reason string  `json:"reason,omitempty"` // the whole request refused: unknown_actor, rate_limited, invalid
}

// State is one device's state, by its type; a field it does not know is left out.
type State struct {
	Path string `json:"path"`
	// On: a light, switch, dimmer or fan is on; a binary_sensor is open, a motion sensor sees movement.
	On *bool `json:"on,omitempty"`
	// Position: a cover's, 0..100, 100 open.
	Position *float64 `json:"position,omitempty"`
	// Value: a reading, temperature in °C, humidity in %, illuminance in lx.
	Value *float64 `json:"value,omitempty"`
}

// Reasons for a refused request (cmd_result, notify_action_result).
const (
	ReasonForbidden    = "forbidden"
	ReasonNotFound     = "not_found" // e.g. the notification's buttons are gone after a Lua reload
	ReasonExpired      = "expired"   // the deadline passed
	ReasonError        = "error"
	ReasonInvalid      = "invalid"       // the action does not fit the device
	ReasonConfirm      = "confirm"       // the device needs Cmd.Confirmed
	ReasonPinRequired  = "pin_required"  // the device needs its PIN
	ReasonPinWrong     = "pin_wrong"     // the PIN did not match
	ReasonRateLimited  = "rate_limited"  // too many commands, or wrong PINs
	ReasonUnknownActor = "unknown_actor" // the person is not in vCLU's members
)

// Codes in Error.
const (
	CodeRateLimited = "rate_limited"
	CodeInvalid     = "invalid"
)

type Error struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// MaxMessageSize bounds one control message.
const MaxMessageSize = 64 << 10

var ErrTooLarge = errors.New("proto: message too large")

// Message is a decoded control message: its type and the raw JSON, to be
// unmarshalled into the struct for that type.
type Message struct {
	Type string
	Raw  json.RawMessage
}

// Decode unmarshals the message into v (e.g. *Hello).
func (m Message) Decode(v any) error { return json.Unmarshal(m.Raw, v) }

// Reader reads newline-delimited JSON messages.
type Reader struct{ br *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{br: bufio.NewReaderSize(r, 4096)} }

// Read returns the next message. Unknown types are returned as well: callers
// ignore and log them, so both sides can add messages without a version bump.
func (r *Reader) Read() (Message, error) {
	var line []byte
	for {
		chunk, err := r.br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > MaxMessageSize {
			return Message{}, ErrTooLarge
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				return Message{}, io.ErrUnexpectedEOF
			}
			return Message{}, err
		}
		break
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return Message{}, fmt.Errorf("proto: bad message: %w", err)
	}
	if head.Type == "" {
		return Message{}, errors.New("proto: message without type")
	}
	return Message{Type: head.Type, Raw: json.RawMessage(line)}, nil
}

// Write encodes v (a message struct with its Type set) as one line.
func Write(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) >= MaxMessageSize {
		return ErrTooLarge
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// MetricsLimits are what a home may send (section 10, "Statistics"). Anything over them is
// refused in MetricsAck, never stored.
type MetricsLimits struct {
	Series  int   `json:"series"`          // series the home may keep; a new one over it is refused
	BucketS int   `json:"bucket_s"`        // MetricsBucketSeconds
	MaxAgeS int64 `json:"max_age_s"`       // a bucket older than this is refused
	Query   bool  `json:"query,omitempty"` // the cloud answers MetricsQuery; vCLU sends none otherwise
}

// Metrics carries one-minute aggregates. vCLU sends a series only when Lua registers it with
// cloud=true, in batches every few minutes, the backlog after an outage included. The cloud
// stores them for the connection's home: the message names no home. Sending a bucket again
// replaces it.
type Metrics struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"` // unique per vCLU; MetricsAck names it
	Series []MetricSeries `json:"series"`
}

type MetricSeries struct {
	Path string `json:"path"` // as in metric(); 1..MaxMetricPath of MetricPathChars
	Unit string `json:"unit,omitempty"`
	Kind string `json:"kind,omitempty"` // MetricGauge (the default) or MetricCounter
	// Points are [bucket start in unix seconds, a multiple of MetricsBucketSeconds, avg, min, max,
	// samples], oldest first.
	Points [][5]float64 `json:"points"`
}

const (
	MetricGauge   = "gauge"   // a level: temperature, power; charts draw avg, min and max
	MetricCounter = "counter" // a meter reading that only grows: energy; bars draw its increase
)

// Limits of Metrics.
const (
	MetricsBucketSeconds = 60
	MaxMetricsSeries     = 50   // series in one message
	MaxMetricsPoints     = 1000 // points in one message, all series together
	MaxMetricPath        = 64
	MaxMetricUnit        = 16
	MetricPathChars      = "a-z A-Z 0-9 _ - . : /"
)

// MetricsAck answers Metrics. With Error set nothing was stored: vCLU keeps the batch and sends
// it again after RetryAfterS (MetricsRateLimited, MetricsBusy) or stops (MetricsOff). Otherwise
// every point not listed in Rejected is stored and vCLU may forget the batch.
type MetricsAck struct {
	Type        string           `json:"type"`
	ID          string           `json:"id"`
	Stored      int              `json:"stored"`
	Rejected    []MetricRejected `json:"rejected,omitempty"`
	Error       string           `json:"error,omitempty"`
	RetryAfterS int              `json:"retry_after_s,omitempty"`
}

// MetricRejected is a series refused whole (MetricOverLimit, MetricInvalid) or some of its
// points (MetricBadPoints, with Points their number).
type MetricRejected struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Points int    `json:"points,omitempty"`
}

// MetricsAck.Error.
const (
	MetricsOff         = "off"          // the home's plan has no statistics: send none until the next welcome enables them
	MetricsRateLimited = "rate_limited" // too many points too fast
	MetricsBusy        = "busy"         // the previous batch is still being stored
	MetricsInvalid     = "invalid"      // over MaxMetricsSeries or MaxMetricsPoints
)

// MetricsQuery asks for the connection's home's stored buckets of Path from Since to Until
// (unix seconds, Until not included), merged into steps of StepS: a multiple of
// MetricsBucketSeconds up to MaxMetricsStepS. The cloud may answer with a larger step: hours once
// the range reaches past the minutes it keeps, and as many as keep the answer within
// MaxMetricsQueryPoints. vCLU sends one at a time and waits for its MetricsPoints.
type MetricsQuery struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Path  string `json:"path"`
	Since int64  `json:"since"`
	Until int64  `json:"until"`
	StepS int    `json:"step_s"`
}

// MetricsPoints answers MetricsQuery. Points are [step start, avg (weighted by samples), min,
// max, samples], oldest first; a step with nothing stored is left out. With Error set there are
// none: MetricsOff, MetricsRateLimited or MetricsBusy (ask again after RetryAfterS),
// MetricsInvalid.
type MetricsPoints struct {
	Type        string       `json:"type"`
	ID          string       `json:"id"`
	StepS       int          `json:"step_s,omitempty"`
	Points      [][5]float64 `json:"points"`
	Error       string       `json:"error,omitempty"`
	RetryAfterS int          `json:"retry_after_s,omitempty"`
}

// Limits of MetricsQuery.
const (
	MaxMetricsQueryPoints = 2000
	MaxMetricsStepS       = 86400
)

// MetricRejected.Reason.
const (
	MetricOverLimit = "limit"   // a new series over MetricsLimits.Series
	MetricInvalid   = "invalid" // the path, unit or kind is not allowed
	MetricBadPoints = "points"  // off the bucket grid, too old, in the future or not finite
)
