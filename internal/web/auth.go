package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "jobapp_session"

type sessionPayload struct {
	Exp      int64  `json:"exp"`
	Instance string `json:"iid"`
}

func newSessionInstance() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) checkPassword(password string) bool {
	if s.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte(password)) == nil
}

func (s *Server) setSession(w http.ResponseWriter) error {
	payload := sessionPayload{
		Exp:      time.Now().Add(30 * 24 * time.Hour).Unix(),
		Instance: s.sessionInstance,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sig := sign(s.SessionSecret, raw)
	val := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(sig)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure left false: Tailscale/local HTTP is expected for this single-user tool.
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, sign(s.SessionSecret, raw)) {
		return false
	}
	var payload sessionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if payload.Instance == "" || payload.Instance != s.sessionInstance {
		return false
	}
	return time.Now().Unix() < payload.Exp
}

func sign(secret string, raw []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		loginURL := loginPathWithNext(authReturnPath(r))
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", loginURL)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, loginURL, http.StatusSeeOther)
	})
}

// authReturnPath is the path to resume after login. GETs use the requested URL;
// mutating/HTMX requests fall back to Referer when it is a same-origin path.
func authReturnPath(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return r.URL.RequestURI()
	}
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil {
			return u.RequestURI()
		}
	}
	return ""
}

func loginPathWithNext(next string) string {
	if safeNext := safeRedirect(next); safeNext != "" && safeNext != jobsDefaultListURL {
		return "/login?next=" + url.QueryEscape(safeNext)
	}
	return "/login"
}

// safeRedirect returns next when it is a same-origin relative path suitable for
// post-login redirect; otherwise it returns the default jobs list URL.
func safeRedirect(next string) string {
	if next == "" {
		return jobsDefaultListURL
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return jobsDefaultListURL
	}
	if strings.ContainsAny(next, ":\\ \t\r\n") {
		return jobsDefaultListURL
	}
	path := next
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if path == "/login" || path == "/logout" {
		return jobsDefaultListURL
	}
	return next
}
