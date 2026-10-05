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
