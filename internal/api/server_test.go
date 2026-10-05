package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"gpup/internal/app"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestAPIValidatesMutationsAndServesSPARoutes(t *testing.T) {
	a, err := app.New(filepath.Join(t.TempDir(), "api.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var assets fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>GPUP</html>")}}
	h := Handler(a, assets, Options{})
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/v1/snapshot", "", 200}, {"GET", "/models", "", 200},
		{"POST", "/api/v1/targets", `{"name":"bad","engine":"openai","url":"file:///etc/passwd"}`, 400},
		{"POST", "/api/v1/targets", `{"name":"local","engine":"openai","url":"http://localhost:8000/v1"}`, 201},
		{"POST", "/api/v1/benchmarks", `{"targetId":"missing","model":"x","durationSeconds":1,"concurrency":[1],"maxTokens":1}`, 400},
		{"POST", "/api/v1/targets", `{"unexpected":1}`, 400},
		{"GET", "/api/v1/nope", "", 404},
	} {
		r := httptest.NewRequest(tc.method, "http://localhost"+tc.path, bytes.NewBufferString(tc.body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "http://localhost/api/v1/targets", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var rows []any
	if err = json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("targets %v %v", rows, err)
	}
}

func TestSSEDisconnectCancelsStream(t *testing.T) {
	a, err := app.New(filepath.Join(t.TempDir(), "api.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	Handler(a, fstest.MapFS{}, Options{}).ServeHTTP(w, r)
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %v", w.Header())
	}
}

func TestSSEDoesNotResendUnchangedSnapshot(t *testing.T) {
	a, err := app.New(filepath.Join(t.TempDir(), "api.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	server := httptest.NewServer(Handler(a, fstest.MapFS{}, Options{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/stream", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	events := 0
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: snapshot") {
			events++
			if events > 1 {
				t.Fatal("unchanged snapshot resent to idle browser")
			}
		}
	}
	if events != 1 {
		t.Fatalf("initial snapshot missing: %d", events)
	}
}

func TestStoppingAppClosesLiveStream(t *testing.T) {
	a, err := app.New(filepath.Join(t.TempDir(), "api.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	server := httptest.NewServer(Handler(a, fstest.MapFS{}, Options{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/stream", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() {
		t.Fatal("missing stream")
	}
	a.Stop()
	for scanner.Scan() {
	}
	if ctx.Err() != nil {
		t.Fatal("stream outlived application shutdown")
	}
}
