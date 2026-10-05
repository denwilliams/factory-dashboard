package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

type Deployment struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Project   string `json:"project"`
	Scope     string `json:"scope,omitempty"`
	State     string `json:"state"` // READY | ERROR | BUILDING | QUEUED | CANCELED ...
	Target    string `json:"target"`
	CreatedAt int64  `json:"created_at"` // unix ms
	ReadyAt   int64  `json:"ready_at,omitempty"`
	Creator   string `json:"creator,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
	CommitMsg string `json:"commit_message,omitempty"`
}

type VercelData struct {
	SyncedAt    time.Time    `json:"synced_at"`
	Deployments []Deployment `json:"deployments"`
}

type vercelListOutput struct {
	ContextName string `json:"contextName"`
	Deployments []struct {
		ID         string                     `json:"id"`
		URL        string                     `json:"url"`
		Name       string                     `json:"name"`
		State      string                     `json:"state"`
		Target     *string                    `json:"target"`
		CreatedAt  int64                      `json:"createdAt"`
		BuildingAt int64                      `json:"buildingAt"`
		Ready      int64                      `json:"ready"`
		Creator    *struct{ Username string } `json:"creator"`
		Meta       map[string]any             `json:"meta"`
	} `json:"deployments"`
	Pagination *struct {
		Next *int64 `json:"next"`
	} `json:"pagination"`
}

func syncVercel(ctx context.Context, cfg Config) (int, error) {
	scopes := cfg.VercelScopes
	if len(scopes) == 0 {
		scopes = []string{""}
	}
	cutoff := time.Now().AddDate(0, 0, -cfg.LookbackDays).UnixMilli()
	data := VercelData{SyncedAt: time.Now().UTC(), Deployments: []Deployment{}}
	for _, scope := range scopes {
		deps, err := listVercelDeployments(ctx, scope, cutoff)
		if err != nil {
			return 0, err
		}
		data.Deployments = append(data.Deployments, deps...)
	}
	sort.Slice(data.Deployments, func(i, j int) bool { return data.Deployments[i].CreatedAt > data.Deployments[j].CreatedAt })
	return len(data.Deployments), writeJSON(fileVercel, data)
}

func listVercelDeployments(ctx context.Context, scope string, cutoff int64) ([]Deployment, error) {
	var deps []Deployment
	var next int64
	for page := 0; page < 20; page++ {
		// Runs from the data dir, which is never a linked project, so this lists every project in scope.
		args := []string{"list", "--format", "json", "--limit", "100", "--yes"}
		if scope != "" {
			args = append(args, "--scope", scope)
		}
		if next > 0 {
			args = append(args, "--next", strconv.FormatInt(next, 10))
		}
		out, err := runCLI(ctx, 2*time.Minute, nil, "vercel", args...)
		if err != nil {
			return nil, err
		}
		batch, nextPage, err := parseVercelList(out, scope)
		if err != nil {
			return nil, err
		}
		reachedCutoff := false
		for _, d := range batch {
			if d.CreatedAt < cutoff {
				reachedCutoff = true
				continue
			}
			deps = append(deps, d)
		}
		if reachedCutoff || nextPage == 0 || len(batch) == 0 {
			break
		}
		next = nextPage
	}
	return deps, nil
}

func parseVercelList(out []byte, scope string) ([]Deployment, int64, error) {
	var resp vercelListOutput
	if err := json.Unmarshal(jsonStart(out), &resp); err != nil {
		return nil, 0, fmt.Errorf("parse vercel output: %w", err)
	}
	if scope == "" {
		scope = resp.ContextName
	}
	deps := make([]Deployment, 0, len(resp.Deployments))
	for _, d := range resp.Deployments {
		dep := Deployment{
			ID: d.ID, URL: d.URL, Project: d.Name, Scope: scope, State: d.State,
			Target: "preview", CreatedAt: d.CreatedAt, ReadyAt: d.Ready,
		}
		if d.Target != nil && *d.Target != "" {
			dep.Target = *d.Target
		}
		if d.Creator != nil {
			dep.Creator = d.Creator.Username
		}
		meta := func(k string) string {
			if v, ok := d.Meta[k].(string); ok {
				return v
			}
			return ""
		}
		org, repo := firstNonEmpty(meta("githubOrg"), meta("githubCommitOrg")), firstNonEmpty(meta("githubRepo"), meta("githubCommitRepo"))
		if org != "" && repo != "" {
			dep.Repo = org + "/" + repo
		}
		dep.Branch = meta("githubCommitRef")
		dep.CommitSHA = meta("githubCommitSha")
		dep.CommitMsg = truncate(meta("githubCommitMessage"), 200)
		if dep.Creator == "" {
			dep.Creator = meta("githubCommitAuthorLogin")
		}
		deps = append(deps, dep)
	}
	var next int64
	if resp.Pagination != nil && resp.Pagination.Next != nil {
		next = *resp.Pagination.Next
	}
	return deps, next, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
