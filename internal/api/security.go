package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type Options struct {
	Token        string
	ReadOnly     bool
	TLSCert      string
	TLSKey       string
	PublicStatic bool
}

const sessionCookie = "gpup_session"

// sessionValue derives the browser session cookie from the API token.
func sessionValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("gpup browser session v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func ValidateBind(addr, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if token == "" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("non-loopback binding requires GPUP_API_TOKEN or --token")
	}
	return nil
}

func Secure(opt Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		if opt.Token == "" && !opt.PublicStatic {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			host = strings.Trim(host, "[]")
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				http.Error(w, "tokenless API requires a loopback Host", http.StatusForbidden)
				return
			}
		}
		if opt.Token != "" {
			session := sessionValue(opt.Token)
			header := r.Header.Get("Authorization")
			ok := header != "" && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(opt.Token)) == 1
			if c, err := r.Cookie(sessionCookie); err == nil && header == "" {
				ok = subtle.ConstantTimeCompare([]byte(c.Value), []byte(session)) == 1
			}
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// An authenticated initial browser request establishes an HttpOnly
			// same-origin SSE session. The cookie holds a value derived from the
			// token, so it cannot be replayed as a bearer token.
			if header != "" {
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: session, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if opt.ReadOnly {
				http.Error(w, "read-only mode", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				if err != nil || u.Host != r.Host || u.Scheme != scheme {
					http.Error(w, "foreign origin rejected", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
