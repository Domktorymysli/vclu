package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Domktorymysli/vclu/pkg/cloud/proto"
	"html/template"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/cloud"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/i18n"
)

// SetCloudClient wires the vclu cloud connection (nil hides the page).
func (s *Server) SetCloudClient(c *cloud.Client) {
	s.cloudClient = c
	if c != nil {
		s.metricsHistory = c
	}
}

// metricsHistorySource is cloud.Client.MetricsHistory; tests replace it.
type metricsHistorySource interface {
	MetricsHistory(ctx context.Context, path string, since, until int64, step int) ([][5]float64, int64, error)
}

func (s *Server) handleCloud(c *gin.Context) {
	trans := s.getTranslations(c)
	lang, _ := trans["lang"].(string)
	i18nJSON, err := json.Marshal(i18n.GetSection(lang, "cloud"))
	if err != nil {
		i18nJSON = []byte("{}")
	}
	var st cloud.Status
	if s.cloudClient != nil {
		st = s.cloudClient.Status()
	}
	statusJSON, _ := json.Marshal(st)
	// the home's people as the cloud last sent them, and where to manage them
	var members []proto.Member
	if s.local != nil && st.HomeID != 0 {
		members = s.local.Members()
	}
	phones := 0 // app installations with a LAN token, for the status card
	for _, m := range members {
		phones += len(m.Apps)
	}
	homeURL, manage := "", ""
	if st.HomeID != 0 && st.URL != "" {
		homeURL = strings.TrimSuffix(st.URL, "/") + "/app/homes/" + strconv.FormatInt(st.HomeID, 10)
		manage = homeURL + "/members"
	}
	var remote, devs string
	if s.luaExecutor != nil {
		remote, _ = s.luaExecutor.Execute("Dashboard.API.remoteList()")
		devs, _ = s.luaExecutor.Execute("JSON.encode(ExposedObjects:remoteList())")
	}
	shared, devices := sharedDashboards(remote, members), sharedDevices(devs, members)
	var link *cloudLinkView
	var mst *cloudMetricsView
	if s.cloudClient != nil && st.HomeID != 0 {
		link = newCloudLinkView(s.cloudClient.Link(), time.Now())
		mst = newCloudMetricsView(s.cloudClient.Metrics(), time.Now())
	}
	s.renderHTML(c, http.StatusOK, "cloud.html", gin.H{
		"cloudShared":  shared,
		"cloudDevices": devices,
		"cloudRoles":   homeRoles(members, shared, devices),
		"cloudActs":    s.cloudActivity(c, devices),
		"cloudLink":    link,
		"cloudMetrics": mst,
		"cloudMembers": members,
		"cloudManage":  manage,
		"cloudHomeURL": homeURL,
		"cloudPhones":  phones,
		"cloudAuth":    config.IsAuthEnabled(s.serverDir), // pairing needs the panel password
		"title":        "Cloud",
		"currentPage":  "cloud",
		"configured":   s.isConfigured,
		"available":    s.cloudClient != nil,
		"cloudI18n":    template.JS(i18nJSON),
		"cloudStatus":  template.JS(statusJSON),
		"lang":         trans["lang"],
		"t":            trans["t"],
	})
}

// cloudReady answers 503 when the cloud client is not wired.
func (s *Server) cloudReady(c *gin.Context) bool {
	if s.cloudClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cloud client not available"})
		return false
	}
	return true
}

// cloudAdmin allows pairing and unpairing only to the web session (or local
// web with auth disabled), never to an API key: whoever enters the code
// attaches this house to their cloud account.
func (s *Server) cloudAdmin(c *gin.Context) bool {
	if !s.cloudReady(c) {
		return false
	}
	if c.GetString("auth_method") == "session" || !config.IsAuthEnabled(s.serverDir) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "Cloud pairing needs a web session"})
	return false
}

// cloudPairing allows pairing only to a session of a panel that has a password: without one,
// anyone on the home network could attach this house to their own cloud account.
func (s *Server) cloudPairing(c *gin.Context) bool {
	if !s.cloudReady(c) {
		return false
	}
	if !config.IsAuthEnabled(s.serverDir) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Set a panel password first", "code": "auth_required"})
		return false
	}
	if c.GetString("auth_method") != "session" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cloud pairing needs a web session"})
		return false
	}
	return true
}

func (s *Server) handleAPICloudStatus(c *gin.Context) {
	if !s.cloudReady(c) {
		return
	}
	c.JSON(http.StatusOK, statusFor(s.cloudClient.Status(), c.GetString("auth_method") == "apikey"))
}

// statusFor is the cloud's status as the requester may see it: the pairing code attaches this
// house to whoever enters it first, so only a person (cloudAdmin) sees it, never the API key.
func statusFor(st cloud.Status, apiKey bool) cloud.Status {
	if st.Pairing != nil && apiKey {
		p := *st.Pairing
		p.UserCode, p.VerifyURLFull = "", ""
		st.Pairing = &p
	}
	return st
}

func (s *Server) handleAPICloudPair(c *gin.Context) {
	if !s.cloudPairing(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	st, err := s.cloudClient.Pair(ctx)
	if errors.Is(err, cloud.ErrPaired) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "status": st})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "status": st})
		return
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) handleAPICloudPairCancel(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	s.cloudClient.CancelPairing()
	c.JSON(http.StatusOK, s.cloudClient.Status())
}

func (s *Server) handleAPICloudUnpair(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := s.cloudClient.Unpair(ctx); err != nil {
		// The token is gone either way; the warning says the cloud was not told.
		c.JSON(http.StatusOK, gin.H{"warning": err.Error(), "status": s.cloudClient.Status()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": s.cloudClient.Status()})
}

// handleAPICloudEnabled switches the connection off (?on=0) or on (?on=1), keeping the pairing.
func (s *Server) handleAPICloudEnabled(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	if err := s.cloudClient.SetEnabled(c.Query("on") == "1"); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "status": s.cloudClient.Status()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": s.cloudClient.Status()})
}

func (s *Server) handleAPICloudReconnect(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	if err := s.cloudClient.Reconnect(); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.cloudClient.Status())
}

// sharedDashboard is a dashboard Lua shares through the cloud (:remote()), as the cloud page
// lists it: who sees it, and which of its roles no member has (most likely a typo in Lua).
type sharedDashboard struct {
	ID, Name, Icon string
	Roles          []string // empty: every member
	Nobody         []string // roles that no member has
	Pin            bool
}

// sharedDashboards reads Dashboard.API.remoteList()'s JSON; members are the cloud's last list
// (nil before the first, then no role is flagged).
func sharedDashboards(remoteJSON string, members []proto.Member) []sharedDashboard {
	var list []struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Icon  string   `json:"icon"`
		Roles []string `json:"roles"`
		Pin   bool     `json:"pin"`
	}
	if remoteJSON == "" || json.Unmarshal([]byte(remoteJSON), &list) != nil {
		return nil
	}
	out := make([]sharedDashboard, 0, len(list))
	for _, d := range list {
		sd := sharedDashboard{ID: d.ID, Name: d.Name, Icon: d.Icon, Roles: d.Roles, Pin: d.Pin}
		if sd.Name == "" {
			sd.Name = d.ID
		}
		sd.Nobody = rolesNobodyHas(d.Roles, members)
		out = append(out, sd)
	}
	return out
}

// sharedDevice is a device Lua exposes with :remote(), as the cloud page lists it: who
// commands it and how (proto.Object.Access: full, confirm, pin or readonly).
type sharedDevice struct {
	Path, Name, Area, Type, Access string
	Roles                          []string // empty: every member
	Nobody                         []string // roles that no member has
}

// sharedDevices reads ExposedObjects:remoteList()'s JSON, the list the cloud's snapshot gets,
// sorted by area and name.
func sharedDevices(listJSON string, members []proto.Member) []sharedDevice {
	var list []proto.Object
	if listJSON == "" || json.Unmarshal([]byte(listJSON), &list) != nil {
		return nil
	}
	out := make([]sharedDevice, 0, len(list))
	for _, o := range list {
		name := o.Name
		if name == "" {
			name = o.Path
		}
		out = append(out, sharedDevice{Path: o.Path, Name: name, Area: o.Area, Type: o.Type, Access: string(o.Access),
			Roles: o.Roles, Nobody: rolesNobodyHas(o.Roles, members)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// rolesNobodyHas lists the roles no member holds (most likely a typo in Lua); nothing before the
// cloud sent its first list of members.
func rolesNobodyHas(roles []string, members []proto.Member) []string {
	if members == nil {
		return nil
	}
	held := map[string]bool{}
	for _, m := range members {
		for _, r := range m.Roles {
			held[r] = true
		}
	}
	var out []string
	for _, r := range roles {
		if !held[r] {
			out = append(out, r)
		}
	}
	return out
}

// homeRole is one of the home's own roles as the cloud page explains it: who has it and what
// Lua opens with it.
type homeRole struct {
	Name       string
	People     []string // members with the role, by name
	Dashboards []string // shared dashboards limited to it
	Devices    int      // shared devices limited to it
}

// homeRoles lists the roles the members have and the roles Lua names, sorted by name; a role
// Lua names that nobody has comes with no People.
func homeRoles(members []proto.Member, shared []sharedDashboard, devices []sharedDevice) []homeRole {
	byName := map[string]*homeRole{}
	get := func(name string) *homeRole {
		if r, ok := byName[name]; ok {
			return r
		}
		r := &homeRole{Name: name}
		byName[name] = r
		return r
	}
	for _, m := range members {
		who := m.Name
		if who == "" {
			who = m.User
		}
		for _, r := range m.Roles {
			get(r).People = append(get(r).People, who)
		}
	}
	for _, d := range shared {
		for _, r := range d.Roles {
			get(r).Dashboards = append(get(r).Dashboards, d.Name)
		}
	}
	for _, d := range devices {
		for _, r := range d.Roles {
			get(r).Devices++
		}
	}
	out := make([]homeRole, 0, len(byName))
	for _, r := range byName {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// cloudActivityLimit is how many of the latest changes through the cloud the page lists.
const cloudActivityLimit = 30

// cloudAct is one change made through vclu cloud (origin "remote": the app, voice, a
// notification's button or a dashboard opened through the cloud) as the page lists it.
type cloudAct struct {
	At       time.Time
	Who      string
	What     string // the device's name and area, or its path
	Old, New string
}

// cloudActivity reads the latest changes with origin "remote" from the history.
func (s *Server) cloudActivity(c *gin.Context, devices []sharedDevice) []cloudAct {
	if s.historyStore == nil {
		return nil
	}
	res, err := s.historyStore.Search(history.SearchQuery{Origin: "remote", Limit: cloudActivityLimit, Ctx: c.Request.Context()})
	if err != nil {
		return nil
	}
	names := map[string]string{}
	for _, d := range devices {
		names[d.Path] = d.Name
		if d.Area != "" {
			names[d.Path] = d.Area + " → " + d.Name
		}
	}
	people := s.actorNames(res.Entries)
	out := make([]cloudAct, 0, len(res.Entries))
	for _, e := range res.Entries {
		a := cloudAct{At: time.UnixMilli(e.AtMs), Who: e.Actor, What: e.Path, Old: actValue(e.Old), New: actValue(e.Value)}
		if n := people[e.Actor]; n != "" {
			a.Who = n
		}
		if n := names[e.Path]; n != "" {
			a.What = n
		}
		out = append(out, a)
	}
	return out
}

func actValue(v any) string {
	switch v := v.(type) {
	case nil:
		return "—"
	case string:
		return v
	case bool, float64, int, int64:
		return fmt.Sprint(v)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// cloudLinkView is the connection card: the day's pings as an SVG line, how long the cloud was
// reachable, and the outages.
type cloudLinkView struct {
	Chart        template.HTML
	Availability string // "99.8" or "" with nothing to tell
	Last, Avg    string // ms of the last and the average ping, "" without one
	Max          string
	Outages      []linkOutage
	Since        time.Time
}

const linkChartW, linkChartH = 1440.0, 60.0

// linkGap breaks the line where pings are missing; they come once a minute.
const linkGap = 3 * time.Minute

func newCloudLinkView(r cloud.LinkReport, now time.Time) *cloudLinkView {
	v := &cloudLinkView{Since: r.Since}
	for _, o := range r.Outages {
		v.Outages = append(v.Outages, newLinkOutage(o, now))
	}
	if r.Availability >= 0 {
		v.Availability = strconv.FormatFloat(r.Availability*100, 'f', 1, 64)
	}
	span := now.Sub(r.Since).Seconds()
	if span <= 0 {
		span = 1
	}
	x := func(t time.Time) float64 { return t.Sub(r.Since).Seconds() / span * linkChartW }
	var top, sum float64
	var ok []cloud.LinkSample
	for _, p := range r.Samples {
		if p.OK {
			ok = append(ok, p)
			sum += p.RTT
			top = max(top, p.RTT)
		}
	}
	if len(ok) > 0 {
		ms := func(f float64) string { return strconv.FormatFloat(f, 'f', 0, 64) }
		v.Last, v.Avg, v.Max = ms(ok[len(ok)-1].RTT), ms(sum/float64(len(ok))), ms(top)
	}
	scale := max(top*1.5, 50) // the highest ping two thirds up; at least 50 ms, so a quiet line stays low
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="cloud-link-chart" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" role="img">`, linkChartW, linkChartH)
	for _, o := range r.Outages {
		to := o.To
		if to.IsZero() {
			to = now
		}
		from := o.From
		if from.Before(r.Since) {
			from = r.Since
		}
		fmt.Fprintf(&b, `<rect class="down" x="%.1f" y="0" width="%.1f" height="%.0f"/>`, x(from), max(x(to)-x(from), 2), linkChartH)
	}
	var pts []string
	flush := func() {
		if len(pts) > 0 {
			fmt.Fprintf(&b, `<polyline class="rtt" points="%s"/>`, strings.Join(pts, " "))
			pts = nil
		}
	}
	var prev time.Time
	for _, p := range r.Samples {
		if p.At.Sub(prev) > linkGap {
			flush() // no pings in between (vCLU or the connection was down): no line across
		}
		prev = p.At
		if !p.OK {
			flush()
			fmt.Fprintf(&b, `<line class="fail" x1="%.1f" x2="%.1f" y1="0" y2="%.0f"/>`, x(p.At), x(p.At), linkChartH)
			continue
		}
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(p.At), linkChartH-p.RTT/scale*linkChartH))
	}
	flush()
	b.WriteString(`</svg>`)
	v.Chart = template.HTML(b.String()) // numbers only, built here
	return v
}

// fmtFor writes a span as the page shows it: "<1 min", "12 min", "3 h 5 min", "2 d 4 h".
func fmtFor(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "<1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d d %d h", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// linkOutage is an outage as the page lists it: the reason as a known kind (Kind, translated)
// or as vCLU logged it (Raw).
type linkOutage struct {
	From, To   time.Time
	For        string // "12 min", "3 h 5 min"
	Kind, Raw  string
	StillGoing bool
}

func newLinkOutage(o cloud.Outage, now time.Time) linkOutage {
	l := linkOutage{From: o.From, To: o.To, Raw: o.Reason, StillGoing: o.To.IsZero()}
	to := o.To
	if l.StillGoing {
		to = now
	}
	l.For = fmtFor(to.Sub(o.From))
	switch {
	case strings.HasPrefix(o.Reason, "connection lost"):
		l.Kind = "lost"
	case strings.HasPrefix(o.Reason, "cloud closed the connection"):
		l.Kind = "closed"
	case strings.Contains(o.Reason, "no such host"), strings.Contains(o.Reason, "network is unreachable"),
		strings.Contains(o.Reason, "i/o timeout"), strings.Contains(o.Reason, "connection refused"):
		l.Kind = "network"
	}
	return l
}

// cloudMetricsView is the statistics tab: what goes to the cloud, how far each series is, and the
// points stored per hour as bars.
type cloudMetricsView struct {
	cloud.MetricsStatus
	Rows       []metricRow
	Day        int64 // points stored in the last 24 h
	Behind     int   // series still sending their backlog
	Chart      template.HTML
	ChartSince time.Time
}

// metricRow is one metric with cloud=true. State: "ok" (up to date), "behind" (sending the
// backlog), "waiting" (nothing in the cloud yet), "limit" or "invalid".
type metricRow struct {
	Path, Unit, Kind string
	State            string
	Until            time.Time // the newest minute in the cloud
	Behind           string    // how far back, while behind
	Points           int64     // stored since vCLU started
}

// metricBehind: a series whose newest minute in the cloud is older than this is still catching
// up. Some metrics sample every 10 minutes and batches go every 5.
const metricBehind = 20 * time.Minute

func newCloudMetricsView(st cloud.MetricsStatus, now time.Time) *cloudMetricsView {
	v := &cloudMetricsView{MetricsStatus: st}
	for _, m := range st.List {
		r := metricRow{Path: m.Path, Unit: m.Unit, Kind: m.Kind, Points: st.Points[m.Path]}
		if at, ok := st.Sent[m.Path]; ok {
			r.Until = time.Unix(at, 0)
		}
		switch {
		case slices.Contains(st.Invalid, m.Path):
			r.State = "invalid"
		case slices.Contains(st.OverLimit, m.Path):
			r.State = "limit"
		case r.Until.IsZero():
			r.State = "waiting"
		case now.Sub(r.Until) > metricBehind:
			r.State, r.Behind = "behind", fmtFor(now.Sub(r.Until))
			v.Behind++
		default:
			r.State = "ok"
		}
		v.Rows = append(v.Rows, r)
	}

	// 24 bars, one per hour, the last the hour now is in.
	first := now.Truncate(time.Hour).Add(-23 * time.Hour)
	v.ChartSince = first
	var bars [24]int64
	var top int64
	for _, h := range st.Hours {
		i := int((h.At - first.Unix()) / 3600)
		if i < 0 || i >= len(bars) {
			continue
		}
		bars[i] += h.Points
		v.Day += h.Points
		top = max(top, bars[i])
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="cloud-link-chart" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" role="img">`, linkChartW, linkChartH)
	w := linkChartW / float64(len(bars))
	for i, n := range bars {
		if n == 0 {
			continue
		}
		h := max(float64(n)/float64(top)*(linkChartH-4), 2)
		fmt.Fprintf(&b, `<rect class="bar" x="%.1f" y="%.1f" width="%.1f" height="%.1f"><title>%d</title></rect>`, float64(i)*w+2, linkChartH-h, w-4, h, n)
	}
	b.WriteString(`</svg>`)
	v.Chart = template.HTML(b.String()) // numbers only, built here
	return v
}
