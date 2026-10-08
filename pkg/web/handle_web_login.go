package web

import (
	"net/http"

	"github.com/Domktorymysli/vclu/pkg/config"
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
	username := c.PostForm("username")
	password := c.PostForm("password")

	if config.ValidateAuth(s.serverDir, username, password) {
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

	// Invalid credentials
	trans := s.getTranslations(c)
	s.renderHTML(c, http.StatusOK, "login.html", gin.H{
		"title":     "Login",
		"error":     true,
		"lang":      trans["lang"],
		"t":         trans["t"],
		"langs":     trans["langs"],
		"csrfToken": s.getCSRFToken(c),
	})
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
