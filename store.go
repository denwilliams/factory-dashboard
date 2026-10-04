package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Data lives on disk, not in memory: the server streams these files straight
// to the browser, so an idle process holds none of the synced data.
const (
	fileGitHub  = "github.json"
	fileJira    = "jira.json"
	fileVercel  = "vercel.json"
	fileSummary = "summary.json"
	fileStatus  = "status.json"
)

var dataFiles = map[string]string{
	"github":  fileGitHub,
	"jira":    fileJira,
	"vercel":  fileVercel,
	"summary": fileSummary,
}

func dataDir() string { return filepath.Join(homeDir(), "data") }

func writeJSON(name string, v any) error {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	if err := enc.Encode(v); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

func readJSON(name string, v any) error {
	f, err := os.Open(filepath.Join(dataDir(), name))
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

// SourceStatus records the outcome of the latest run for one source.
type SourceStatus struct {
	LastRun    time.Time `json:"last_run"`
	LastOK     time.Time `json:"last_ok"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Count      int       `json:"count"`
	Skipped    string    `json:"skipped,omitempty"`
}

type Status struct {
	mu       sync.Mutex
	Syncing  bool                     `json:"syncing"`
	Sources  map[string]*SourceStatus `json:"sources"`
	NextSync time.Time                `json:"next_sync"`
}

func loadStatus() *Status {
	s := &Status{Sources: map[string]*SourceStatus{}}
	_ = readJSON(fileStatus, s)
	if s.Sources == nil {
		s.Sources = map[string]*SourceStatus{}
	}
	s.Syncing = false
	return s
}

func (s *Status) record(source string, start time.Time, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.Sources[source]
	if st == nil {
		st = &SourceStatus{}
		s.Sources[source] = st
	}
	st.LastRun = start
	st.DurationMS = time.Since(start).Milliseconds()
	st.Skipped = ""
	st.OK = err == nil
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Error = ""
		st.LastOK = start
		st.Count = count
	}
	_ = writeJSON(fileStatus, s.snapshotLocked())
}

func (s *Status) skip(source, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.Sources[source]
	if st == nil {
		st = &SourceStatus{}
		s.Sources[source] = st
	}
	st.Skipped = reason
}

func (s *Status) setSyncing(v bool, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Syncing = v
	if !next.IsZero() {
		s.NextSync = next
	}
}

func (s *Status) lastOK(source string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st := s.Sources[source]; st != nil {
		return st.LastOK
	}
	return time.Time{}
}

type statusSnapshot struct {
	Syncing  bool                    `json:"syncing"`
	Sources  map[string]SourceStatus `json:"sources"`
	NextSync time.Time               `json:"next_sync"`
}

func (s *Status) snapshotLocked() statusSnapshot {
	out := statusSnapshot{Syncing: s.Syncing, NextSync: s.NextSync, Sources: make(map[string]SourceStatus, len(s.Sources))}
	for k, v := range s.Sources {
		out.Sources[k] = *v
	}
	return out
}

func (s *Status) snapshot() statusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}
