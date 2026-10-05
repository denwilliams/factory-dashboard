package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config is loaded from <home>/config.json. Any field left out keeps its default.
type Config struct {
	Port int    `json:"port"`
	Bind string `json:"bind"`

	SyncInterval    string `json:"sync_interval"`
	SummaryInterval string `json:"summary_interval"`
	LookbackDays    int    `json:"lookback_days"`

	GitHubEnabled bool     `json:"github_enabled"`
	GitHubOwners  []string `json:"github_owners"` // "my-org" or "user:someone"; empty = discover from `gh`
	AgentLogins   []string `json:"agent_logins"`  // extra logins to treat as coding agents

	JiraEnabled bool   `json:"jira_enabled"`
	JiraJQL     string `json:"jira_jql"` // empty = everything updated within lookback_days
	JiraFields  string `json:"jira_fields"`
	JiraLimit   int    `json:"jira_limit"`

	VercelEnabled bool     `json:"vercel_enabled"`
	VercelScopes  []string `json:"vercel_scopes"` // empty = the CLI's current scope

	SummaryEnabled bool     `json:"summary_enabled"`
	PiArgs         []string `json:"pi_args"`
	PiProvider     string   `json:"pi_provider"`
	PiModel        string   `json:"pi_model"`

	MemoryLimitMB int      `json:"memory_limit_mb"`
	ExtraPath     []string `json:"extra_path"`

	syncEvery    time.Duration
	summaryEvery time.Duration
}

func defaultConfig() Config {
	return Config{
		Port:            7420,
		Bind:            "127.0.0.1",
		SyncInterval:    "15m",
		SummaryInterval: "6h",
		LookbackDays:    14,
		GitHubEnabled:   true,
		GitHubOwners:    []string{},
		AgentLogins:     []string{},
		JiraEnabled:     true,
		JiraFields:      "key,summary,status,assignee,reporter,issuetype,priority,created,updated,resolutiondate,project",
		JiraLimit:       300,
		VercelEnabled:   true,
		VercelScopes:    []string{},
		SummaryEnabled:  true,
		// --no-tools matters: PR titles are untrusted input, so pi must not be able to run commands.
		PiArgs:        []string{"-p", "--no-session", "--no-tools", "--no-context-files", "--no-extensions", "--no-skills"},
		MemoryLimitMB: 48,
		ExtraPath:     []string{},
	}
}

// homeDir is where config, data, logs and the pid file live.
func homeDir() string {
	if h := os.Getenv("FACTORY_DASHBOARD_HOME"); h != "" {
		return h
	}
	uh, err := os.UserHomeDir()
	if err != nil {
		return ".factory-dashboard"
	}
	return filepath.Join(uh, ".factory-dashboard")
}

func configPath() string { return filepath.Join(homeDir(), "config.json") }

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(configPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", configPath(), err)
		}
	}
	if cfg.syncEvery, err = time.ParseDuration(cfg.SyncInterval); err != nil || cfg.syncEvery < time.Minute {
		return cfg, fmt.Errorf("sync_interval %q must be a duration of at least 1m", cfg.SyncInterval)
	}
	if cfg.summaryEvery, err = time.ParseDuration(cfg.SummaryInterval); err != nil || cfg.summaryEvery < 5*time.Minute {
		return cfg, fmt.Errorf("summary_interval %q must be a duration of at least 5m", cfg.SummaryInterval)
	}
	if cfg.LookbackDays < 1 {
		cfg.LookbackDays = 14
	}
	if cfg.JiraLimit < 1 {
		cfg.JiraLimit = 300
	}
	return cfg, nil
}

func writeDefaultConfig(force bool) (string, error) {
	p := configPath()
	if _, err := os.Stat(p); err == nil && !force {
		return p, fmt.Errorf("%s already exists (use -force to overwrite)", p)
	}
	if err := os.MkdirAll(homeDir(), 0o755); err != nil {
		return p, err
	}
	b, _ := json.MarshalIndent(defaultConfig(), "", "  ")
	return p, os.WriteFile(p, append(b, '\n'), 0o644)
}

func (c Config) addr() string { return fmt.Sprintf("%s:%d", c.Bind, c.Port) }

func (c Config) url() string {
	host := c.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d/", host, c.Port)
}
