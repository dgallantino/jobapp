package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "jobapp_session"

type sessionPayload struct {
	Exp int64 `json:"exp"`
}

func (s *Server) checkPassword(password string) bool {
	if s.PasswordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(s.PasswordHash), []byte(password)) == nil
}

func (s *Server) setSession(w http.ResponseWriter) error {
	payload := sessionPayload{Exp: time.Now().Add(30 * 24 * time.Hour).Unix()}
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
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}
