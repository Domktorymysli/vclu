package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
	"rsc.io/qr"
)

// The panel's page for the clients of the device API (clients.go): made, changed and deleted
// only with the panel's own session, never with the panel's API key, which could otherwise
// hand itself further access.

var errClientNotFound = &configError{http.StatusNotFound, "Client not found"}

// handleClientsPage renders the API clients page.
func (s *Server) handleClientsPage(c *gin.Context) {
	s.renderHTML(c, http.StatusOK, "clients.html", gin.H{
		"currentPage":     "clients",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            s.getLang(c),
		"t":               s.getTranslations(c)["t"],
	})
}

// clientsAdmin answers 403 to the panel's API key: clients are given access by a person.
func clientsAdmin(c *gin.Context) bool {
	if c.GetString("auth_method") == "apikey" {
		c.JSON(http.StatusForbidden, gin.H{"error": "API clients are managed from the panel only"})
		return false
	}
	return true
}

// clientsGranting answers 403 while the panel has no password: anyone on the network could open
// it and give themselves a client with any profiles and roles. Listing and deleting stay open.
func (s *Server) clientsGranting(c *gin.Context) bool {
	if !clientsAdmin(c) {
		return false
	}
	if !config.IsAuthEnabled(s.serverDir) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Set a panel password first", "code": "auth_required"})
		return false
	}
	return true
}

// ClientView is a client as the panel lists it: never its token or the token's hash.
type ClientView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Profiles  []string `json:"profiles"`
	Roles     []string `json:"roles"`
	ExpiresAt int64    `json:"expiresAt,omitempty"`
	Expired   bool     `json:"expired,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// ClientList is the answer of GET /api/clients (documentation only: the handler answers gin.H).
type ClientList struct {
	Clients     []ClientView `json:"clients"`
	Roles       []string     `json:"roles"`       // the roles to choose from: named in Lua and held by the cloud's members
	AuthEnabled bool         `json:"authEnabled"` // false: clients cannot be given (no panel password)
	LAN         *ClientLAN   `json:"lan,omitempty"`
}

// ClientLAN is where the LAN listener is (documentation only).
type ClientLAN struct {
	Addresses []string `json:"addresses" example:"192.168.0.50:9443"`
	Pin       string   `json:"pin" example:"sha256/4kTPH21ZodCHTvQdZWC4kbIQqq+DzebfNmx5OYMXxTI="`
}

// ClientToken is a client's token, shown once, with the connection behind its QR code
// (documentation only).
type ClientToken struct {
	Client     *ClientView `json:"client,omitempty"` // on create
	Token      string      `json:"token" example:"vclu_client_…"`
	Connection string      `json:"connection"` // JSON v1: {v, type "vclu-client", name, token, addresses, pin}
	QR         string      `json:"qr,omitempty" example:"data:image/png;base64,…"`
}

// handleAPIClients lists the clients, the roles to choose from and where the LAN listener is.
//
//	@Summary		API clients
//	@Description	The API clients, the roles to choose from and where the LAN listener is. Only the panel's session: the API key gets 403.
//	@Tags			clients
//	@Produce		json
//	@Success		200	{object}	web.ClientList
//	@Failure		403	{object}	web.APIError
//	@Router			/api/clients [get]
func (s *Server) handleAPIClients(c *gin.Context) {
	if !clientsAdmin(c) {
		return
	}
	mc, _ := config.ReadMasterConfig(s.serverDir)
	now := time.Now().Unix()
	list := []ClientView{}
	if mc != nil {
		for _, cl := range mc.Clients {
			list = append(list, ClientView{ID: cl.ID, Name: cl.Name, Profiles: nonNil(cl.Profiles), Roles: nonNil(cl.Roles),
				ExpiresAt: cl.ExpiresAt, Expired: cl.ExpiresAt != 0 && cl.ExpiresAt <= now, CreatedAt: cl.CreatedAt})
		}
	}
	resp := gin.H{"clients": list, "roles": s.knownRoles(), "authEnabled": config.IsAuthEnabled(s.serverDir)}
	if s.local != nil {
		resp["lan"] = gin.H{"addresses": s.local.addresses(s.local.port), "pin": s.local.Pin()}
	}
	c.JSON(http.StatusOK, resp)
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// knownRoles are the roles the remote dashboards name in Lua and the cloud's members hold.
func (s *Server) knownRoles() []string {
	var roles []string
	s.remoteRolesMu.Lock()
	for _, dr := range s.remoteDashboardsLocked() {
		roles = append(roles, dr...)
	}
	s.remoteRolesMu.Unlock()
	if s.local != nil {
		for _, m := range s.local.Members() {
			roles = append(roles, m.Roles...)
		}
	}
	slices.Sort(roles)
	return nonNil(slices.Compact(roles))
}

// ClientRequest makes or changes a client: all fields at once.
type ClientRequest struct {
	Name      string   `json:"name"`
	Profiles  []string `json:"profiles"`
	Roles     []string `json:"roles"`
	ExpiresAt int64    `json:"expiresAt"`
}

// check validates the request; "" when it is fine.
func (r *ClientRequest) check() string {
	r.Name = strings.TrimSpace(r.Name)
	switch {
	case r.Name == "" || len(r.Name) > 60:
		return "Name is required (at most 60 characters)"
	case r.Roles != nil && !validClientRoles(r.Roles):
		return "Roles: 1 to 32 of a-z, 0-9, - and _, at most 30"
	case r.ExpiresAt < 0:
		return "Invalid expiry"
	}
	return ""
}

// handleAPIClientCreate makes a client and answers its token, shown this once.
//
//	@Summary		Add an API client
//	@Description	Makes a client and answers its token, shown this once, with its connection as JSON and as a QR code. Needs a panel password (403 auth_required) and the panel's session (the API key gets 403).
//	@Tags			clients
//	@Accept			json
//	@Produce		json
//	@Param			request	body		web.ClientRequest	true	"The client"
//	@Success		200		{object}	web.ClientToken
//	@Failure		400		{object}	web.APIError
//	@Failure		403		{object}	web.APIError
//	@Router			/api/clients [post]
func (s *Server) handleAPIClientCreate(c *gin.Context) {
	if !s.clientsGranting(c) {
		return
	}
	var req ClientRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	if msg := req.check(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	id := make([]byte, 4)
	token, err := newClientToken()
	if err == nil {
		_, err = rand.Read(id)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate the token"})
		return
	}
	cl := config.APIClient{
		ID: hex.EncodeToString(id), Name: req.Name, TokenHash: config.HashAPIKey(token),
		Profiles: nonNil(req.Profiles), Roles: req.Roles, ExpiresAt: req.ExpiresAt,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		mc.Clients = append(mc.Clients, cl)
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}
	if s.local != nil && s.local.OnIssue != nil {
		s.local.OnIssue() // the LAN listener, if it has not started
	}
	resp := s.clientConnection(cl.Name, token)
	resp["client"] = ClientView{ID: cl.ID, Name: cl.Name, Profiles: cl.Profiles,
		Roles: nonNil(cl.Roles), ExpiresAt: cl.ExpiresAt, CreatedAt: cl.CreatedAt}
	c.JSON(http.StatusOK, resp)
}

// clientConnection is what a client needs to reach this vCLU, shown once with its token: as
// JSON to paste ("connection") and as a QR code of the same JSON for an app to scan ("qr", a PNG
// data URL). Version 1: {v, type "vclu-client", name, token, addresses (host:port of the LAN
// listener, best first), pin (sha256/ of its certificate's public key)}; no addresses or pin
// when the LAN listener is off.
func (s *Server) clientConnection(name, token string) gin.H {
	conn := map[string]any{"v": 1, "type": "vclu-client", "name": name, "token": token}
	if s.local != nil {
		conn["addresses"] = s.local.addresses(s.local.port)
		conn["pin"] = s.local.Pin()
	}
	b, _ := json.Marshal(conn)
	resp := gin.H{"token": token, "connection": string(b)}
	if code, err := qr.Encode(string(b), qr.M); err == nil {
		code.Scale = 6
		resp["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
	}
	return resp
}

// handleAPIClientUpdate changes a client's name, profiles, roles and expiry (all of them).
//
//	@Summary		Change an API client
//	@Description	Sets the client's name, profiles, roles and expiry, all of them; its token stays. Needs a panel password and the panel's session.
//	@Tags			clients
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"Client id"
//	@Param			request	body		web.ClientRequest	true	"The client"
//	@Success		200		{object}	web.APIOK
//	@Failure		400		{object}	web.APIError
//	@Failure		403		{object}	web.APIError
//	@Failure		404		{object}	web.APIError
//	@Router			/api/clients/{id} [put]
func (s *Server) handleAPIClientUpdate(c *gin.Context) {
	if !s.clientsGranting(c) {
		return
	}
	var req ClientRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	if msg := req.check(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	s.updateClient(c, func(cl *config.APIClient) {
		cl.Name, cl.Profiles, cl.Roles, cl.ExpiresAt = req.Name, nonNil(req.Profiles), req.Roles, req.ExpiresAt
	}, gin.H{"success": true})
}

// handleAPIClientRegenerate gives a client a new token; the old one stops at once.
//
//	@Summary		New token for an API client
//	@Description	Gives the client a new token, shown this once with its connection and QR code; the old token stops at once, also on open connections. Needs a panel password and the panel's session.
//	@Tags			clients
//	@Produce		json
//	@Param			id	path		string	true	"Client id"
//	@Success		200	{object}	web.ClientToken
//	@Failure		403	{object}	web.APIError
//	@Failure		404	{object}	web.APIError
//	@Router			/api/clients/{id}/regenerate [post]
func (s *Server) handleAPIClientRegenerate(c *gin.Context) {
	if !s.clientsGranting(c) {
		return
	}
	token, err := newClientToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate the token"})
		return
	}
	resp := gin.H{} // filled in the change: the answer needs the client's name
	s.updateClient(c, func(cl *config.APIClient) {
		cl.TokenHash = config.HashAPIKey(token)
		for k, v := range s.clientConnection(cl.Name, token) {
			resp[k] = v
		}
	}, resp)
}

func (s *Server) updateClient(c *gin.Context, change func(*config.APIClient), ok gin.H) {
	id := c.Param("id")
	err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		for i := range mc.Clients {
			if mc.Clients[i].ID == id {
				change(&mc.Clients[i])
				return nil
			}
		}
		return errClientNotFound
	})
	if ce, isCE := asConfigError(err); isCE {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}
	c.JSON(http.StatusOK, ok)
}

// handleAPIClientDelete deletes a client; its token stops at once.
//
//	@Summary		Delete an API client
//	@Description	Deletes the client; its token stops at once, also on open connections. Works without a panel password too, but never with the API key.
//	@Tags			clients
//	@Produce		json
//	@Param			id	path		string	true	"Client id"
//	@Success		200	{object}	web.APIOK
//	@Failure		403	{object}	web.APIError
//	@Failure		404	{object}	web.APIError
//	@Router			/api/clients/{id} [delete]
func (s *Server) handleAPIClientDelete(c *gin.Context) {
	if !clientsAdmin(c) {
		return
	}
	id := c.Param("id")
	err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		n := len(mc.Clients)
		mc.Clients = slices.DeleteFunc(mc.Clients, func(cl config.APIClient) bool { return cl.ID == id })
		if len(mc.Clients) == n {
			return errClientNotFound
		}
		return nil
	})
	if ce, isCE := asConfigError(err); isCE {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
