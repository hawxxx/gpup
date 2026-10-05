package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gpup/internal/analysis"
	"gpup/internal/app"
	"gpup/internal/bench"
	"gpup/internal/engine"
	"gpup/internal/storage"
)

func Handler(a *app.App, assets fs.FS, opt Options) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"status": "ok", "version": app.Version})
	})
	r.Get("/metrics", Secure(opt, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := a.Snapshot()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# HELP gpup_targets Configured inference targets.\n# TYPE gpup_targets gauge\ngpup_targets %d\n# TYPE gpup_observed_requests gauge\ngpup_observed_requests %d\n", len(s.Targets), len(s.Requests))
		for _, g := range s.GPUs {
			if g.Utilization != nil {
				fmt.Fprintf(w, "gpup_gpu_utilization_percent{gpu=%q} %g\n", g.UUID, *g.Utilization)
			}
			if g.PowerW != nil {
				fmt.Fprintf(w, "gpup_gpu_power_watts{gpu=%q} %g\n", g.UUID, *g.PowerW)
			}
		}
	})).ServeHTTP)
	streams := make(chan struct{}, 16)
	r.Route("/api/v1", func(api chi.Router) {
		api.Use(func(next http.Handler) http.Handler { return Secure(opt, next) })
		api.Get("/snapshot", func(w http.ResponseWriter, r *http.Request) {
			s := a.Snapshot()
			s.System["readOnly"] = opt.ReadOnly
			s.System["authRequired"] = opt.Token != ""
			write(w, 200, s)
		})
		api.Get("/targets", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().Targets) })
		api.Post("/targets", func(w http.ResponseWriter, r *http.Request) {
			var t engine.Target
			if !decode(w, r, &t) {
				return
			}
			v, err := a.AddTarget(r.Context(), t)
			if err != nil {
				fail(w, 400, err)
				return
			}
			write(w, 201, v)
		})
		api.Delete("/targets/{id}", func(w http.ResponseWriter, r *http.Request) {
			if err := a.DeleteTarget(chi.URLParam(r, "id")); err != nil {
				fail(w, 400, err)
				return
			}
			w.WriteHeader(204)
		})
		api.Get("/models", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().Models) })
		api.Get("/gpus", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().GPUs) })
		api.Get("/requests", func(w http.ResponseWriter, r *http.Request) {
			s := a.Snapshot()
			limit := 200
			if raw := r.URL.Query().Get("limit"); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil || n < 1 || n > 2000 {
					fail(w, 400, errors.New("limit must be 1..2000"))
					return
				}
				limit = n
			}
			rows := s.Requests
			if len(rows) > limit {
				rows = rows[len(rows)-limit:]
			}
			write(w, 200, rows)
		})
		api.Get("/metrics", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().Metrics) })
		api.Get("/events", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().Findings) })
		api.Get("/benchmarks", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().Runs) })
		api.Post("/benchmarks", func(w http.ResponseWriter, r *http.Request) {
			var c bench.Config
			if !decode(w, r, &c) {
				return
			}
			id, err := a.StartBenchmark(r.Context(), c)
			if err != nil {
				fail(w, 400, err)
				return
			}
			write(w, 202, map[string]string{"id": id})
		})
		api.Get("/benchmarks/{id}", func(w http.ResponseWriter, r *http.Request) {
			j, err := a.Job(chi.URLParam(r, "id"))
			if err != nil {
				fail(w, 404, err)
				return
			}
			write(w, 200, j)
		})
		api.Delete("/benchmarks/{id}", func(w http.ResponseWriter, r *http.Request) {
			if err := a.CancelJob(chi.URLParam(r, "id")); err != nil {
				fail(w, 404, err)
				return
			}
			w.WriteHeader(204)
		})
		api.Get("/experiments", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().Runs) })
		api.Get("/compare", func(w http.ResponseWriter, r *http.Request) {
			base, err := a.Run(r.URL.Query().Get("baseline"))
			if err != nil {
				fail(w, 404, err)
				return
			}
			candidate, err := a.Run(r.URL.Query().Get("candidate"))
			if err != nil {
				fail(w, 404, err)
				return
			}
			result, err := analysis.Compare(base, candidate)
			if err != nil {
				fail(w, 400, err)
				return
			}
			write(w, 200, result)
		})
		api.Post("/discover", func(w http.ResponseWriter, r *http.Request) {
			v, err := a.Discover(r.Context())
			if err != nil {
				fail(w, 400, err)
				return
			}
			write(w, 200, v)
		})
		api.Get("/system", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Snapshot().System) })
		api.Get("/stream", func(w http.ResponseWriter, r *http.Request) {
			select {
			case streams <- struct{}{}:
				defer func() { <-streams }()
			default:
				fail(w, 503, errors.New("live stream capacity reached"))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			rc := http.NewResponseController(w)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			lastRevision := ^uint64(0)
			lastWrite := time.Now()
			for {
				if r.Context().Err() != nil {
					return
				}
				select {
				case <-a.Done():
					return
				default:
				}
				revision := a.Revision()
				if revision != lastRevision {
					s := a.Snapshot()
					s.System["readOnly"] = opt.ReadOnly
					s.System["authRequired"] = opt.Token != ""
					b, err := json.Marshal(s)
					if err != nil {
						return
					}
					_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if _, err = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b); err != nil {
						return
					}
					if err = rc.Flush(); err != nil {
						return
					}
					lastRevision = revision
					lastWrite = time.Now()
				} else if time.Since(lastWrite) >= 15*time.Second {
					_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
						return
					}
					if err := rc.Flush(); err != nil {
						return
					}
					lastWrite = time.Now()
				}
				select {
				case <-r.Context().Done():
					return
				case <-a.Done():
					return
				case <-ticker.C:
				}
			}
		})
		api.NotFound(func(w http.ResponseWriter, r *http.Request) { fail(w, 404, storage.ErrNotFound) })
	})
	files := http.FileServer(http.FS(assets))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if strings.HasPrefix(path, "api/") {
			http.NotFound(w, r)
			return
		}
		if info, err := fs.Stat(assets, path); err == nil && !info.IsDir() {
			if strings.HasPrefix(path, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if strings.Contains(path, ".") {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			fail(w, 503, errors.New("embedded frontend missing; run make build"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
	// Security headers apply to public static content; API authentication happens inside its router.
	return Secure(Options{PublicStatic: true}, r)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, err error) {
	write(w, status, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, fmt.Errorf("invalid JSON: %w", err))
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, errors.New("one JSON object required"))
		return false
	}
	return true
}
