package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityRequiresTokenAndRejectsForeignOrigin(t *testing.T) {
	h := Secure(Options{Token: "secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		method, token, origin string
		want                  int
	}{
		{"GET", "", "", 401}, {"GET", "Bearer wrong", "", 401}, {"GET", "Bearer secret", "", 204},
		{"POST", "Bearer secret", "http://evil.example", 403}, {"POST", "Bearer secret", "http://localhost:7331", 204},
	} {
		r := httptest.NewRequest(tc.method, "http://localhost:7331/api/v1/targets", nil)
		r.Header.Set("Authorization", tc.token)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%+v got %d", tc, w.Code)
		}
	}
}
func TestReadOnlyBlocksWrites(t *testing.T) {
	h := Secure(Options{ReadOnly: true}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "http://localhost/api/v1/benchmarks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("read-only write: %d", w.Code)
	}
}
func TestPublicBindRequiresToken(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7331", ":7331", "[::]:7331"} {
		if err := ValidateBind(addr, ""); err == nil {
			t.Errorf("accepted %s", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:7331", "localhost:7331", "[::1]:7331"} {
		if err := ValidateBind(addr, ""); err != nil {
			t.Error(err)
		}
	}
	if err := ValidateBind("0.0.0.0:7331", "secret"); err != nil {
		t.Error(err)
	}
}

func TestTokenlessAPIRejectsDNSRebindingHost(t *testing.T) {
	h := Secure(Options{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "http://attacker.example:7331/api/v1/targets", nil)
	r.Header.Set("Origin", "http://attacker.example:7331")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("rebinding request accepted: %d", w.Code)
	}
}

func TestSessionCookieIsDerivedAndNotTheToken(t *testing.T) {
	h := Secure(Options{Token: "secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("GET", "http://localhost:7331/api/v1/snapshot", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	if w.Code != 204 || len(cookies) != 1 || cookies[0].Value == "secret" || !cookies[0].HttpOnly {
		t.Fatalf("unexpected session cookie: %d %+v", w.Code, cookies)
	}
	for _, tc := range []struct {
		cookie, header string
		want           int
	}{
		{cookies[0].Value, "", 204},
		{"secret", "", 401},
		{"", "Bearer " + cookies[0].Value, 401},
		{cookies[0].Value, "Bearer wrong", 401},
	} {
		r := httptest.NewRequest("GET", "http://localhost:7331/api/v1/stream", nil)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "gpup_session", Value: tc.cookie})
		}
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("cookie %q header %q: got %d, want %d", tc.cookie, tc.header, w.Code, tc.want)
		}
	}
}
