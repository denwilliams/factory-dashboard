package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type JiraIssue struct {
	Key            string `json:"key"`
	Summary        string `json:"summary"`
	Status         string `json:"status"`
	StatusCategory string `json:"status_category"` // new | indeterminate | done
	Type           string `json:"type,omitempty"`
	Priority       string `json:"priority,omitempty"`
	Assignee       string `json:"assignee,omitempty"`
	Reporter       string `json:"reporter,omitempty"`
	Project        string `json:"project,omitempty"`
	Created        string `json:"created,omitempty"`
	Updated        string `json:"updated,omitempty"`
	Resolved       string `json:"resolved,omitempty"`
	URL            string `json:"url,omitempty"`
}

type JiraData struct {
	SyncedAt time.Time   `json:"synced_at"`
	JQL      string      `json:"jql"`
	Issues   []JiraIssue `json:"issues"`
}

type acliIssue struct {
	Key    string `json:"key"`
	Self   string `json:"self"`
	Fields struct {
		Summary string `json:"summary"`
		Status  *struct {
			Name           string `json:"name"`
			StatusCategory *struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		IssueType *struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority *struct {
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
		Reporter *struct {
			DisplayName string `json:"displayName"`
		} `json:"reporter"`
		Project *struct {
			Key string `json:"key"`
		} `json:"project"`
		Created        string `json:"created"`
		Updated        string `json:"updated"`
		ResolutionDate string `json:"resolutiondate"`
	} `json:"fields"`
}

func jiraJQL(cfg Config) string {
	if cfg.JiraJQL != "" {
		return cfg.JiraJQL
	}
	return fmt.Sprintf("updated >= -%dd ORDER BY updated DESC", cfg.LookbackDays)
}

func syncJira(ctx context.Context, cfg Config) (int, error) {
	jql := jiraJQL(cfg)
	out, err := runCLI(ctx, 3*time.Minute, nil, "acli", "jira", "workitem", "search",
		"--jql", jql, "--fields", cfg.JiraFields, "--limit", strconv.Itoa(cfg.JiraLimit), "--json")
	if err != nil {
		return 0, err
	}
	issues, err := parseAcliIssues(out)
	if err != nil {
		return 0, err
	}
	return len(issues), writeJSON(fileJira, JiraData{SyncedAt: time.Now().UTC(), JQL: jql, Issues: issues})
}

// parseAcliIssues accepts either a bare array or a REST-style {"issues": [...]} envelope.
func parseAcliIssues(out []byte) ([]JiraIssue, error) {
	out = jsonStart(out)
	var raw []acliIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		var env struct {
			Issues []acliIssue `json:"issues"`
		}
		if err2 := json.Unmarshal(out, &env); err2 != nil {
			return nil, fmt.Errorf("parse acli output: %w", err)
		}
		raw = env.Issues
	}
	issues := make([]JiraIssue, 0, len(raw))
	for _, r := range raw {
		f := r.Fields
		is := JiraIssue{Key: r.Key, Summary: f.Summary, Created: f.Created, Updated: f.Updated, Resolved: f.ResolutionDate}
		if i := strings.Index(r.Self, "/rest/"); i > 0 {
			is.URL = r.Self[:i] + "/browse/" + r.Key
		}
		if f.Status != nil {
			is.Status = f.Status.Name
			if f.Status.StatusCategory != nil {
				is.StatusCategory = f.Status.StatusCategory.Key
			}
		}
		if f.IssueType != nil {
			is.Type = f.IssueType.Name
		}
		if f.Priority != nil {
			is.Priority = f.Priority.Name
		}
		if f.Assignee != nil {
			is.Assignee = f.Assignee.DisplayName
		}
		if f.Reporter != nil {
			is.Reporter = f.Reporter.DisplayName
		}
		if f.Project != nil {
			is.Project = f.Project.Key
		}
		issues = append(issues, is)
	}
	return issues, nil
}
