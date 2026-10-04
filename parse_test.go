package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassify(t *testing.T) {
	c := newAgentClassifier([]string{"my-agent-bot"})
	cases := []struct {
		login, typename, branch, body string
		kind, agent                   string
	}{
		{"alice", "User", "feature/x", "", "human", ""},
		{"dependabot", "Bot", "dependabot/npm/x", "", "bot", ""},
		{"renovate[bot]", "Bot", "renovate/x", "", "bot", ""},
		{"Copilot", "Bot", "copilot/fix", "", "agent", "copilot"},
		{"devin-ai-integration[bot]", "Bot", "devin/123", "", "agent", "devin"},
		{"some-ci[bot]", "Bot", "x", "", "bot", ""},
		{"alice", "User", "claude/fix-login", "", "agent", "claude"},
		{"bob", "User", "fix", "body\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)", "agent", "claude"},
		{"carol", "User", "codex/abc", "", "agent", "codex"},
		{"My-Agent-Bot", "User", "x", "", "agent", "my-agent-bot"},
		{"pierre", "User", "pin-deps", "", "human", ""}, // "pi" must not match inside words
	}
	for _, tc := range cases {
		kind, agent := c.classify(tc.login, tc.typename, tc.branch, tc.body, nil)
		if kind != tc.kind || agent != tc.agent {
			t.Errorf("classify(%q, %q, %q) = %s/%s, want %s/%s", tc.login, tc.typename, tc.branch, kind, agent, tc.kind, tc.agent)
		}
	}
}

func TestJiraKeys(t *testing.T) {
	n := ghPRNode{Title: "PROJ-12: fix login (CVE-2024-1234)", HeadRefName: "abc-99-thing"}
	p := toPR(n, newAgentClassifier(nil))
	if len(p.JiraKeys) != 2 || p.JiraKeys[0] != "PROJ-12" || p.JiraKeys[1] != "ABC-99" {
		t.Fatalf("jira keys = %v", p.JiraKeys)
	}
	if p.Author != "ghost" {
		t.Fatalf("nil author should be ghost, got %q", p.Author)
	}
}

func TestParseAcliIssues(t *testing.T) {
	arr := `Fetching...
[{"key":"PROJ-1","self":"https://acme.atlassian.net/rest/api/3/issue/10001","fields":{"summary":"Do it","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"assignee":{"displayName":"Ann"},"created":"2026-09-01T10:00:00.000+1000"}}]`
	issues, err := parseAcliIssues([]byte(arr))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].StatusCategory != "indeterminate" || issues[0].Assignee != "Ann" || issues[0].URL != "https://acme.atlassian.net/browse/PROJ-1" {
		t.Fatalf("got %+v", issues)
	}
	if parseTime(issues[0].Created).IsZero() {
		t.Fatal("jira timestamp did not parse")
	}
	env := `{"issues":[{"key":"PROJ-2","fields":{"summary":"x"}}]}`
	issues, err = parseAcliIssues([]byte(env))
	if err != nil || len(issues) != 1 || issues[0].Key != "PROJ-2" {
		t.Fatalf("envelope: %v %+v", err, issues)
	}
}

func TestParseVercelList(t *testing.T) {
	out := `{"contextName":"acme","deployments":[{"id":"dpl_1","url":"web-abc.vercel.app","name":"web","state":"READY","target":"production","createdAt":1700000000000,"ready":1700000060000,"creator":{"username":"ann"},"meta":{"githubOrg":"acme","githubRepo":"web","githubCommitRef":"main","githubCommitMessage":"Ship it","githubCommitSha":"abc"}},{"id":"dpl_2","url":"x.vercel.app","name":"api","state":"ERROR","target":null,"createdAt":1690000000000,"meta":{}}],"pagination":{"count":2,"next":1690000000000}}`
	deps, next, err := parseVercelList([]byte(out), "")
	if err != nil {
		t.Fatal(err)
	}
	if next != 1690000000000 || len(deps) != 2 {
		t.Fatalf("next=%d deps=%d", next, len(deps))
	}
	if d := deps[0]; d.Repo != "acme/web" || d.Target != "production" || d.Scope != "acme" || d.CommitMsg != "Ship it" {
		t.Fatalf("got %+v", d)
	}
	if deps[1].Target != "preview" {
		t.Fatalf("null target should be preview, got %q", deps[1].Target)
	}
}

func TestGuard(t *testing.T) {
	cfg := defaultConfig()
	h := guard(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	check := func(method, host, header string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+host+"/api/sync", nil)
		if header != "" {
			r.Header.Set("X-Factory-Dashboard", header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s %s header=%q: got %d want %d", method, host, header, w.Code, want)
		}
	}
	check("GET", "127.0.0.1:7420", "", 200)
	check("GET", "localhost:7420", "", 200)
	check("GET", "evil.example:7420", "", 403)
	check("POST", "127.0.0.1:7420", "", 403)
	check("POST", "127.0.0.1:7420", "1", 200)
}
