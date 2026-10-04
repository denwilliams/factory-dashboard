package main

import (
	"context"
	"log"
	"runtime"
	"runtime/debug"
	"time"
)

type syncRequest struct {
	forceSummary bool
	summaryOnly  bool
}

// Scheduler runs all syncs sequentially on one goroutine. Running the CLIs
// one at a time keeps peak memory (theirs and ours) low.
type Scheduler struct {
	cfg     Config
	status  *Status
	trigger chan syncRequest
}

func newScheduler(cfg Config, status *Status) *Scheduler {
	return &Scheduler{cfg: cfg, status: status, trigger: make(chan syncRequest, 1)}
}

// Trigger requests a sync; it never blocks and coalesces with a pending request.
func (s *Scheduler) Trigger(r syncRequest) bool {
	select {
	case s.trigger <- r:
		return true
	default:
		return false
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	// Start promptly if data is stale, otherwise wait out the rest of the interval.
	wait := 2 * time.Second
	if last := s.status.lastOK("github"); !last.IsZero() {
		if rem := s.cfg.syncEvery - time.Since(last); rem > wait {
			wait = rem
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	s.status.setSyncing(false, time.Now().Add(wait))
	for {
		var req syncRequest
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case req = <-s.trigger:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		s.runOnce(ctx, req)
		timer.Reset(s.cfg.syncEvery)
		s.status.setSyncing(false, time.Now().Add(s.cfg.syncEvery))
		// Hand memory used while parsing CLI output back to the OS before idling.
		runtime.GC()
		debug.FreeOSMemory()
	}
}

func (s *Scheduler) runOnce(ctx context.Context, req syncRequest) {
	s.status.setSyncing(true, time.Time{})
	type source struct {
		name    string
		enabled bool
		cli     string
		run     func(context.Context, Config) (int, error)
	}
	sources := []source{
		{"github", s.cfg.GitHubEnabled, "gh", syncGitHub},
		{"jira", s.cfg.JiraEnabled, "acli", syncJira},
		{"vercel", s.cfg.VercelEnabled, "vercel", syncVercel},
	}
	if !req.summaryOnly {
		for _, src := range sources {
			if ctx.Err() != nil {
				return
			}
			if !src.enabled {
				s.status.skip(src.name, "disabled in config")
				continue
			}
			if !haveCLI(src.cli) {
				s.status.skip(src.name, src.cli+" not found on PATH")
				continue
			}
			start := time.Now()
			n, err := src.run(ctx, s.cfg)
			s.status.record(src.name, start, n, err)
			if err != nil {
				log.Printf("sync %s failed: %v", src.name, err)
			} else {
				log.Printf("sync %s: %d items in %s", src.name, n, time.Since(start).Round(time.Millisecond))
			}
		}
	}
	s.maybeSummarize(ctx, req.forceSummary)
}

func (s *Scheduler) maybeSummarize(ctx context.Context, force bool) {
	if !s.cfg.SummaryEnabled {
		s.status.skip("summary", "disabled in config")
		return
	}
	if !haveCLI("pi") {
		s.status.skip("summary", "pi not found on PATH")
		return
	}
	if !force && time.Since(s.status.lastOK("summary")) < s.cfg.summaryEvery {
		return
	}
	start := time.Now()
	n, skipped, err := syncSummary(ctx, s.cfg, force)
	if skipped != "" {
		s.status.record("summary", start, 0, nil)
		s.status.skip("summary", skipped)
		return
	}
	s.status.record("summary", start, n, err)
	if err != nil {
		log.Printf("summary failed: %v", err)
	}
}
