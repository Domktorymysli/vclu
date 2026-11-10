package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/gin-gonic/gin"
)

// The vCLU app at home reaches this vCLU directly, not through the cloud (vclu-cloud
// docs/protocol.md, section 6a, "The mobile app at home"):
//
//   - The app asks the cloud, the cloud asks here through the tunnel (proto.PathLocalToken, as the
//     person, with their installation), and this vCLU issues a token for that person and app.
//   - The app sends it on the LAN listener (LocalHandler, HTTPS with a certificate made here,
//     pinned by the app from the grant). There it gets what it would get through the cloud: the
//     remote dashboards, as the person's role.
//   - A token works only while its person and installation are in the cloud's last members list,
//     with the role from there: signing the phone out or removing the member in the cloud ends
//     it with the next list. Tokens expire after LocalTokenTTL and are renewed through the cloud.

const LocalTokenTTL = 30 * 24 * time.Hour

var localInstallRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// LocalAccess keeps the local tokens and the members list, in localStateFile next to the
// certificate.
type LocalAccess struct {
	dir  string
	port int
	pin  string
	now  func() time.Time
	// addresses lists where the LAN listener is reached, best first.
	addresses func(port int) []string
	// OnIssue is called before each token is issued: the LAN listener starts then if it
	// has not yet.
	OnIssue func()

	mu      sync.Mutex
	tokens  map[string]localTokenRec // by hex SHA-256 of the token
	members proto.Members
	// cloudSeen is when the cloud was last heard from (unix seconds): a members list, or a
	// health report sent on a working connection. Tokens stop after cloudSilence without it.
	cloudSeen int64
}

// cloudSilence is how long local tokens keep working without the cloud: the members list, and the
// roles in it, are the cloud's, and a home cut off from it must not honour them for ever.
const cloudSilence = 7 * 24 * time.Hour

type localTokenRec struct {
	Actor   string `json:"actor"`
	Install string `json:"install"`
	Expires int64  `json:"expires"`
}

type localState struct {
	Tokens    map[string]localTokenRec `json:"tokens"`
	Members   proto.Members            `json:"members"`
	CloudSeen int64                    `json:"cloudSeen,omitempty"`
}

const (
	localStateFile = "cloud-local.json"
	localCertFile  = "cloud-local.crt"
	localKeyFile   = "cloud-local.key"
)

// NewLocalAccess loads the tokens and members kept in dir and the LAN certificate, making one
// the first time, and returns the TLS config for the listener on port.
func NewLocalAccess(dir string, port int) (*LocalAccess, *tls.Config, error) {
	cert, err := localCert(dir)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(cert.Leaf.RawSubjectPublicKeyInfo)
	a := &LocalAccess{
		dir: dir, port: port, now: time.Now, addresses: lanAddresses,
		pin:    "sha256/" + base64.StdEncoding.EncodeToString(sum[:]),
		tokens: map[string]localTokenRec{},
	}
	if b, err := os.ReadFile(filepath.Join(dir, localStateFile)); err == nil {
		var st localState
		if json.Unmarshal(b, &st) == nil {
			if st.Tokens != nil {
				a.tokens = st.Tokens
			}
			a.members = st.Members
			a.cloudSeen = st.CloudSeen
		}
	}
	if a.cloudSeen == 0 {
		a.cloudSeen = a.now().Unix() // a state from before cloudSeen: count from now
	}
	return a, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// SetMembers takes the cloud's members list, ignoring one older than the last, and forgets the
// tokens it no longer lists.
func (a *LocalAccess) SetMembers(m proto.Members) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m.Version < a.members.Version {
		return
	}
	a.members = m
	a.cloudSeen = a.now().Unix()
	for h, t := range a.tokens {
		if _, _, ok := a.listed(t); !ok {
			delete(a.tokens, h)
		}
	}
	a.save()
}

// Revoke ends local access with the pairing it rested on (cloud.Options.Forgotten): every
// token and the members list go, and the next pairing's first list is taken whatever its
// version.
func (a *LocalAccess) Revoke() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = map[string]localTokenRec{}
	a.members = proto.Members{}
	a.save()
}

// CloudSeen notes that the cloud connection works (vCLU just sent it a health report). Saved at
// most hourly: it only has to be right to within a fraction of cloudSilence.
func (a *LocalAccess) CloudSeen() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now().Unix()
	if now-a.cloudSeen >= 3600 {
		a.cloudSeen = now
		a.save()
	}
}

// Members returns the cloud's last members list, for the cloud page.
func (a *LocalAccess) Members() []proto.Member {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.members.Members)
}

// MemberName is the name of actor ("cloud:<id>") in the cloud's members list, "" when it does
// not name them.
func (a *LocalAccess) MemberName(actor string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range a.members.Members {
		if m.User == actor {
			return m.Name
		}
	}
	return ""
}

// MemberRoles are the home's own roles of actor in the cloud's members list; known is false
// while no list came, and then nothing is known about anyone.
func (a *LocalAccess) MemberRoles(actor string) (roles []string, member, known bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.members.Version == 0 {
		return nil, false, false
	}
	for _, m := range a.members.Members {
		if m.User == actor {
			return slices.Clone(m.Roles), true, true
		}
	}
	return nil, false, true
}

// listed returns the role and the home's own roles of t's person while the members list names
// them and t's app. mu held.
func (a *LocalAccess) listed(t localTokenRec) (string, []string, bool) {
	for _, m := range a.members.Members {
		if m.User == t.Actor && slices.ContainsFunc(m.Apps, func(app proto.App) bool { return app.Install == t.Install }) {
			return m.Role, m.Roles, remoteRoles[m.Role]
		}
	}
	return "", nil, false
}

// Issue makes a token for actor's app install, replacing that install's earlier one.
func (a *LocalAccess) Issue(actor, install string) (proto.LocalGrant, error) {
	if !remoteActorRe.MatchString(actor) || !localInstallRe.MatchString(install) {
		return proto.LocalGrant{}, errors.New("local: bad actor or installation")
	}
	if a.OnIssue != nil {
		a.OnIssue()
	}
	b := make([]byte, 32)
	rand.Read(b)
	token := "vclu_local_" + base64.RawURLEncoding.EncodeToString(b)
	exp := a.now().Add(LocalTokenTTL)
	a.mu.Lock()
	for h, t := range a.tokens {
		if t.Install == install || t.Expires <= a.now().Unix() {
			delete(a.tokens, h)
		}
	}
	a.tokens[hashLocal(token)] = localTokenRec{Actor: actor, Install: install, Expires: exp.Unix()}
	a.save()
	a.mu.Unlock()
	return proto.LocalGrant{Token: token, ExpiresAt: exp.Unix(), Addresses: a.addresses(a.port), Pin: a.pin}, nil
}

// Viewer is who a token is for and with what role, false when it is unknown, expired or no
// longer in the members list.
func (a *LocalAccess) Viewer(token string) (actor, role string, roles []string, ok bool) {
	if !strings.HasPrefix(token, "vclu_local_") {
		return "", "", nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, found := a.tokens[hashLocal(token)]
	now := a.now().Unix()
	if !found || t.Expires <= now || now-a.cloudSeen > int64(cloudSilence/time.Second) {
		return "", "", nil, false
	}
	role, roles, ok = a.listed(t)
	return t.Actor, role, roles, ok
}

// Pin is the certificate pin the app checks, as in grants.
func (a *LocalAccess) Pin() string { return a.pin }

// save writes the state; mu held. A failed write leaves the tokens working until a restart.
func (a *LocalAccess) save() {
	b, _ := json.Marshal(localState{Tokens: a.tokens, Members: a.members, CloudSeen: a.cloudSeen})
	tmp := filepath.Join(a.dir, localStateFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("[Cloud] local access not saved: %v", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(a.dir, localStateFile)); err != nil {
		log.Printf("[Cloud] local access not saved: %v", err)
	}
}

func hashLocal(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// localCert loads the LAN listener's certificate from dir, or makes a self-signed one: the app
// pins it, so no name or CA matters, and it outlives the hardware.
func localCert(dir string) (tls.Certificate, error) {
	crt, key := filepath.Join(dir, localCertFile), filepath.Join(dir, localKeyFile)
	if c, err := tls.LoadX509KeyPair(crt, key); err == nil {
		return c, nil
	}
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "vCLU"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(30, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &pk.PublicKey, pk)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, err := x509.MarshalECPrivateKey(pk)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(crt, key)
}

// lanAddresses are this machine's private IPv4 addresses with port, home networks (192.168/16)
// first; containers' bridges and VPNs are left out where their names tell.
func lanAddresses(port int) []string {
	ifaces, _ := net.Interfaces()
	var ips []net.IP
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			if n, ok := ad.(*net.IPNet); ok {
				if ip := n.IP.To4(); ip != nil && ip.IsPrivate() {
					ips = append(ips, ip)
				}
			}
		}
	}
	sort.SliceStable(ips, func(i, j int) bool { return ips[i][0] == 192 && ips[j][0] != 192 })
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	}
	return out
}

func virtualIface(name string) bool {
	for _, p := range []string{"docker", "br-", "bridge", "veth", "virbr", "tun", "tap", "utun", "wg", "tailscale", "zt", "lxc", "cni", "flannel"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// SetLocalAccess turns on local tokens: the tunnel issues them and LocalHandler takes them.
func (s *Server) SetLocalAccess(a *LocalAccess) { s.local = a }

// handleLocalToken issues a local token for the person the cloud names (tunnel only).
func (s *Server) handleLocalToken(c *gin.Context) {
	if s.local == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	g, err := s.local.Issue(c.GetString(ctxRemoteActor), c.GetHeader(proto.HeaderInstall))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(g.Addresses) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no LAN address"})
		return
	}
	c.JSON(http.StatusOK, g)
}

// LocalHandler serves the app on the LAN, behind TLS: what the tunnel serves, as the person a
// local token names. Nothing without a valid token, not even a hint the path exists.
func (s *Server) LocalHandler() http.Handler {
	r := gin.New()
	if s.router != nil {
		r.HTMLRender = s.router.HTMLRender
	}
	r.UseRawPath, r.UnescapePathValues = true, true
	r.Use(gin.Recovery(), s.localViewer, staticCache)
	r.GET("/api/local/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "role": remoteRole(c)})
	})
	s.remoteRoutes(r)
	s.clientRoutes(r)
	return r
}

func (s *Server) localViewer(c *gin.Context) {
	token := bearer(c)
	if strings.HasPrefix(token, clientTokenPrefix) {
		s.lanClientViewer(c, token)
		return
	}
	if s.local == nil || token == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	actor, role, roles, ok := s.local.Viewer(token)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "local token not valid"})
		return
	}
	// a dashboard naming roles (:roles()) only for who has one, and one the cloud does not list
	// (its roles function hides it) for nobody: through the cloud the cloud checks this, on the
	// LAN it is not in the way
	if st := s.mayView(c.Request.URL.Path, roles, true); st != 0 {
		c.AbortWithStatusJSON(st, gin.H{"error": "Dashboard not found"})
		return
	}
	c.Set(ctxRemoteActor, actor)
	c.Set(ctxRemoteRole, role)
	c.Set(ctxViewerRoles, roles)
	c.Set(ctxRecheck, func(path string) int {
		_, _, roles, ok := s.local.Viewer(token)
		if !ok {
			return http.StatusUnauthorized
		}
		return s.mayView(path, roles, true)
	})
	c.Next()
}

// lanClientViewer is localViewer for a client of the device API (clients.go): the device API
// checks its token itself; the remote dashboards are its as a remote viewer, by its roles.
func (s *Server) lanClientViewer(c *gin.Context, token string) {
	if strings.HasPrefix(c.Request.URL.Path, "/api/v1/") {
		c.Next()
		return
	}
	cl, st := s.authClient(c, token)
	if st != 0 {
		c.AbortWithStatusJSON(st, gin.H{"error": "client token not valid"})
		return
	}
	actor, roles := clientActor(cl), cl.Roles
	if st := s.clientMayView(c.Request.URL.Path, roles); st != 0 {
		c.AbortWithStatusJSON(st, gin.H{"error": "Dashboard not found"})
		return
	}
	c.Set(ctxRemoteActor, actor)
	c.Set(ctxRemoteRole, "member") // Lua only tells the house from a remote viewer (roleKey)
	c.Set(ctxViewerRoles, roles)
	c.Set(ctxRecheck, func(path string) int {
		_, roles, ok := s.lanClient(token)
		if !ok {
			return http.StatusUnauthorized
		}
		return s.clientMayView(path, roles)
	})
	c.Next()
}

// String describes the access for logs.
func (a *LocalAccess) String() string {
	return fmt.Sprintf("port %d, %s", a.port, strings.Join(a.addresses(a.port), " "))
}

// dashboardOfPath is the dashboard a path is about: /d/<id>... and /api/dashboard/<id>..., "" else.
func dashboardOfPath(p string) string {
	rest, ok := strings.CutPrefix(p, "/d/")
	if !ok {
		rest, ok = strings.CutPrefix(p, "/api/dashboard/")
	}
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	return id
}

// rolesAllow is the cloud's rule (vclu-cloud homes.Sees): a dashboard naming no roles is every
// member's, one naming some is for who has one of them.
func rolesAllow(dashboard, person []string) bool {
	if len(dashboard) == 0 {
		return true
	}
	for _, r := range person {
		if slices.Contains(dashboard, r) {
			return true
		}
	}
	return false
}

// remoteDashboardRoles are the roles a remote dashboard names in Lua (:roles()), from the list
// the cloud gets, and whether it is on that list (not when its roles function hides it); cached
// until Lua reloads (DashboardsReloaded) or for remoteRolesFor.
func (s *Server) remoteDashboardRoles(id string) ([]string, bool) {
	s.remoteRolesMu.Lock()
	defer s.remoteRolesMu.Unlock()
	r, ok := s.remoteDashboardsLocked()[id]
	return r, ok
}

// remoteDashboardsFor are the ids of the remote dashboards roles reach (nil roles: those naming
// none); named leaves out those naming no roles.
func (s *Server) remoteDashboardsFor(roles []string, named bool) []string {
	s.remoteRolesMu.Lock()
	defer s.remoteRolesMu.Unlock()
	ids := []string{}
	for id, dr := range s.remoteDashboardsLocked() {
		if named && len(dr) == 0 {
			continue
		}
		if rolesAllow(dr, roles) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// remoteDashboardsLocked is the cached list, read again when it is older than remoteRolesFor;
// remoteRolesMu held.
func (s *Server) remoteDashboardsLocked() map[string][]string {
	if s.remoteRolesCache == nil || time.Since(s.remoteRolesAt) > remoteRolesFor {
		s.remoteRolesCache, s.remoteRolesAt = map[string][]string{}, time.Now()
		if s.luaExecutor != nil {
			out, err := s.luaExecutor.Execute("Dashboard.API.remoteList()")
			var list []struct {
				ID    string   `json:"id"`
				Roles []string `json:"roles"`
			}
			if err == nil && json.Unmarshal([]byte(out), &list) == nil {
				for _, d := range list {
					s.remoteRolesCache[d.ID] = d.Roles
				}
			}
		}
	}
	return s.remoteRolesCache
}

// remoteRolesFor is how long the roles stay cached, as long as the cloud waits for a change.
const remoteRolesFor = 15 * time.Second
