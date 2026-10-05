package api

import (
	"crypto/subtle"
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
			auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if c, err := r.Cookie("gpup_session"); err == nil && auth == "" {
				auth = c.Value
			}
			if subtle.ConstantTimeCompare([]byte(auth), []byte(opt.Token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			// An authenticated initial browser request establishes an HttpOnly same-origin SSE session.
			if r.Header.Get("Authorization") != "" {
				http.SetCookie(w, &http.Cookie{Name: "gpup_session", Value: opt.Token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
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
