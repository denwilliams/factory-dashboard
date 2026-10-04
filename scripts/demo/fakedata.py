#!/usr/bin/env python3
"""Fake gh/acli/vercel/pi output for `make demo`. Not used in normal operation."""
import json, os, random, sys, time
from datetime import datetime, timedelta, timezone

random.seed(int(time.time() // 900))  # stable within a 15 minute sync window
now = datetime.now(timezone.utc)
REPOS = ["acme/web", "acme/api", "acme/mobile", "acme/infra", "acme/billing"]
HUMANS = ["alice", "bob", "carol", "dan"]
WORDS = ["checkout", "login", "search", "billing", "onboarding", "cache", "webhooks", "reports", "rate limits", "SSO"]
VERBS = ["Fix", "Add", "Refactor", "Speed up", "Harden", "Remove", "Document"]

def iso(dt): return dt.strftime("%Y-%m-%dT%H:%M:%SZ")

def prs():
    out = []
    for n in range(1, 140):
        created = now - timedelta(hours=random.uniform(1, 24 * 16))
        r = random.random()
        if r < 0.15:   author, typ, branch, body = "Copilot", "Bot", f"copilot/task-{n}", ""
        elif r < 0.35: author, typ, branch, body = random.choice(HUMANS), "User", f"claude/task-{n}", "Generated with [Claude Code]"
        elif r < 0.42: author, typ, branch, body = "dependabot", "Bot", f"dependabot/npm/pkg-{n}", ""
        elif r < 0.50: author, typ, branch, body = "devin-ai-integration", "Bot", f"devin/{n}", ""
        else:          author, typ, branch, body = random.choice(HUMANS), "User", f"PROJ-{random.randint(1,60)}-work", ""
        merged = closed = None
        state = random.choices(["MERGED", "OPEN", "CLOSED"], [0.7, 0.25, 0.05])[0]
        if state != "OPEN":
            end = min(now, created + timedelta(hours=random.uniform(0.3, 72)))
            closed = iso(end)
            if state == "MERGED": merged = closed
        repo = random.choice(REPOS)
        out.append({"number": n, "title": f"{random.choice(VERBS)} {random.choice(WORDS)}" + (f" (PROJ-{random.randint(1,60)})" if random.random() < .4 else ""),
            "url": f"https://github.com/{repo}/pull/{n}", "state": state, "isDraft": state == "OPEN" and random.random() < .2,
            "createdAt": iso(created), "updatedAt": closed or iso(min(now, created + timedelta(hours=random.uniform(0, 200)))),
            "mergedAt": merged, "closedAt": closed, "additions": random.randint(1, 800), "deletions": random.randint(0, 300),
            "headRefName": branch, "reviewDecision": random.choice(["APPROVED", "REVIEW_REQUIRED", None]), "body": body,
            "author": {"login": author, "__typename": typ}, "repository": {"nameWithOwner": repo}, "labels": {"nodes": []}})
    return out

def gh(args):
    if "user/orgs" in args: print("acme"); return
    q = next(a[2:] for a in args if a.startswith("q="))
    want = "OPEN" if "is:open" in q else "MERGED" if "is:merged" in q else "CLOSED"
    nodes = [p for p in prs() if p["state"] == want]
    print(json.dumps({"data": {"search": {"issueCount": len(nodes), "pageInfo": {"hasNextPage": False, "endCursor": None}, "nodes": nodes}}}))

def acli(args):
    cats = [("To Do", "new"), ("In Progress", "indeterminate"), ("In Review", "indeterminate"), ("Done", "done")]
    issues = []
    for n in range(1, 61):
        st, cat = random.choice(cats)
        created = now - timedelta(hours=random.uniform(1, 24 * 20))
        issues.append({"key": f"PROJ-{n}", "self": f"https://acme.atlassian.net/rest/api/3/issue/{10000+n}", "fields": {
            "summary": f"{random.choice(VERBS)} {random.choice(WORDS)} for customers", "status": {"name": st, "statusCategory": {"key": cat}},
            "assignee": {"displayName": random.choice(["Alice", "Bob", "Carol", "Dan"])} if random.random() < .8 else None,
            "reporter": {"displayName": "PM"}, "issuetype": {"name": random.choice(["Story", "Bug", "Task"])},
            "created": created.strftime("%Y-%m-%dT%H:%M:%S.000+0000"), "updated": iso(now - timedelta(hours=random.uniform(0, 72))),
            "resolutiondate": (now - timedelta(hours=random.uniform(0, 24 * 10))).strftime("%Y-%m-%dT%H:%M:%S.000+0000") if cat == "done" else None}})
    print(json.dumps(issues))

def vercel(args):
    deps = []
    for i in range(80):
        t = now - timedelta(hours=random.uniform(0, 24 * 15))
        repo = random.choice(REPOS[:3])
        deps.append({"id": f"dpl_{i}", "url": f"{repo.split('/')[1]}-{i}.vercel.app", "name": repo.split("/")[1],
            "state": random.choices(["READY", "ERROR", "BUILDING"], [0.88, 0.1, 0.02])[0], "target": random.choice(["production", None, None]),
            "createdAt": int(t.timestamp() * 1000), "ready": int(t.timestamp() * 1000) + 60000, "creator": {"username": random.choice(HUMANS)},
            "meta": {"githubOrg": "acme", "githubRepo": repo.split("/")[1], "githubCommitRef": "main", "githubCommitMessage": f"{random.choice(VERBS)} {random.choice(WORDS)}"}})
    deps.sort(key=lambda d: -d["createdAt"])
    print(json.dumps({"contextName": "acme", "deployments": deps, "pagination": {"next": None}}))

def pi(args):
    sys.stdin.read()
    print("## Last 24 hours\n- Checkout and billing fixes landed across **acme/web** and **acme/api**.\n- Agents opened most of the small refactors.\n\n"
          "## Changelog (last 7 days)\n### acme/web\n- Faster search results\n- Fixed SSO redirect loop\n### acme/api\n- Added webhook retries\n\n"
          "## Watch list\n- 2 failed production deploys on `web`.\n- 3 PRs open for more than a week.")

{"gh": gh, "acli": acli, "vercel": vercel, "pi": pi}[sys.argv[1]](sys.argv[2:])
