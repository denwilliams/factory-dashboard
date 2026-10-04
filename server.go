package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed web
var webFS embed.FS

func newServer(cfg Config, status *Status, sched *Scheduler) http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServerFS(static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, http.StatusOK, map[string]any{"ok": true, "version": version, "pid": os.Getpid()})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		snap := status.snapshot()
		writeJSONResponse(w, http.StatusOK, map[string]any{
			"syncing":       snap.Syncing,
			"next_sync":     snap.NextSync,
			"sources":       snap.Sources,
			"sync_interval": cfg.SyncInterval,
			"lookback_days": cfg.LookbackDays,
			"version":       version,
		})
	})
	// Synced data is streamed from disk rather than cached in memory.
	mux.HandleFunc("GET /api/data/{name}", func(w http.ResponseWriter, r *http.Request) {
		file, ok := dataFiles[r.PathValue("name")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		p := filepath.Join(dataDir(), file)
		if _, err := os.Stat(p); err != nil {
			writeJSONResponse(w, http.StatusOK, map[string]any{})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, p)
	})
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		queued := sched.Trigger(syncRequest{})
		writeJSONResponse(w, http.StatusAccepted, map[string]any{"queued": queued})
	})
	mux.HandleFunc("POST /api/summarize", func(w http.ResponseWriter, r *http.Request) {
		queued := sched.Trigger(syncRequest{forceSummary: true, summaryOnly: true})
		writeJSONResponse(w, http.StatusAccepted, map[string]any{"queued": queued})
	})
	return guard(cfg, mux)
}

// guard blocks DNS-rebinding (unexpected Host headers) and cross-site POSTs
// (a custom header forces a CORS preflight, which we never grant).
func guard(cfg Config, next http.Handler) http.Handler {
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if cfg.Bind != "" && cfg.Bind != "0.0.0.0" && cfg.Bind != "::" {
		allowed[cfg.Bind] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if !allowed[host] && !(cfg.Bind == "0.0.0.0" || cfg.Bind == "::") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-Factory-Dashboard") != "1" {
			http.Error(w, "missing X-Factory-Dashboard header", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSONResponse(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
