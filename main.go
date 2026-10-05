// Command factory-dashboard runs a small local daemon that syncs GitHub, Jira
// and Vercel activity through their CLIs and serves a dashboard over HTTP.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

const usage = `factory-dashboard — see what your org is delivering, humans and agents.

Usage:
  factory-dashboard [command]

Commands:
  start     Start the daemon in the background and open the dashboard (default)
  serve     Run in the foreground (use this from launchd/systemd)
  stop      Stop the background daemon
  restart   Stop then start
  status    Show whether the daemon is running and the last sync results
  open      Open the dashboard in your browser
  sync      Ask the running daemon to sync now (or run one sync if it isn't running)
  init      Write a default config to %s
  version   Print the version

Flags for start/serve/open: -port N, -no-open
Environment: FACTORY_DASHBOARD_HOME (default ~/.factory-dashboard)
`

func main() {
	log.SetFlags(log.LstdFlags)
	cmd := "start"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fl := flag.NewFlagSet(cmd, flag.ExitOnError)
	port := fl.Int("port", 0, "override the configured port")
	noOpen := fl.Bool("no-open", false, "don't open the browser")
	force := fl.Bool("force", false, "overwrite existing config (init)")
	fl.Usage = func() { fmt.Fprintf(os.Stderr, usage, configPath()) }
	_ = fl.Parse(args)

	if cmd == "init" {
		p, err := writeDefaultConfig(*force)
		if err != nil {
			fatal(err)
		}
		fmt.Println("wrote", p)
		return
	}
	if cmd == "version" {
		fmt.Println(version)
		return
	}
	if cmd == "help" || cmd == "-h" {
		fl.Usage()
		return
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal(err)
	}
	if *port > 0 {
		cfg.Port = *port
	}
	extendPath(cfg.ExtraPath)

	switch cmd {
	case "serve":
		serve(cfg)
	case "start":
		start(cfg, !*noOpen)
	case "stop":
		if err := stop(); err != nil {
			fatal(err)
		}
	case "restart":
		_ = stop()
		start(cfg, false)
	case "status":
		printStatus(cfg)
	case "open":
		openBrowser(cfg.url())
	case "sync":
		syncNow(cfg)
	default:
		fl.Usage()
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "factory-dashboard:", err)
	os.Exit(1)
}

func pidPath() string { return filepath.Join(homeDir(), "factory-dashboard.pid") }
func logPath() string { return filepath.Join(homeDir(), "factory-dashboard.log") }

// tuneMemory keeps the idle heap small: a soft memory limit plus a more
// aggressive GC target. GOMEMLIMIT/GOGC in the environment win.
func tuneMemory(cfg Config) {
	if os.Getenv("GOMEMLIMIT") == "" && cfg.MemoryLimitMB > 0 {
		debug.SetMemoryLimit(int64(cfg.MemoryLimitMB) << 20)
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
	}
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(2)
	}
}

func serve(cfg Config) {
	tuneMemory(cfg)
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		fatal(err)
	}
	ln, err := net.Listen("tcp", cfg.addr())
	if err != nil {
		fatal(fmt.Errorf("listen on %s: %w", cfg.addr(), err))
	}
	_ = os.WriteFile(pidPath(), []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidPath())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	status := loadStatus()
	sched := newScheduler(cfg, status)
	go sched.Run(ctx)

	srv := &http.Server{
		Handler:           newServer(cfg, status, sched),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("factory-dashboard %s listening on %s (data in %s)", version, cfg.url(), dataDir())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func health(cfg Config) (int, bool) {
	c := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(cfg.url() + "api/health")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var h struct {
		OK  bool `json:"ok"`
		PID int  `json:"pid"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil || !h.OK {
		return 0, false
	}
	return h.PID, true
}

func start(cfg Config, open bool) {
	if pid, ok := health(cfg); ok {
		fmt.Printf("already running (pid %d) at %s\n", pid, cfg.url())
		if open {
			openBrowser(cfg.url())
		}
		return
	}
	if err := os.MkdirAll(homeDir(), 0o755); err != nil {
		fatal(err)
	}
	if fi, err := os.Stat(logPath()); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(logPath(), logPath()+".1")
	}
	logf, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fatal(err)
	}
	defer logf.Close()
	exe, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	cmd := exec.Command(exe, "serve", "-port", strconv.Itoa(cfg.Port))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		fatal(err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	for i := 0; i < 50; i++ {
		if _, ok := health(cfg); ok {
			fmt.Printf("started (pid %d) at %s\nlogs: %s\n", pid, cfg.url(), logPath())
			if open {
				openBrowser(cfg.url())
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fatal(fmt.Errorf("daemon did not become healthy; see %s", logPath()))
}

func stop() error {
	b, err := os.ReadFile(pidPath())
	if err != nil {
		return errors.New("not running (no pid file)")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := terminate(p); err != nil {
		_ = os.Remove(pidPath())
		return fmt.Errorf("stop pid %d: %w", pid, err)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(pidPath()); errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Printf("stopped (pid %d)\n", pid)
	return nil
}

func printStatus(cfg Config) {
	pid, ok := health(cfg)
	if !ok {
		fmt.Println("not running")
	} else {
		fmt.Printf("running (pid %d) at %s\n", pid, cfg.url())
		fmt.Printf("memory: %s\n", processRSS(pid))
	}
	st := loadStatus()
	for _, name := range []string{"github", "jira", "vercel", "summary"} {
		s := st.Sources[name]
		switch {
		case s == nil:
			fmt.Printf("  %-8s never synced\n", name)
		case s.Skipped != "" && s.LastRun.IsZero():
			fmt.Printf("  %-8s skipped: %s\n", name, s.Skipped)
		case !s.OK:
			fmt.Printf("  %-8s FAILED %s ago: %s\n", name, since(s.LastRun), s.Error)
		default:
			fmt.Printf("  %-8s ok %s ago, %d items\n", name, since(s.LastOK), s.Count)
		}
	}
}

func since(t time.Time) string { return time.Since(t).Round(time.Second).String() }

func syncNow(cfg Config) {
	if _, ok := health(cfg); ok {
		req, _ := http.NewRequest(http.MethodPost, cfg.url()+"api/sync", nil)
		req.Header.Set("X-Factory-Dashboard", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fatal(err)
		}
		resp.Body.Close()
		fmt.Println("sync requested; run `factory-dashboard status` to see results")
		return
	}
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		fatal(err)
	}
	status := loadStatus()
	newScheduler(cfg, status).runOnce(context.Background(), syncRequest{})
	printStatus(cfg)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("open", url)
		return
	}
	go cmd.Wait()
}
