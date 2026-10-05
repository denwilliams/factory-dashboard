"use strict";
(() => {
  const HOUR = 3600e3, DAY = 24 * HOUR;
  const WINDOWS = [
    { id: "1d", label: "24h", ms: DAY },
    { id: "7d", label: "7d", ms: 7 * DAY },
    { id: "14d", label: "14d", ms: 14 * DAY },
    { id: "30d", label: "30d", ms: 30 * DAY },
  ];
  const KIND_LABEL = { human: "Human", agent: "Agent", bot: "Bot" };
  const KINDS = ["human", "agent", "bot"];

  const state = { status: null, gh: null, jira: null, vercel: null, summary: null, win: "7d", repoFilter: "", lastRuns: "" };
  const $ = (id) => document.getElementById(id);

  // ---- utils -------------------------------------------------------------
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const safeURL = (u) => (/^https?:\/\//i.test(u || "") ? u : "#");
  const link = (url, text, cls = "") => `<a href="${esc(safeURL(url))}" target="_blank" rel="noopener noreferrer"${cls ? ` class="${cls}"` : ""}>${esc(text)}</a>`;
  const ts = (s) => (s ? (typeof s === "number" ? s : Date.parse(s)) || 0 : 0);
  const fmtN = (n) => (n == null || isNaN(n) ? "–" : n.toLocaleString());
  function ago(t) {
    if (!t) return "never";
    const s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
    return `${Math.floor(s / 86400)}d ago`;
  }
  function short(t) {
    if (!t) return "";
    const s = (Date.now() - t) / 1000;
    if (s < 3600) return `${Math.max(1, Math.floor(s / 60))}m`;
    if (s < 86400) return `${Math.floor(s / 3600)}h`;
    return `${Math.floor(s / 86400)}d`;
  }
  function dur(ms) {
    if (ms == null || isNaN(ms)) return "–";
    const h = ms / HOUR;
    if (h < 1) return `${Math.round(ms / 60e3)}m`;
    if (h < 48) return `${h.toFixed(h < 10 ? 1 : 0)}h`;
    return `${(h / 24).toFixed(1)}d`;
  }
  const median = (a) => {
    if (!a.length) return null;
    const s = [...a].sort((x, y) => x - y), m = s.length >> 1;
    return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
  };
  // The human who drove an agent-authored PR, if any (not the agent's own bot account).
  const operator = (p) => (p.author_kind === "agent" && p.agent && !p.author.toLowerCase().includes(p.agent) ? p.author : "");
  const whoLabel = (p) => (p.author_kind === "agent" ? [p.agent, operator(p)].filter(Boolean).join(" · ") : p.author);
  const pct = (a, b) => (b ? Math.round((a / b) * 100) : 0);
  const winMs = () => WINDOWS.find((w) => w.id === state.win).ms;
  const ls = { get(k) { try { return localStorage.getItem(k); } catch { return null; } }, set(k, v) { try { localStorage.setItem(k, v); } catch {} } };

  async function getJSON(url) {
    const r = await fetch(url, { cache: "no-store" });
    if (!r.ok) throw new Error(`${url}: ${r.status}`);
    return r.json();
  }
  async function post(url) {
    return fetch(url, { method: "POST", headers: { "X-Factory-Dashboard": "1" } });
  }

  // ---- data --------------------------------------------------------------
  async function loadStatus() {
    state.status = await getJSON("api/status");
    const runs = Object.entries(state.status.sources || {}).map(([k, v]) => k + v.last_run).sort().join("|");
    const changed = runs !== state.lastRuns;
    state.lastRuns = runs;
    return changed;
  }
  async function loadData() {
    const [gh, jira, vercel, summary] = await Promise.all(["github", "jira", "vercel", "summary"].map((n) => getJSON(`api/data/${n}`).catch(() => ({}))));
    Object.assign(state, { gh, jira, vercel, summary });
  }

  function derive() {
    const now = Date.now(), w = winMs(), from = now - w, prevFrom = from - w;
    const prs = (state.gh && state.gh.prs) || [];
    const issues = (state.jira && state.jira.issues) || [];
    const deps = (state.vercel && state.vercel.deployments) || [];
    for (const p of prs) { p._c = ts(p.created_at); p._u = ts(p.updated_at); p._m = ts(p.merged_at); }
    for (const i of issues) { i._c = ts(i.created); i._u = ts(i.updated); i._r = ts(i.resolved); }
    const merged = prs.filter((p) => p.state === "MERGED" && p._m >= from);
    const mergedPrev = prs.filter((p) => p.state === "MERGED" && p._m >= prevFrom && p._m < from);
    const open = prs.filter((p) => p.state === "OPEN");
    const prod = deps.filter((d) => d.target === "production" && d.created_at >= from);
    return { now, w, from, prevFrom, prs, issues, deps, merged, mergedPrev, open, prod };
  }

  // ---- render: top bar ---------------------------------------------------
  function renderSources() {
    const st = state.status || { sources: {} };
    const names = { github: "GitHub", jira: "Jira", vercel: "Vercel", summary: "Summary" };
    const html = Object.keys(names).map((k) => {
      const s = st.sources[k];
      let cls = "", text = "never synced", title = "";
      if (st.syncing) { cls = "busy"; text = "syncing"; }
      else if (!s) { cls = ""; }
      else if (s.skipped && !s.last_run) { cls = "warn"; text = "skipped"; title = s.skipped; }
      else if (!s.ok) { cls = "bad"; text = `failed ${ago(ts(s.last_run))}`; title = s.error; }
      else { cls = "good"; text = ago(ts(s.last_ok)); title = s.skipped ? s.skipped : `${s.count} items in ${(s.duration_ms / 1000).toFixed(1)}s`; }
      return `<span class="chip" title="${esc(title)}"><span class="dot ${cls}"></span>${names[k]} · ${esc(text)}</span>`;
    }).join("");
    $("sources").innerHTML = html;
    $("syncBtn").disabled = !!st.syncing;
    $("syncBtn").textContent = st.syncing ? "Syncing…" : "Sync now";

    const errs = Object.entries(st.sources).filter(([, s]) => s && ((!s.ok && s.error) || (s.skipped && !s.last_run)));
    const banner = $("banner");
    if (errs.length) {
      banner.hidden = false;
      banner.innerHTML = errs.map(([k, s]) => `<div><b>${esc(names[k] || k)}:</b> ${esc(s.error || s.skipped)}</div>`).join("");
    } else banner.hidden = true;
  }

  function renderWindowControl() {
    const lookback = (state.status && state.status.lookback_days) || 14;
    const opts = WINDOWS.filter((w) => w.ms <= lookback * DAY);
    if (!opts.find((w) => w.id === state.win)) state.win = opts[opts.length - 1].id;
    $("window").innerHTML = opts.map((w) => `<button type="button" role="radio" aria-checked="${w.id === state.win}" data-win="${w.id}">${w.label}</button>`).join("");
  }

  // ---- render: KPIs --------------------------------------------------------
  function renderKPIs(d) {
    const byKind = (list) => KINDS.map((k) => list.filter((p) => p.author_kind === k).length);
    const [h, a, b] = byKind(d.merged);
    const prevN = d.mergedPrev.length;
    const coversPrev = state.gh && state.gh.since && ts(state.gh.since) <= d.prevFrom;
    const delta = coversPrev && prevN ? Math.round(((d.merged.length - prevN) / prevN) * 100) : null;
    const awaiting = d.open.filter((p) => !p.draft && p.review !== "APPROVED").length;
    const stale = d.open.filter((p) => !p.draft && d.now - p._u > 7 * DAY).length;
    const cycle = median(d.merged.map((p) => p._m - p._c));
    const agentCycle = median(d.merged.filter((p) => p.author_kind === "agent").map((p) => p._m - p._c));
    const prodFail = d.prod.filter((x) => x.state === "ERROR").length;
    const prodDone = d.prod.filter((x) => x.state === "READY" || x.state === "ERROR").length;
    const resolved = d.issues.filter((i) => i.status_category === "done" && i._r >= d.from).length;
    const created = d.issues.filter((i) => i._c >= d.from).length;
    const hasJira = !!(state.jira && state.jira.issues);
    const hasVercel = !!(state.vercel && state.vercel.deployments);

    const bar = d.merged.length
      ? `<div class="bar" aria-hidden="true">${[[h, "human"], [a, "agent"], [b, "bot"]].filter(([n]) => n).map(([n, k]) => `<span class="sw ${k}" style="flex:${n};height:6px"></span>`).join("")}</div>`
      : "";
    const tiles = [
      { label: "PRs merged", value: fmtN(d.merged.length), extra: delta == null ? "" : `<span class="delta">${delta >= 0 ? "▲" : "▼"} ${Math.abs(delta)}%</span>`, sub: `${h} human · ${a} agent · ${b} bot`, after: bar },
      { label: "Agent share", value: d.merged.length ? `${pct(a, d.merged.length)}%` : "–", sub: "of merged PRs" },
      { label: "Open PRs", value: fmtN(d.open.length), sub: `${awaiting} awaiting review · ${stale} stale` },
      { label: "Median cycle time", value: dur(cycle), sub: agentCycle != null ? `agents ${dur(agentCycle)}` : "opened → merged" },
      { label: "Prod deploys", value: hasVercel ? fmtN(d.prod.length) : "–", sub: hasVercel ? `${prodFail} failed${prodDone ? ` (${pct(prodFail, prodDone)}%)` : ""}` : "no Vercel data" },
      { label: "Jira resolved", value: hasJira ? fmtN(resolved) : "–", sub: hasJira ? `${created} created` : "no Jira data" },
    ];
    $("kpis").innerHTML = tiles.map((t) => `<div class="kpi"><div class="label">${t.label}</div><div class="value">${t.value}${t.extra || ""}</div><div class="sub">${esc(t.sub)}</div>${t.after || ""}</div>`).join("");
  }

  // ---- render: production line -------------------------------------------
  function kindBadge(p) {
    const label = p.author_kind === "agent" && p.agent ? p.agent : KIND_LABEL[p.author_kind] || p.author_kind;
    return `<span class="badge"><span class="sw ${esc(p.author_kind)}"></span>${esc(label)}</span>`;
  }
  function depState(s) {
    const cls = s === "READY" ? "good" : s === "ERROR" ? "critical" : s === "CANCELED" ? "" : "warning";
    const label = { READY: "Ready", ERROR: "Failed", BUILDING: "Building", QUEUED: "Queued", CANCELED: "Canceled", INITIALIZING: "Starting" }[s] || s;
    return `<span class="st ${cls}">${esc(label)}</span>`;
  }
  function renderLine(d) {
    const todo = d.issues.filter((i) => i.status_category === "new").sort((x, y) => y._u - x._u);
    const doing = d.issues.filter((i) => i.status_category === "indeterminate").sort((x, y) => y._u - x._u);
    const open = [...d.open].sort((x, y) => y._u - x._u);
    const merged = [...d.merged].sort((x, y) => y._m - x._m);
    const delivered = d.prod.filter((x) => x.state === "READY" || x.state === "ERROR");
    const prKeys = new Set(d.prs.flatMap((p) => p.jira_keys || []));
    const hasJira = !!(state.jira && state.jira.issues);

    const issueItem = (i) => `<li class="item">${i.url ? link(i.url, i.summary, "t") : `<span class="t">${esc(i.summary)}</span>`}<span class="m"><span class="mono">${esc(i.key)}</span>${esc(i.assignee || "unassigned")}${i.status_category === "indeterminate" && !prKeys.has(i.key) ? ' · <span class="st warning">no PR</span>' : ""}</span></li>`;
    const prItem = (p, t) => `<li class="item">${link(p.url, p.title, "t")}<span class="m">${kindBadge(p)}<span class="mono">${esc(p.repo.split("/")[1] || p.repo)}#${p.number}</span>${p.draft ? "draft · " : ""}${short(t)}</span></li>`;
    const depItem = (x) => `<li class="item">${link("https://" + x.url, x.commit_message || x.project, "t")}<span class="m">${depState(x.state)}<span class="mono">${esc(x.project)}</span>${short(x.created_at)}</span></li>`;

    const stages = [
      { name: "Inbound", src: "Jira · to do", items: hasJira ? todo : null, render: issueItem },
      { name: "In progress", src: "Jira · in progress", items: hasJira ? doing : null, render: issueItem },
      { name: "Assembly", src: "GitHub · open PRs", items: open, render: (p) => prItem(p, p._u) },
      { name: "Shipped", src: "GitHub · merged", items: merged, render: (p) => prItem(p, p._m) },
      { name: "Delivered", src: "Vercel · production", items: state.vercel && state.vercel.deployments ? delivered : null, render: depItem },
    ];
    const N = 6;
    $("line").innerHTML = stages.map((s) => {
      const body = s.items == null ? `<div class="empty">No data</div>`
        : s.items.length ? `<ul class="items">${s.items.slice(0, N).map(s.render).join("")}</ul>${s.items.length > N ? `<div class="more">+${s.items.length - N} more</div>` : ""}`
        : `<div class="empty">Nothing here</div>`;
      return `<div class="stage"><div class="stage-head"><div><span class="name">${s.name}</span><span class="src">${s.src}</span></div><span class="count">${s.items == null ? "–" : fmtN(s.items.length)}</span></div>${body}</div>`;
    }).join("");
    $("lineNote").textContent = `Shipped & delivered: last ${WINDOWS.find((w) => w.id === state.win).label}`;
  }

  // ---- render: throughput chart ------------------------------------------
  function buckets(d) {
    const hourly = d.w <= DAY;
    const step = hourly ? HOUR : DAY;
    const n = Math.round(d.w / step);
    const end = new Date(d.now);
    if (hourly) end.setMinutes(0, 0, 0); else end.setHours(0, 0, 0, 0);
    const start = end.getTime() - (n - 1) * step;
    const out = [];
    for (let i = 0; i < n; i++) {
      const t = hourly ? start + i * step : new Date(end.getFullYear(), end.getMonth(), end.getDate() - (n - 1 - i)).getTime();
      out.push({ t, human: 0, agent: 0, bot: 0 });
    }
    const idx = (t) => {
      if (hourly) return Math.floor((t - start) / step);
      const dd = new Date(t); dd.setHours(0, 0, 0, 0);
      return Math.round((dd.getTime() - out[0].t) / DAY);
    };
    for (const p of d.merged) {
      const i = idx(p._m);
      if (i >= 0 && i < n && out[i][p.author_kind] != null) out[i][p.author_kind]++;
    }
    return { out, hourly };
  }
  function bucketLabel(t, hourly, long) {
    const dt = new Date(t);
    if (hourly) return dt.toLocaleTimeString([], { hour: "numeric" });
    return dt.toLocaleDateString([], long ? { weekday: "short", month: "short", day: "numeric" } : { month: "short", day: "numeric" });
  }
  function renderChart(d) {
    const { out, hourly } = buckets(d);
    $("legend").innerHTML = KINDS.map((k) => `<span><span class="sw ${k}"></span>${KIND_LABEL[k]}</span>`).join("");
    const el = $("chart");
    const W = Math.max(280, el.clientWidth || 600), H = el.clientHeight || 240;
    const m = { t: 8, r: 4, b: 22, l: 28 };
    const iw = W - m.l - m.r, ih = H - m.t - m.b;
    const max = Math.max(1, ...out.map((b) => b.human + b.agent + b.bot));
    const nice = niceMax(max), ticks = 4;
    const y = (v) => m.t + ih - (v / nice) * ih;
    const bw = iw / out.length, barW = Math.max(2, Math.min(28, bw * 0.62));
    let svg = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="PRs merged per ${hourly ? "hour" : "day"} by humans, agents and bots">`;
    for (let i = 0; i <= ticks; i++) {
      const v = (nice / ticks) * i, yy = y(v);
      svg += `<line class="gridline" x1="${m.l}" x2="${W - m.r}" y1="${yy}" y2="${yy}"/><text class="axis" x="${m.l - 6}" y="${yy + 3.5}" text-anchor="end">${Math.round(v)}</text>`;
    }
    const every = Math.ceil(out.length / Math.max(2, Math.floor(iw / 64)));
    out.forEach((b, i) => {
      const cx = m.l + bw * i + bw / 2, x = cx - barW / 2;
      let acc = 0;
      const segs = KINDS.filter((k) => b[k] > 0);
      segs.forEach((k, si) => {
        const v = b[k], y0 = y(acc), y1 = y(acc + v);
        const top = si === segs.length - 1;
        const h = Math.max(0, y0 - y1 - (top ? 0 : 2)); // 2px surface gap between stacked fills
        const yTop = top ? y1 : y1 + 2;
        svg += top ? roundedTop(x, yTop, barW, h, Math.min(4, barW / 2), `var(--series-${KINDS.indexOf(k) + 1})`)
          : `<rect x="${x}" y="${yTop}" width="${barW}" height="${h}" fill="var(--series-${KINDS.indexOf(k) + 1})"/>`;
        acc += v;
      });
      if (i % every === (out.length - 1) % every) svg += `<text class="axis" x="${cx}" y="${H - 6}" text-anchor="middle">${esc(bucketLabel(b.t, hourly))}</text>`;
      svg += `<rect class="hit" data-i="${i}" x="${m.l + bw * i}" y="${m.t}" width="${bw}" height="${ih}"/>`;
    });
    svg += `<line class="gridline" x1="${m.l}" x2="${W - m.r}" y1="${y(0)}" y2="${y(0)}" style="stroke:var(--muted);opacity:.5"/></svg>`;
    el.innerHTML = svg;
    el.onmousemove = (e) => {
      const i = e.target.dataset && e.target.dataset.i;
      if (i == null) return hideTip();
      const b = out[+i], total = b.human + b.agent + b.bot;
      showTip(e, `<div style="margin-bottom:4px;color:var(--text-2)">${esc(bucketLabel(b.t, hourly, true))}</div>${KINDS.map((k) => `<div class="row"><span><span class="sw ${k}"></span> ${KIND_LABEL[k]}</span><b>${b[k]}</b></div>`).join("")}<div class="row" style="border-top:1px solid var(--border);margin-top:4px;padding-top:4px"><span>Total</span><b>${total}</b></div>`);
    };
    el.onmouseleave = hideTip;
    $("chartTable").innerHTML = `<table><thead><tr><th>${hourly ? "Hour" : "Day"}</th>${KINDS.map((k) => `<th class="num">${KIND_LABEL[k]}</th>`).join("")}<th class="num">Total</th></tr></thead><tbody>${out.map((b) => `<tr><td>${esc(bucketLabel(b.t, hourly, true))}</td>${KINDS.map((k) => `<td class="num">${b[k]}</td>`).join("")}<td class="num">${b.human + b.agent + b.bot}</td></tr>`).join("")}</tbody></table>`;
  }
  function niceMax(v) {
    const p = Math.pow(10, Math.floor(Math.log10(v)));
    for (const f of [1, 2, 2.5, 5, 10]) if (f * p >= v) return Math.max(4, f * p);
    return v;
  }
  function roundedTop(x, y, w, h, r, fill) {
    if (h <= 0) return "";
    r = Math.min(r, h);
    return `<path d="M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z" fill="${fill}"/>`;
  }
  function showTip(e, html) {
    const tip = $("tooltip");
    tip.innerHTML = html;
    tip.hidden = false;
    const r = tip.getBoundingClientRect();
    let x = e.clientX + 14, y = e.clientY + 14;
    if (x + r.width > innerWidth - 8) x = e.clientX - r.width - 14;
    if (y + r.height > innerHeight - 8) y = e.clientY - r.height - 14;
    tip.style.left = `${x}px`;
    tip.style.top = `${y}px`;
  }
  const hideTip = () => { $("tooltip").hidden = true; };

  // ---- render: people & repos ----------------------------------------------
  function renderPeople(d) {
    const rows = new Map();
    const key = (p) => (p.author_kind === "agent" ? `agent:${p.agent || p.author}` : `${p.author_kind}:${p.author}`);
    const get = (p) => {
      const k = key(p);
      if (!rows.has(k)) rows.set(k, { name: p.author_kind === "agent" ? p.agent || p.author : p.author, kind: p.author_kind, merged: 0, open: 0, lines: 0, operators: new Set() });
      return rows.get(k);
    };
    for (const p of d.merged) { const r = get(p); r.merged++; r.lines += p.additions + p.deletions; if (operator(p)) r.operators.add(operator(p)); }
    for (const p of d.open) get(p).open++;
    const list = [...rows.values()].sort((a, b) => b.merged - a.merged || b.open - a.open);
    if (!list.length) { $("people").innerHTML = `<div class="empty">No PR activity in this window.</div>`; return; }
    $("people").innerHTML = `<table><thead><tr><th>Who</th><th>Kind</th><th class="num">Merged</th><th class="num">Open</th><th class="num">Lines changed</th></tr></thead><tbody>${list.map((r) => `<tr><td title="${r.operators.size ? esc("with " + [...r.operators].join(", ")) : ""}">${esc(r.name)}${r.operators.size ? ` <span class="muted">· ${r.operators.size} operator${r.operators.size > 1 ? "s" : ""}</span>` : ""}</td><td><span class="badge"><span class="sw ${esc(r.kind)}"></span>${KIND_LABEL[r.kind] || esc(r.kind)}</span></td><td class="num">${r.merged}</td><td class="num">${r.open}</td><td class="num">${fmtN(r.lines)}</td></tr>`).join("")}</tbody></table>`;
  }

  function renderRepos(d) {
    const repos = new Map();
    const get = (name) => {
      if (!repos.has(name)) repos.set(name, { name, merged: 0, agent: 0, open: 0, lines: 0, deploy: null });
      return repos.get(name);
    };
    for (const p of d.merged) { const r = get(p.repo); r.merged++; if (p.author_kind === "agent") r.agent++; r.lines += p.additions + p.deletions; }
    for (const p of d.open) get(p.repo).open++;
    const prodAll = d.deps.filter((x) => x.target === "production");
    for (const r of repos.values()) {
      const short = r.name.split("/")[1];
      r.deploy = prodAll.find((x) => x.repo === r.name) || prodAll.find((x) => x.project === short) || null;
    }
    const f = state.repoFilter.toLowerCase();
    const list = [...repos.values()].filter((r) => !f || r.name.toLowerCase().includes(f)).sort((a, b) => b.merged - a.merged || b.open - a.open);
    if (!list.length) { $("repos").innerHTML = `<div class="empty">No repositories match.</div>`; return; }
    $("repos").innerHTML = `<table><thead><tr><th>Repository</th><th class="num">Merged</th><th class="num">Agent</th><th class="num">Open</th><th>Last prod deploy</th></tr></thead><tbody>${list.map((r) => `<tr><td>${link("https://github.com/" + r.name, r.name)}</td><td class="num">${r.merged}</td><td class="num">${r.merged ? pct(r.agent, r.merged) + "%" : "–"}</td><td class="num">${r.open}</td><td>${r.deploy ? `${depState(r.deploy.state)} <span class="muted">${ago(r.deploy.created_at)}</span>` : '<span class="muted">–</span>'}</td></tr>`).join("")}</tbody></table>`;
  }

  // ---- render: summary ---------------------------------------------------
  function markdown(src) {
    const inline = (s) => esc(s)
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/(^|[\s(])\*([^*\s][^*]*)\*/g, "$1<em>$2</em>")
      .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
    let html = "", inList = false;
    for (const raw of src.split(/\r?\n/)) {
      const line = raw.trimEnd();
      const li = line.match(/^\s*[-*+]\s+(.*)$/);
      if (li) { if (!inList) { html += "<ul>"; inList = true; } html += `<li>${inline(li[1])}</li>`; continue; }
      if (inList) { html += "</ul>"; inList = false; }
      const h = line.match(/^(#{1,4})\s+(.*)$/);
      if (h) { const lvl = Math.min(3, Math.max(2, h[1].length)); html += `<h${lvl}>${inline(h[2])}</h${lvl}>`; }
      else if (line.trim()) html += `<p>${inline(line)}</p>`;
    }
    if (inList) html += "</ul>";
    return html;
  }
  function renderSummary() {
    const s = state.summary || {};
    const st = (state.status && state.status.sources && state.status.sources.summary) || null;
    if (s.markdown) {
      $("summary").innerHTML = `<div class="muted" style="margin-bottom:8px">Generated by pi ${esc(ago(ts(s.generated_at)))}</div>${markdown(s.markdown)}`;
    } else {
      const why = st && (st.error || st.skipped);
      $("summary").innerHTML = `<div class="empty">No changelog yet.${why ? " " + esc(why) : " It is generated with pi after the first GitHub sync."}</div>`;
    }
  }

  // ---- render: activity feed ---------------------------------------------
  function renderFeed(d) {
    const ev = [];
    for (const p of d.merged) ev.push({ t: p._m, kind: "Merged", html: link(p.url, `${p.repo.split("/")[1]}#${p.number} ${p.title}`), who: whoLabel(p), sw: p.author_kind });
    for (const p of d.prs) if (p._c >= d.from && p.state === "OPEN") ev.push({ t: p._c, kind: "Opened", html: link(p.url, `${p.repo.split("/")[1]}#${p.number} ${p.title}`), who: whoLabel(p), sw: p.author_kind });
    for (const x of d.deps) if (x.created_at >= d.from && (x.target === "production" || x.state === "ERROR")) ev.push({ t: x.created_at, kind: x.target === "production" ? "Deployed" : "Preview", html: `${depState(x.state)} ${link("https://" + x.url, `${x.project} ${x.commit_message ? "— " + x.commit_message : ""}`)}`, who: x.creator || "" });
    for (const i of d.issues) {
      if (i.status_category === "done" && i._r >= d.from) ev.push({ t: i._r, kind: "Resolved", html: `<span class="mono">${esc(i.key)}</span> ${i.url ? link(i.url, i.summary) : esc(i.summary)}`, who: i.assignee || "" });
      else if (i._c >= d.from) ev.push({ t: i._c, kind: "Ticket", html: `<span class="mono">${esc(i.key)}</span> ${i.url ? link(i.url, i.summary) : esc(i.summary)}`, who: i.reporter || "" });
    }
    ev.sort((a, b) => b.t - a.t);
    const N = 50;
    $("feedNote").textContent = ev.length > N ? `latest ${N} of ${ev.length}` : `${ev.length} events`;
    $("feed").innerHTML = ev.length ? ev.slice(0, N).map((e) => `<li><span class="when">${esc(short(e.t))} ago</span><span class="kind badge">${e.sw ? `<span class="sw ${esc(e.sw)}"></span>` : ""}${e.kind}</span><span class="what">${e.html}</span><span class="who">${esc(e.who)}</span></li>`).join("") : `<li><span class="empty">No activity in this window yet.</span></li>`;
  }

  // ---- main --------------------------------------------------------------
  function render() {
    renderSources();
    renderWindowControl();
    const d = derive();
    renderKPIs(d);
    renderLine(d);
    renderChart(d);
    renderPeople(d);
    renderRepos(d);
    renderSummary();
    renderFeed(d);
  }

  let timer = null;
  async function tick(forceData) {
    clearTimeout(timer);
    try {
      const changed = await loadStatus();
      if (changed || forceData) await loadData();
      render();
    } catch (e) {
      const b = $("banner");
      b.hidden = false;
      b.textContent = `Can't reach the factory-dashboard daemon (${e.message}). Is it running?`;
    }
    const delay = state.status && state.status.syncing ? 3000 : 30000;
    if (!document.hidden) timer = setTimeout(() => tick(false), delay);
  }

  function init() {
    const theme = ls.get("fd-theme");
    if (theme) document.documentElement.dataset.theme = theme;
    const savedWin = ls.get("fd-window");
    if (savedWin && WINDOWS.find((w) => w.id === savedWin)) state.win = savedWin;

    $("window").addEventListener("click", (e) => {
      const w = e.target.dataset && e.target.dataset.win;
      if (!w) return;
      state.win = w;
      ls.set("fd-window", w);
      render();
    });
    $("syncBtn").addEventListener("click", async () => {
      $("syncBtn").disabled = true;
      await post("api/sync");
      setTimeout(() => tick(false), 500);
    });
    $("summarizeBtn").addEventListener("click", async () => {
      $("summarizeBtn").disabled = true;
      $("summarizeBtn").textContent = "Queued…";
      await post("api/summarize");
      setTimeout(() => { $("summarizeBtn").disabled = false; $("summarizeBtn").textContent = "Regenerate"; tick(false); }, 1500);
    });
    $("themeBtn").addEventListener("click", () => {
      const cur = document.documentElement.dataset.theme || (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
      const next = cur === "dark" ? "light" : "dark";
      document.documentElement.dataset.theme = next;
      ls.set("fd-theme", next);
    });
    $("repoFilter").addEventListener("input", (e) => { state.repoFilter = e.target.value; renderRepos(derive()); });
    document.addEventListener("visibilitychange", () => { if (!document.hidden) tick(false); });
    let rt;
    addEventListener("resize", () => { clearTimeout(rt); rt = setTimeout(() => renderChart(derive()), 150); });
    tick(true);
  }
  init();
})();
