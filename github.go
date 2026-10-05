package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type PR struct {
	Repo       string   `json:"repo"`
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	State      string   `json:"state"` // OPEN | MERGED | CLOSED
	Draft      bool     `json:"draft"`
	Author     string   `json:"author"`
	AuthorKind string   `json:"author_kind"` // human | agent | bot
	Agent      string   `json:"agent,omitempty"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	MergedAt   string   `json:"merged_at,omitempty"`
	ClosedAt   string   `json:"closed_at,omitempty"`
	Additions  int      `json:"additions"`
	Deletions  int      `json:"deletions"`
	Branch     string   `json:"branch"`
	Review     string   `json:"review,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	JiraKeys   []string `json:"jira_keys,omitempty"`
}

type GitHubData struct {
	SyncedAt time.Time `json:"synced_at"`
	Owners   []string  `json:"owners"`
	Since    string    `json:"since"`
	PRs      []PR      `json:"prs"`
	Warnings []string  `json:"warnings,omitempty"`
}

const prSearchQuery = `query($q: String!, $cursor: String) {
  search(query: $q, type: ISSUE, first: 50, after: $cursor) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        number title url state isDraft createdAt updatedAt mergedAt closedAt
        additions deletions headRefName reviewDecision body
        author { login __typename }
        repository { nameWithOwner }
        labels(first: 10) { nodes { name } }
      }
    }
  }
}`

type ghSearchResponse struct {
	Data struct {
		Search struct {
			IssueCount int `json:"issueCount"`
			PageInfo   struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []ghPRNode `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

type ghPRNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	MergedAt       string `json:"mergedAt"`
	ClosedAt       string `json:"closedAt"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	HeadRefName    string `json:"headRefName"`
	ReviewDecision string `json:"reviewDecision"`
	Body           string `json:"body"`
	Author         *struct {
		Login    string `json:"login"`
		Typename string `json:"__typename"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

func syncGitHub(ctx context.Context, cfg Config) (int, error) {
	owners := cfg.GitHubOwners
	if len(owners) == 0 {
		var err error
		if owners, err = discoverGitHubOwners(ctx); err != nil {
			return 0, err
		}
	}
	since := time.Now().AddDate(0, 0, -cfg.LookbackDays).Format("2006-01-02")
	data := GitHubData{SyncedAt: time.Now().UTC(), Owners: owners, Since: since, PRs: []PR{}}
	seen := map[string]bool{}
	classifier := newAgentClassifier(cfg.AgentLogins)

	for _, owner := range owners {
		qual := ownerQualifier(owner)
		queries := []string{
			fmt.Sprintf("%s is:pr is:open archived:false", qual),
			fmt.Sprintf("%s is:pr is:merged merged:>=%s", qual, since),
			fmt.Sprintf("%s is:pr is:closed is:unmerged closed:>=%s", qual, since),
		}
		for _, q := range queries {
			nodes, total, err := searchPRs(ctx, q)
			if err != nil {
				return 0, err
			}
			if total > 1000 {
				data.Warnings = append(data.Warnings, fmt.Sprintf("%q matched %d PRs; GitHub search caps at 1000", q, total))
			}
			for _, n := range nodes {
				if n.URL == "" || seen[n.URL] {
					continue
				}
				seen[n.URL] = true
				data.PRs = append(data.PRs, toPR(n, classifier))
			}
		}
	}
	sort.Slice(data.PRs, func(i, j int) bool { return data.PRs[i].UpdatedAt > data.PRs[j].UpdatedAt })
	return len(data.PRs), writeJSON(fileGitHub, data)
}

func ownerQualifier(owner string) string {
	if strings.Contains(owner, ":") {
		return owner
	}
	return "org:" + owner
}

func discoverGitHubOwners(ctx context.Context) ([]string, error) {
	out, err := runCLI(ctx, time.Minute, nil, "gh", "api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var owners []string
	for _, l := range strings.Fields(string(out)) {
		owners = append(owners, l)
	}
	if len(owners) == 0 {
		login, err := runCLI(ctx, time.Minute, nil, "gh", "api", "user", "--jq", ".login")
		if err != nil {
			return nil, err
		}
		owners = []string{"user:" + strings.TrimSpace(string(login))}
	}
	return owners, nil
}

func searchPRs(ctx context.Context, q string) ([]ghPRNode, int, error) {
	var all []ghPRNode
	cursor := ""
	total := 0
	for page := 0; page < 20; page++ { // search API stops at 1000 results
		args := []string{"api", "graphql", "-f", "query=" + prSearchQuery, "-f", "q=" + q}
		if cursor != "" {
			args = append(args, "-f", "cursor="+cursor)
		}
		out, err := runCLI(ctx, 2*time.Minute, nil, "gh", args...)
		if err != nil {
			return nil, 0, err
		}
		var resp ghSearchResponse
		if err := json.Unmarshal(jsonStart(out), &resp); err != nil {
			return nil, 0, fmt.Errorf("parse gh graphql output: %w", err)
		}
		s := resp.Data.Search
		total = s.IssueCount
		for _, n := range s.Nodes {
			n.Body = trimBody(n.Body)
			all = append(all, n)
		}
		if !s.PageInfo.HasNextPage || s.PageInfo.EndCursor == "" {
			break
		}
		cursor = s.PageInfo.EndCursor
	}
	return all, total, nil
}

// trimBody keeps only what agent detection needs; trailers live at the end.
func trimBody(b string) string {
	if len(b) <= 4096 {
		return b
	}
	return b[:2048] + "\n" + b[len(b)-2048:]
}

var jiraKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-[1-9][0-9]{0,6}\b`)

func toPR(n ghPRNode, c *agentClassifier) PR {
	p := PR{
		Repo:      n.Repository.NameWithOwner,
		Number:    n.Number,
		Title:     n.Title,
		URL:       n.URL,
		State:     n.State,
		Draft:     n.IsDraft,
		CreatedAt: n.CreatedAt,
		UpdatedAt: n.UpdatedAt,
		MergedAt:  n.MergedAt,
		ClosedAt:  n.ClosedAt,
		Additions: n.Additions,
		Deletions: n.Deletions,
		Branch:    n.HeadRefName,
		Review:    n.ReviewDecision,
	}
	typename := ""
	if n.Author != nil {
		p.Author, typename = n.Author.Login, n.Author.Typename
	} else {
		p.Author = "ghost"
	}
	for _, l := range n.Labels.Nodes {
		p.Labels = append(p.Labels, l.Name)
	}
	p.AuthorKind, p.Agent = c.classify(p.Author, typename, n.HeadRefName, n.Body, p.Labels)
	keys := map[string]bool{}
	for _, k := range jiraKeyRe.FindAllString(n.Title+" "+strings.ToUpper(n.HeadRefName), -1) {
		if !keys[k] && !strings.HasPrefix(k, "CVE-") && !strings.HasPrefix(k, "UTF-") && !strings.HasPrefix(k, "SHA-") {
			keys[k] = true
			p.JiraKeys = append(p.JiraKeys, k)
		}
	}
	return p
}

type agentClassifier struct {
	extra map[string]bool
}

func newAgentClassifier(extra []string) *agentClassifier {
	c := &agentClassifier{extra: map[string]bool{}}
	for _, l := range extra {
		c.extra[strings.ToLower(l)] = true
	}
	return c
}

// Known coding agents, matched against author logins and branch prefixes.
var agentNames = []string{"claude", "copilot", "devin", "cursor", "codex", "jules", "openhands", "sweep", "codegen", "factory-droid", "amazon-q", "gemini", "aider", "pi"}

// Dependency/maintenance bots are machines but not "agents" doing feature work.
var maintenanceBots = []string{"dependabot", "renovate", "github-actions", "snyk", "mergify", "release-please", "changeset", "imgbot", "allcontributors", "greenkeeper", "pre-commit-ci"}

var bodyAgentMarkers = []struct{ marker, agent string }{
	{"generated with [claude code]", "claude"},
	{"co-authored-by: claude", "claude"},
	{"claude.ai/code", "claude"},
	{"co-authored-by: copilot", "copilot"},
	{"generated by copilot", "copilot"},
	{"devin.ai", "devin"},
	{"cursor.com/agents", "cursor"},
	{"chatgpt.com/codex", "codex"},
	{"jules.google", "jules"},
}

// classify returns (kind, agent). Agent-assisted PRs opened under a human
// account count as agent output; the human login stays as the author.
func (c *agentClassifier) classify(login, typename, branch, body string, labels []string) (string, string) {
	l := strings.ToLower(login)
	bare := strings.TrimSuffix(l, "[bot]")
	if c.extra[l] || c.extra[bare] {
		return "agent", bare
	}
	isBot := typename == "Bot" || strings.HasSuffix(l, "[bot]")
	for _, m := range maintenanceBots {
		if strings.Contains(bare, m) {
			return "bot", ""
		}
	}
	for _, a := range agentNames {
		if a == "pi" {
			continue // too short to match inside logins
		}
		if strings.Contains(bare, a) {
			return "agent", a
		}
	}
	if isBot {
		return "bot", ""
	}
	br := strings.ToLower(branch)
	for _, a := range agentNames {
		if strings.HasPrefix(br, a+"/") || strings.HasPrefix(br, a+"-") {
			return "agent", a
		}
	}
	lb := strings.ToLower(body)
	for _, m := range bodyAgentMarkers {
		if strings.Contains(lb, m.marker) {
			return "agent", m.agent
		}
	}
	for _, lab := range labels {
		ll := strings.ToLower(lab)
		for _, a := range agentNames {
			if a != "pi" && strings.Contains(ll, a) {
				return "agent", a
			}
		}
	}
	return "human", ""
}
