package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/gin-gonic/gin"
)

// handleLogin shows the login page
func (s *Server) handleLogin(c *gin.Context) {
	// If auth is not enabled, redirect to status
	if !config.IsAuthEnabled(s.serverDir) {
		c.Redirect(http.StatusFound, "/stats")
		return
	}

	// If already logged in, redirect to status
	if s.isAuthenticated(c) {
		c.Redirect(http.StatusFound, "/stats")
		return
	}

	trans := s.getTranslations(c)
	s.renderHTML(c, http.StatusOK, "login.html", gin.H{
		"title":     "Login",
		"lang":      trans["lang"],
		"t":         trans["t"],
		"langs":     trans["langs"],
		"csrfToken": s.getCSRFToken(c),
	})
}

// handleLoginPost processes login form
func (s *Server) handleLoginPost(c *gin.Context) {
	if crossSite(c) {
		c.String(http.StatusForbidden, "Login from another site refused")
		return
	}
	ip := c.ClientIP()
	// the try is reserved before the password is checked: posts at the same time never get
	// more tries than are left
	if wait := s.loginLocks.begin(ip); wait > 0 {
		s.renderLogin(c, http.StatusTooManyRequests, gin.H{"locked": true, "lockedFor": lockText(wait), "lockedSeconds": seconds(wait)})
		return
	}

	username := c.PostForm("username")
	password := c.PostForm("password")

	if config.ValidateAuth(s.serverDir, username, password) {
		s.loginLocks.result(ip, true)
		// Create session
		sessionToken := s.sessions.create()
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     sessionCookieName,
			Value:    sessionToken,
			MaxAge:   sessionMaxAge,
			Path:     "/",
			HttpOnly: true,
			Secure:   isHTTPS(c),
			SameSite: http.SameSiteStrictMode,
		})
		c.Redirect(http.StatusFound, "/stats")
		return
	}

	// Invalid credentials: the address only, people type passwords into the user field
	s.notifier.Add(notify.Entry{
		Kind: notify.KindEvent, Source: notify.SourceLogin, Level: notify.LevelWarning,
		Key: "login:" + ip, Msg: notify.MsgLogin, Params: map[string]string{"ip": ip}, IP: ip, Link: "/settings",
	})
	if wait := s.loginLocks.result(ip, false); wait > 0 {
		s.notifyLock(notify.MsgLockLogin, "login:"+ip, map[string]string{"ip": ip}, wait, ip)
		s.renderLogin(c, http.StatusTooManyRequests, gin.H{"locked": true, "lockedFor": lockText(wait), "lockedSeconds": seconds(wait)})
		return
	}
	s.renderLogin(c, http.StatusOK, gin.H{"error": true})
}

func (s *Server) renderLogin(c *gin.Context, code int, data gin.H) {
	trans := s.getTranslations(c)
	data["title"] = "Login"
	data["lang"] = trans["lang"]
	data["t"] = trans["t"]
	data["langs"] = trans["langs"]
	data["csrfToken"] = s.getCSRFToken(c)
	s.renderHTML(c, code, "login.html", data)
}

// crossSite is whether a login comes from another site's page. A browser that sends
// Sec-Fetch-Site decides alone (a proxy may rewrite Host): only same-origin and a typed
// address (none) pass. Without it, Origin must be this host.
func crossSite(c *gin.Context) bool {
	if site := c.GetHeader("Sec-Fetch-Site"); site != "" {
		return site != "same-origin" && site != "none"
	}
	origin := c.GetHeader("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err != nil || !strings.EqualFold(u.Host, c.Request.Host)
}

func seconds(d time.Duration) int { return int((d + time.Second - 1) / time.Second) }

// lockText is the wait on the login page: "30 s", "4 min".
func lockText(d time.Duration) string {
	if n := seconds(d); n < 60 {
		return fmt.Sprintf("%d s", n)
	}
	return fmt.Sprintf("%d min", int((d+time.Minute-1)/time.Minute))
}

// handleLogout logs out the user
func (s *Server) handleLogout(c *gin.Context) {
	sessionToken, err := c.Cookie(sessionCookieName)
	if err == nil {
		s.sessions.delete(sessionToken)
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(c),
		SameSite: http.SameSiteStrictMode,
	})
	c.Redirect(http.StatusFound, "/login")
}
