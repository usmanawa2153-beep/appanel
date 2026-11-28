"use strict";
/* Appanel dashboard: dependency-free, DOM built with textContent (no innerHTML),
   so file names and server strings can never inject markup. */

const api = {
  async call(method, url, body, extra = {}) {
    const opt = { method, headers: { "X-Appanel-CSRF": "1" }, credentials: "same-origin", ...extra };
    if (body !== undefined) {
      opt.headers["Content-Type"] = "application/json";
      opt.body = JSON.stringify(body);
    }
    const res = await fetch(url, opt);
    let data = null;
    try { data = await res.json(); } catch { /* empty body */ }
    if (res.status === 401 && state.user) { state.user = null; render(); }
    if (!res.ok) {
      const err = new Error((data && data.error) || `Request failed (${res.status})`);
      err.log = data && data.log;
      throw err;
    }
    return data;
  },
  get: (u) => api.call("GET", u),
  post: (u, b) => api.call("POST", u, b ?? {}),
  put: (u, b) => api.call("PUT", u, b),
  del: (u) => api.call("DELETE", u),
};

const state = { user: null, ticket: null, timer: null, update: false };


const root = document.getElementById("app");
const fmtBytes = (n) => {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n >= 10 || i === 0 ? Math.round(n) : n.toFixed(1)} ${u[i]}`;
};
const fmtUptime = (s) => {
  const d = Math.floor(s / 86400), hr = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d ? `${d}d ${hr}h ${m}m` : hr ? `${hr}h ${m}m` : `${m}m`;
};
const pct = (a, b) => (b ? Math.min(100, (a / b) * 100) : 0);
const level = (p) => (p > 90 ? "bad" : p > 75 ? "warn" : "");

/* theme */
function applyTheme() {
  let t = null;
  try { t = localStorage.getItem("appanel-theme"); } catch { /* storage blocked */ }
  if (t) document.documentElement.dataset.theme = t;
}
function toggleTheme() {
  const dark = document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : matchMedia("(prefers-color-scheme: dark)").matches;
  const next = dark ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  try { localStorage.setItem("appanel-theme", next); } catch { /* ignore */ }
}

/* routing */
function route() {
  const [path, qs] = (location.hash.slice(1) || "/dashboard").split("?");
  return { path, q: new URLSearchParams(qs || "") };
}
const go = (hash) => { location.hash = hash; };

function stopTimer() {
  if (state.timer) { clearInterval(state.timer); state.timer = null; }
  if (state.onLeave) { const f = state.onLeave; state.onLeave = null; f(); }
}

function render() {
  stopTimer();
  root.replaceChildren(state.user ? shell() : loginView());
}
window.addEventListener("hashchange", () => { if (state.user) render(); });

/* ---- login ---- */
function loginView() {
  const err = h("div", { class: "msg err", hidden: true });
  const showErr = (m) => { err.textContent = m; err.hidden = false; };
  const card = h("div", { class: "card" });

  function head(title, lede) {
    return [h("img", { class: "logo", src: "favicon.svg", alt: "" }), h("h1", {}, title), h("p", { class: "lede" }, lede), err];
  }
  function passwordStep() {
    const u = h("input", { type: "text", autocomplete: "username", autofocus: true });
    const p = h("input", { type: "password", autocomplete: "current-password" });
    const btn = h("button", { class: "btn primary", type: "submit" }, "Sign in");
    const form = h("form", {
      onsubmit: async (e) => {
        e.preventDefault(); err.hidden = true; btn.disabled = true;
        try {
          const r = await api.post("api/login", { username: u.value, password: p.value });
          if (r.need_2fa) { state.ticket = r.ticket; twoFactorStep(); }
          else await boot();
        } catch (x) { showErr(x.message); } finally { btn.disabled = false; }
      },
    },
      h("label", { class: "field" }, h("span", {}, "Username"), u),
      h("label", { class: "field" }, h("span", {}, "Password"), p),
      btn);
    card.replaceChildren(...head("Welcome back", "Sign in to manage your server."), form);
  }
  function twoFactorStep() {
    const c = h("input", { type: "text", inputmode: "numeric", autocomplete: "one-time-code", maxlength: "6", autofocus: true });
    const btn = h("button", { class: "btn primary", type: "submit" }, "Verify");
    const form = h("form", {
      onsubmit: async (e) => {
        e.preventDefault(); err.hidden = true; btn.disabled = true;
        try { await api.post("api/login/2fa", { ticket: state.ticket, code: c.value }); await boot(); }
        catch (x) { showErr(x.message); } finally { btn.disabled = false; }
      },
    }, h("label", { class: "field" }, h("span", {}, "6-digit code from your authenticator app"), c), btn);
    err.hidden = true;
    card.replaceChildren(...head("Two-factor check", "Enter the code from your authenticator app."), form);
  }
  passwordStep();

  const NS = "http://www.w3.org/2000/svg";
  const art = document.createElementNS(NS, "svg");
  art.setAttribute("class", "grid-art"); art.setAttribute("viewBox", "0 0 400 400"); art.setAttribute("preserveAspectRatio", "xMidYMid slice");
  for (let i = 0; i <= 400; i += 40) {
    for (const [x1, y1, x2, y2] of [[i, 0, i, 400], [0, i, 400, i]]) {
      const l = document.createElementNS(NS, "line");
      l.setAttribute("x1", x1); l.setAttribute("y1", y1); l.setAttribute("x2", x2); l.setAttribute("y2", y2);
      l.setAttribute("stroke", "#1d2c36"); l.setAttribute("stroke-width", "1");
      art.append(l);
    }
  }
  return h("div", { class: "auth" },
    h("div", { class: "auth-art" }, art,
      h("div", {}, h("h2", {}, "Your server, in one calm place."),
        h("p", {}, "Websites, SSL, databases, backups and WordPress. Self-hosted, no subscriptions."))),
    h("div", { class: "auth-panel" }, card));
}

/* ---- shell ---- */
const NAV = [
  ["Overview", [["/dashboard", "Dashboard", "dashboard"]]],
  ["Hosting", [["/websites", "Websites", "globe"], ["/apps", "One-click apps", "apps"], ["/dns", "DNS", "dns"], ["/databases", "Databases", "database"]]],
  ["Server", [["/files", "File manager", "folder"], ["/terminal", "Terminal", "terminal"], ["/cron", "Cron jobs", "clock"], ["/firewall", "Firewall", "shield"], ["/backups", "Backups", "backup"]]],
  ["Panel", [["/settings", "Settings", "settings"]]],
];

function currentlyDark() {
  return document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : matchMedia("(prefers-color-scheme: dark)").matches;
}

function shell() {
  const { path } = route();
  const view = h("main", { class: "main" });
  const side = h("aside", { class: "side" });
  const closeMenu = () => side.classList.remove("open");
  const link = ([to, label, ic]) => h("a", {
    class: path.startsWith(to) || (to === "/websites" && path.startsWith("/wordpress")) ? "on" : "",
    href: "#" + to, onclick: closeMenu,
  }, icon(ic), label, to === "/settings" && state.update ? h("span", { class: "dot", title: "Update available" }) : null);

  const themeBtn = h("button", { title: "Switch theme", onclick: () => { toggleTheme(); themeBtn.replaceChildren(icon(currentlyDark() ? "sun" : "moon"), "Theme"); } },
    icon(currentlyDark() ? "sun" : "moon"), "Theme");
  side.append(
    h("div", { class: "brand" }, h("img", { src: "favicon.svg", alt: "" }), "Appanel"),
    h("nav", { class: "nav" }, NAV.flatMap(([g, items]) => [h("div", { class: "nav-group" }, g), ...items.map(link)])),
    h("div", { class: "spacer" }),
    h("div", { class: "side-card" },
      h("div", { class: "host" }, state.host || "This server"),
      h("div", { class: "meta" }, h("span", {}, state.user.username), h("span", {}, "v" + String(state.user.version).replace(/^v/, "")))),
    h("div", { class: "side-actions" }, themeBtn,
      h("button", { title: "Sign out", onclick: async () => { try { await api.post("api/logout"); } catch { /* already out */ } state.user = null; render(); } }, icon("logout"), "Sign out")));

  const bar = h("div", { class: "mobile-bar" },
    h("button", { "aria-label": "Menu", onclick: () => side.classList.toggle("open") }, icon("menu")), "Appanel",
    h("button", { "aria-label": "Search", onclick: openPalette }, icon("search")));

  if (path.startsWith("/files")) filesView(view);
  else if (path.startsWith("/websites")) websitesView(view);
  else if (path.startsWith("/wordpress")) wordpressView(view, Number(path.split("/")[2]));
  else if (path.startsWith("/terminal")) terminalView(view);
  else if (path.startsWith("/apps")) appsView(view);
  else if (path.startsWith("/dns")) dnsView(view);
  else if (path.startsWith("/databases")) databasesView(view);
  else if (path.startsWith("/backups")) backupsView(view);
  else if (path.startsWith("/cron")) cronView(view);
  else if (path.startsWith("/firewall")) firewallView(view);
  else if (path.startsWith("/settings")) settingsView(view);
  else dashboardView(view);
  return h("div", {}, bar, h("div", { class: "shell" }, side, view));
}

/* ---- command palette (Ctrl/Cmd+K) ---- */
function openPalette() {
  if (!state.user || document.querySelector("dialog.cmdk")) return;
  const items = NAV.flatMap(([, i]) => i).map(([to, label, ic]) => ({ to, label, ic }));
  items.push({ to: "/wordpress", label: "WordPress sites", ic: "globe", go: "/websites" });
  const dlg = h("dialog", { class: "cmdk" });
  const input = h("input", { type: "text", placeholder: "Jump to…", "aria-label": "Jump to a page" });
  const list = h("ul");
  let shown = items, sel = 0;
  const close = () => { dlg.close(); dlg.remove(); };
  const pick = (it) => { close(); go(it.go || it.to); };
  const draw = () => {
    list.replaceChildren(...shown.map((it, i) => h("li", { class: i === sel ? "on" : "", onclick: () => pick(it) }, icon(it.ic), it.label)));
    if (!shown.length) list.replaceChildren(h("li", {}, "Nothing matches"));
  };
  input.addEventListener("input", () => { const q = input.value.toLowerCase(); shown = items.filter((i) => i.label.toLowerCase().includes(q)); sel = 0; draw(); });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { sel = Math.min(sel + 1, shown.length - 1); draw(); e.preventDefault(); }
    else if (e.key === "ArrowUp") { sel = Math.max(sel - 1, 0); draw(); e.preventDefault(); }
    else if (e.key === "Enter" && shown[sel]) pick(shown[sel]);
  });
  dlg.addEventListener("click", (e) => { if (e.target === dlg) close(); });
  dlg.addEventListener("close", () => dlg.remove());
  dlg.append(input, list);
  document.body.append(dlg);
  draw(); dlg.showModal(); input.focus();
}
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") { e.preventDefault(); openPalette(); }
});

/* ---- dashboard ---- */
function dashboardView(view) {
  const body = h("div");
  view.append(ui.pageHead("Dashboard", "How your server is doing right now."), body);
  let extra = { fw: null, backups: null, sites: null, status: null, upd: null };
  const loadExtra = () => Promise.allSettled([api.get("api/firewall"), api.get("api/backups"), api.get("api/sites"), api.get("api/sites/status"), api.get("api/update")])
    .then(([fw, bk, st, ss, up]) => { extra = { fw: fw.value, backups: bk.value, sites: st.value, status: ss.value, upd: up.value }; });

  function checks(s) {
    const out = [];
    const diskP = pct(s.disk.used, s.disk.total), memP = pct(s.mem.used, s.mem.total);
    if (extra.upd && extra.upd.available) out.push({ lvl: "warn", t: "Panel update available", d: `Version ${extra.upd.latest} is ready to install.`, to: "/settings", b: "Update" });
    if (extra.fw && extra.fw.installed && !extra.fw.active) out.push({ lvl: "warn", t: "Firewall is off", d: "Every port on this server is reachable from the internet.", to: "/firewall", b: "Open firewall" });
    if (extra.fw && !extra.fw.installed) out.push({ lvl: "warn", t: "Firewall is not installed", d: "Install UFW to control which ports are open.", to: "/firewall", b: "Set up" });
    if (extra.status && !extra.status.nginx_installed) out.push({ lvl: "warn", t: "Nginx is not installed", d: "You need it to host websites.", to: "/websites", b: "Install" });
    if (extra.backups && (extra.backups.backups || []).length === 0 && (extra.sites?.sites || []).some((x) => !x.panel)) out.push({ lvl: "warn", t: "No backups yet", d: "Make a backup of your sites and databases before something goes wrong.", to: "/backups", b: "Back up" });
    if (diskP > 85) out.push({ lvl: diskP > 95 ? "bad" : "warn", t: `Disk is ${diskP.toFixed(0)}% full`, d: "Free some space or old backups to keep things running.", to: "/files", b: "Open files" });
    if (memP > 90) out.push({ lvl: "bad", t: `Memory is ${memP.toFixed(0)}% used`, d: "Apps may be killed. Consider adding swap or RAM.", to: null });
    return out;
  }

  async function refresh() {
    let s;
    try { s = await api.get("api/system"); } catch (x) { body.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    state.host = s.hostname;
    const memP = pct(s.mem.used, s.mem.total), diskP = pct(s.disk.used, s.disk.total);
    const meter = (p) => { const i = h("i", { class: level(p) }); i.style.width = p.toFixed(1) + "%"; return h("div", { class: "meter" }, i); };
    const inst = (name, val, p, sub) => h("div", { class: "inst" }, h("div", { class: "name" }, name), meter(p), h("div", { class: "val" }, val), h("div", { class: "sub" }, sub));
    const spark = h("canvas", { class: "spark" });
    const cs = checks(s);
    const checkCard = h("div", { class: "card flush" },
      h("div", { class: "card-head" }, h("h2", {}, "Needs attention"), cs.length ? h("span", { class: "badge warn" }, String(cs.length)) : null),
      h("div", { class: "checks" }, cs.length ? cs.map((c) => h("div", { class: "check " + c.lvl },
        h("span", { class: "sig" }), h("div", { class: "what" }, h("b", {}, c.t), h("span", {}, c.d)),
        c.to ? h("button", { class: "btn sm", onclick: () => go(c.to) }, c.b) : null))
        : h("div", { class: "check" }, h("span", { class: "sig" }), h("div", { class: "what" }, h("b", {}, "All clear"), h("span", {}, "Nothing needs your attention right now.")))));
    const short = [["/websites", "Add a website", "globe"], ["/apps", "Install WordPress", "apps"], ["/databases", "New database", "database"], ["/backups", "Back up now", "backup"], ["/terminal", "Open terminal", "terminal"]];
    body.replaceChildren(
      h("div", { class: "bridge" },
        h("div", { class: "card instruments" },
          inst("CPU", s.cpu_percent.toFixed(0) + "%", s.cpu_percent, `${s.cores} cores, load ${s.load.map((v) => v.toFixed(2)).join(" ")}`),
          inst("Memory", memP.toFixed(0) + "%", memP, `${fmtBytes(s.mem.used)} of ${fmtBytes(s.mem.total)}` + (s.mem.swap_total ? `, swap ${fmtBytes(s.mem.swap_used)}/${fmtBytes(s.mem.swap_total)}` : "")),
          inst("Disk", diskP.toFixed(0) + "%", diskP, `${fmtBytes(s.disk.used)} of ${fmtBytes(s.disk.total)} on /`),
          h("div", { class: "inst" }, h("div", { class: "name" }, "Network"), h("div", {}), h("div", { class: "val" }, "↓ " + fmtBytes(s.net.rx_rate) + "/s"), h("div", { class: "sub" }, `↑ ${fmtBytes(s.net.tx_rate)}/s. Total ↓ ${fmtBytes(s.net.rx_bytes)} ↑ ${fmtBytes(s.net.tx_bytes)}`)),
          h("div", { class: "inst chart" }, h("div", { class: "name" }, "CPU, last 2 minutes"), spark)),
        checkCard),
      h("div", { class: "shortcuts" }, short.map(([to, label, ic]) => h("a", { class: "shortcut", href: "#" + to }, icon(ic), label))),
      h("div", { class: "card" }, h("h2", {}, "Server"),
        h("dl", { class: "kv" },
          h("dt", {}, "Hostname"), h("dd", {}, s.hostname),
          h("dt", {}, "OS"), h("dd", {}, s.os),
          h("dt", {}, "Kernel"), h("dd", {}, `${s.kernel} (${s.arch})`),
          h("dt", {}, "Uptime"), h("dd", {}, fmtUptime(s.uptime_seconds)),
          h("dt", {}, "Panel"), h("dd", {}, state.user.version))));
    drawSpark(spark, s.cpu_history);
  }
  loadExtra().then(refresh);
  state.timer = setInterval(() => { refresh(); }, 3000);
  const slow = setInterval(loadExtra, 60000);
  state.onLeave = () => clearInterval(slow);
}

function drawSpark(c, data) {
  const dpr = window.devicePixelRatio || 1;
  const w = c.clientWidth, hgt = c.clientHeight;
  c.width = w * dpr; c.height = hgt * dpr;
  const g = c.getContext("2d"); g.scale(dpr, dpr);
  const css = getComputedStyle(document.documentElement);
  const accent = css.getPropertyValue("--teal").trim() || "#0b7a75", border = css.getPropertyValue("--line").trim();
  g.strokeStyle = border; g.lineWidth = 1;
  for (const y of [0.25, 0.5, 0.75]) { g.beginPath(); g.moveTo(0, hgt * y); g.lineTo(w, hgt * y); g.stroke(); }
  if (data.length < 2) return;
  const step = w / 59, x0 = w - (data.length - 1) * step;
  const pts = data.map((v, i) => [x0 + i * step, hgt - (v / 100) * (hgt - 4) - 2]);
  g.beginPath(); pts.forEach(([x, y], i) => (i ? g.lineTo(x, y) : g.moveTo(x, y)));
  g.strokeStyle = accent; g.lineWidth = 2; g.lineJoin = "round"; g.stroke();
  g.lineTo(pts[pts.length - 1][0], hgt); g.lineTo(pts[0][0], hgt); g.closePath();
  g.globalAlpha = 0.12; g.fillStyle = accent; g.fill(); g.globalAlpha = 1;
}

/* ---- files ---- */
const joinPath = (dir, name) => (dir === "/" ? "/" + name : dir + "/" + name);
const parentOf = (p) => p.replace(/\/[^/]+\/?$/, "") || "/";

function filesView(view) {
  const q = route().q;
  const cur = q.get("path") || "/";
  const edit = q.get("edit");
  if (edit) return editorView(view, edit);

  const notice = h("div");
  const say = (m, ok) => notice.replaceChildren(m ? h("div", { class: "msg " + (ok ? "ok" : "err") }, m) : "");
  const tableBox = h("div", { class: "card table-wrap" }, "Loading…");
  const crumbs = h("div", { class: "crumbs" });
  const nav = (p) => go("/files?path=" + encodeURIComponent(p));

  const upload = h("input", { type: "file", multiple: true, hidden: true, onchange: async () => {
    if (!upload.files.length) return;
    const fd = new FormData(); for (const f of upload.files) fd.append("file", f, f.name);
    say("Uploading…", true);
    try { await api.call("POST", "api/files/upload?dir=" + encodeURIComponent(cur), undefined, { body: fd }); say("Upload complete.", true); load(); }
    catch (x) { say(x.message); } finally { upload.value = ""; }
  } });

  view.append(
    h("div", { class: "page-head" }, h("h1", { class: "page-title" }, "File manager"),
      h("div", { class: "row" },
        h("button", { class: "btn", onclick: async () => {
          const n = await ui.prompt("New folder name"); if (!n) return;
          try { await api.post("api/files/mkdir", { path: joinPath(cur, n) }); load(); } catch (x) { say(x.message); }
        } }, "New folder"),
        h("button", { class: "btn", onclick: async () => {
          const n = await ui.prompt("New file name"); if (!n) return;
          try { await api.put("api/files/content", { path: joinPath(cur, n), content: "" }); go("/files?path=" + encodeURIComponent(cur) + "&edit=" + encodeURIComponent(joinPath(cur, n))); } catch (x) { say(x.message); }
        } }, "New file"),
        h("button", { class: "btn primary", onclick: () => upload.click() }, "Upload"), upload)),
    h("div", { class: "card" }, crumbs), notice, tableBox);

  async function load() {
    let d;
    try { d = await api.get("api/files?path=" + encodeURIComponent(cur)); }
    catch (x) { tableBox.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    const parts = d.path.split("/").filter(Boolean);
    crumbs.replaceChildren(h("a", { onclick: () => nav("/") }, "/"),
      ...parts.flatMap((seg, i) => [h("a", { onclick: () => nav("/" + parts.slice(0, i + 1).join("/")) }, seg), i < parts.length - 1 ? "/" : null]));
    const rows = d.entries.map((e) => h("tr", {},
      h("td", { class: "name" }, e.is_dir
        ? h("a", { class: "dir", onclick: () => nav(e.path) }, "📁 " + e.name + (e.symlink ? " →" : ""))
        : h("a", { onclick: () => go("/files?path=" + encodeURIComponent(cur) + "&edit=" + encodeURIComponent(e.path)) }, "📄 " + e.name + (e.symlink ? " →" : ""))),
      h("td", { class: "muted" }, e.is_dir ? "" : fmtBytes(e.size)),
      h("td", { class: "muted mono" }, e.mode),
      h("td", { class: "muted" }, new Date(e.mod_time).toLocaleString()),
      h("td", { class: "act" }, h("div", { class: "row" },
        e.is_dir ? null : h("a", { class: "btn sm", href: "api/files/download?path=" + encodeURIComponent(e.path), download: "" }, "Download"),
        h("button", { class: "btn sm", onclick: async () => {
          const n = await ui.prompt("Rename to", e.name); if (!n || n === e.name) return;
          try { await api.post("api/files/rename", { from: e.path, to: joinPath(cur, n) }); load(); } catch (x) { say(x.message); }
        } }, "Rename"),
        h("button", { class: "btn sm danger", onclick: async () => {
          if (!await ui.confirm(`Delete ${e.name}${e.is_dir ? " and everything inside it" : ""}? This cannot be undone.`)) return;
          try { await api.del("api/files?path=" + encodeURIComponent(e.path)); load(); } catch (x) { say(x.message); }
        } }, "Delete")))));
    tableBox.replaceChildren(h("table", { class: "files" },
      h("thead", {}, h("tr", {}, ["Name", "Size", "Mode", "Modified", ""].map((t) => h("th", {}, t)))),
      h("tbody", {},
        d.path !== "/" ? h("tr", {}, h("td", { class: "name", colspan: "5" }, h("a", { class: "dir", onclick: () => nav(parentOf(d.path)) }, "⬆ .."))) : null,
        rows, !rows.length ? h("tr", {}, h("td", { colspan: "5", class: "muted" }, "Empty folder")) : null)));
  }
  load();
}

async function editorView(view, file) {
  const dir = parentOf(file);
  const back = () => go("/files?path=" + encodeURIComponent(dir));
  const notice = h("div");
  const ta = h("textarea", { class: "editor", spellcheck: "false" });
  const save = h("button", { class: "btn primary", onclick: async () => {
    try { await api.put("api/files/content", { path: file, content: ta.value }); notice.replaceChildren(h("div", { class: "msg ok" }, "Saved.")); }
    catch (x) { notice.replaceChildren(h("div", { class: "msg err" }, x.message)); }
  } }, "Save");
  ta.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save.click(); }
    if (e.key === "Tab") { e.preventDefault(); const s = ta.selectionStart; ta.setRangeText("\t", s, ta.selectionEnd, "end"); }
  });
  view.append(h("div", { class: "page-head" }, h("h1", { class: "page-title mono" }, file),
    h("div", { class: "row" }, h("button", { class: "btn", onclick: back }, "Close"), save)), notice, ta);
  try { ta.value = (await api.get("api/files/content?path=" + encodeURIComponent(file))).content; }
  catch (x) { notice.replaceChildren(h("div", { class: "msg err" }, x.message)); ta.disabled = true; save.disabled = true; }
}

/* ---- websites ---- */
function websitesView(view) {
  const notice = h("div");
  const say = (m, ok, log) => notice.replaceChildren(
    ...[m ? h("div", { class: "msg " + (ok ? "ok" : "err") }, m) : null,
        log ? h("pre", { class: "log" }, log) : null].filter(Boolean));
  const setupBox = h("div");
  const formBox = h("div");
  const listBox = h("div", { class: "card table-wrap" }, "Loading…");
  view.append(ui.pageHead("Websites", "Domains served by Nginx, with SSL and one-click WordPress."), notice, setupBox, formBox, listBox);

  let status = null;

  async function loadStatus() {
    try { status = await api.get("api/sites/status"); } catch (x) { say(x.message); return; }
    if (status.nginx_installed && status.certbot_installed) { setupBox.replaceChildren(); return; }
    const php = h("input", { type: "checkbox" });
    const btn = h("button", { class: "btn primary", onclick: async () => {
      if (!await ui.confirm("Install Nginx and Certbot with apt now? This can take a few minutes.")) return;
      btn.disabled = true; say("Installing… please keep this page open.", true);
      try { const r = await api.post("api/sites/setup", { php: php.checked }); say("Installed.", true, r.log); await loadStatus(); drawForm(); }
      catch (x) { say(x.message); } finally { btn.disabled = false; }
    } }, "Install Nginx & Certbot");
    setupBox.replaceChildren(h("div", { class: "card setup" },
      h("h2", {}, "One-time setup"),
      h("p", { class: "muted" }, "Websites need Nginx, and SSL certificates need Certbot. They are not installed on this server yet."),
      h("label", { class: "row" }, php, "Also install PHP-FPM (for PHP sites)"), btn));
  }

  function drawForm() {
    const domain = h("input", { type: "text", placeholder: "example.com", autocomplete: "off" });
    const aliases = h("input", { type: "text", placeholder: "www.example.com, shop.example.com", autocomplete: "off" });
    const type = h("select", {},
      h("option", { value: "static" }, "Static website (HTML files)"),
      h("option", { value: "php" }, "PHP website"),
      h("option", { value: "proxy" }, "Reverse proxy (Node, Python, Docker app)"));
    const proxy = h("input", { type: "text", placeholder: "http://127.0.0.1:3000", autocomplete: "off" });
    const proxyField = h("label", { class: "field", hidden: true }, h("span", {}, "App address"), proxy);
    type.addEventListener("change", () => { proxyField.hidden = type.value !== "proxy"; });
    const btn = h("button", { class: "btn primary", type: "submit" }, "Add website");
    formBox.replaceChildren(h("div", { class: "card" }, h("h2", {}, "Add a website"),
      h("form", { onsubmit: async (e) => {
        e.preventDefault(); btn.disabled = true; say("");
        try {
          await api.post("api/sites", {
            domain: domain.value, type: type.value, proxy_url: proxy.value,
            aliases: aliases.value.split(",").map((x) => x.trim()).filter(Boolean),
          });
          domain.value = aliases.value = proxy.value = "";
          say("Website added. Point its DNS A record to this server, then issue SSL.", true);
          loadList();
        } catch (x) { say(x.message); } finally { btn.disabled = false; }
      } },
      h("div", { class: "grid2" },
        h("label", { class: "field" }, h("span", {}, "Domain"), domain),
        h("label", { class: "field" }, h("span", {}, "Extra names (optional, comma separated)"), aliases),
        h("label", { class: "field" }, h("span", {}, "Type"), type), proxyField),
      btn)));
  }

  const sslBadge = (s) => {
    if (!s.ssl) return h("span", { class: "badge" }, "No SSL");
    if (!s.cert) return h("span", { class: "badge ok" }, "SSL on");
    const d = s.cert.days_left;
    return h("span", { class: "badge " + (d < 10 ? "bad" : d < 25 ? "warn" : "ok") }, `SSL · ${d}d left`);
  };

  async function patch(s, body) {
    try { await api.put("api/sites/" + s.id, body); say(""); loadList(); } catch (x) { say(x.message); }
  }

  async function loadList() {
    let d;
    try { d = await api.get("api/sites"); } catch (x) { listBox.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    if (!d.sites.length) { listBox.replaceChildren(h("p", { class: "muted" }, "No websites yet. Add your first one above.")); return; }
    const rows = d.sites.map((s) => {
      const scheme = s.ssl ? "https" : "http";
      const typeName = { static: "Static", php: "PHP", proxy: "Proxy → " + s.proxy_url }[s.type] || s.type;
      return h("tr", {},
        h("td", { class: "name" },
          h("a", { href: scheme + "://" + s.domain, target: "_blank", rel: "noopener noreferrer" }, s.domain),
          (s.aliases || []).length ? h("div", { class: "muted small" }, s.aliases.join(", ")) : null,
          s.app === "wordpress" ? h("div", {}, h("span", { class: "badge ok" }, "WordPress"), h("a", { class: "small", href: scheme + "://" + s.domain + "/wp-admin/", target: "_blank", rel: "noopener noreferrer" }, "Admin")) : null),
        h("td", { class: "muted" }, typeName),
        h("td", {}, sslBadge(s), s.force_https ? h("span", { class: "badge" }, "HTTPS only") : null),
        h("td", {}, h("span", { class: "badge " + (s.enabled ? "ok" : "") }, s.enabled ? "Enabled" : "Disabled")),
        h("td", { class: "act" }, h("div", { class: "row" },
          s.app === "wordpress" ? h("button", { class: "btn sm primary", onclick: () => go("/wordpress/" + s.id) }, "Manage") : null,
          s.root ? h("button", { class: "btn sm", onclick: () => go("/files?path=" + encodeURIComponent(s.root)) }, "Files") : null,
          !s.ssl ? h("button", { class: "btn sm", onclick: async (e) => {
            const b = e.currentTarget; b.disabled = true; say("Requesting a certificate from Let's Encrypt…", true);
            try { const r = await api.post("api/sites/" + s.id + "/ssl"); say("SSL certificate installed.", true, r.log); loadList(); }
            catch (x) { say(x.message, false, x.log); } finally { b.disabled = false; }
          } }, "Get SSL") : h("button", { class: "btn sm", onclick: () => patch(s, { force_https: !s.force_https }) }, s.force_https ? "Allow HTTP" : "Force HTTPS"),
          s.wildcard_from ? h("button", { class: "btn sm", onclick: async (e) => {
            const b = e.currentTarget; b.disabled = true;
            try { await api.post("api/sites/" + s.id + "/ssl/use-wildcard"); say("Now using the *." + s.wildcard_from + " certificate.", true); loadList(); }
            catch (x) { say(x.message); } finally { b.disabled = false; }
          } }, "Use *." + s.wildcard_from + " cert") : null,
          h("button", { class: "btn sm", onclick: async (e) => {
            if (!await ui.confirm(`Get one certificate for ${s.domain} and *.${s.domain}? It is verified through your Cloudflare account (DNS tab must be connected). This can take a minute.`)) return;
            const b = e.currentTarget; b.disabled = true; say("Requesting a wildcard certificate, this takes about a minute…", true);
            try { const r = await api.post("api/sites/" + s.id + "/ssl/wildcard"); say("Wildcard certificate installed.", true, r.log); loadList(); }
            catch (x) { say(x.message, false, x.log); } finally { b.disabled = false; }
          } }, "Wildcard SSL"),
          s.panel ? h("span", { class: "badge ok" }, "Panel") : h("button", { class: "btn sm", onclick: () => patch(s, { enabled: !s.enabled }) }, s.enabled ? "Disable" : "Enable"),
          s.panel ? null : h("button", { class: "btn sm danger", onclick: async () => {
            if (!await ui.confirm(`Remove ${s.domain} from Nginx? Its files and certificate are kept on the server.`)) return;
            try { await api.del("api/sites/" + s.id); say(""); loadList(); } catch (x) { say(x.message); }
          } }, "Remove"))));
    });
    listBox.replaceChildren(h("table", { class: "files sites" },
      h("thead", {}, h("tr", {}, ["Website", "Type", "SSL", "Status", ""].map((t) => h("th", {}, t)))),
      h("tbody", {}, rows)));
  }

  loadStatus().then(() => { drawForm(); loadList(); });
}

/* ---- databases ---- */
function databasesView(view) {
  const notice = h("div");
  const say = (m, ok, log) => notice.replaceChildren(
    ...[m ? h("div", { class: "msg " + (ok ? "ok" : "err") }, m) : null, log ? h("pre", { class: "log" }, log) : null].filter(Boolean));
  const secretBox = h("div"), body = h("div");
  view.append(ui.pageHead("Databases", "MariaDB and PostgreSQL databases and their users."), notice, secretBox, body);
  const LABEL = { mysql: "MariaDB / MySQL", postgres: "PostgreSQL" };
  const human = (n) => n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : n < 1073741824 ? (n / 1048576).toFixed(1) + " MB" : (n / 1073741824).toFixed(2) + " GB";

  function showSecret(c) {
    const cs = c.engine === "mysql"
      ? `mysql://${c.user}:${c.password}@${c.host}:${c.port}/${c.name}`
      : `postgresql://${c.user}:${c.password}@${c.host}:${c.port}/${c.name}`;
    secretBox.replaceChildren(h("div", { class: "card setup" }, h("h2", {}, "Save these details now"),
      h("p", { class: "muted" }, "The password is shown only once and is not stored anywhere."),
      h("dl", { class: "kv" }, h("dt", {}, "Database"), h("dd", { class: "mono" }, c.name), h("dt", {}, "User"), h("dd", { class: "mono" }, c.user),
        h("dt", {}, "Password"), h("dd", { class: "mono" }, c.password), h("dt", {}, "Host"), h("dd", { class: "mono" }, c.host + ":" + c.port),
        h("dt", {}, "URL"), h("dd", { class: "mono" }, cs)),
      h("div", { class: "row" }, h("button", { class: "btn sm", onclick: () => secretBox.replaceChildren() }, "I have saved it"))));
  }

  async function load() {
    let d;
    try { d = await api.get("api/databases"); } catch (x) { body.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    body.replaceChildren(...d.engines.map((en) => {
      const card = h("div", { class: "card" }, h("h2", {}, LABEL[en.engine]));
      if (!en.installed) {
        const b = h("button", { class: "btn primary", onclick: async () => {
          b.disabled = true; say("Installing " + LABEL[en.engine] + ", this can take a few minutes…", true);
          try { const r = await api.post("api/databases/" + en.engine + "/install"); say("Installed.", true, r.log); load(); }
          catch (x) { say(x.message, false, x.log); } finally { b.disabled = false; }
        } }, "Install " + LABEL[en.engine]);
        card.append(h("p", { class: "muted" }, "Not installed."), b);
        return card;
      }
      if (!en.running) { card.append(h("div", { class: "msg err" }, en.error || "The server is installed but not answering.")); return card; }
      const name = h("input", { type: "text", placeholder: "new_database_name", autocomplete: "off", class: "mono" });
      card.append(h("form", { class: "row", onsubmit: async (e) => {
        e.preventDefault();
        try { const c = await api.post("api/databases/" + en.engine, { name: name.value }); say(""); showSecret(c); name.value = ""; load(); }
        catch (x) { say(x.message); }
      } }, h("div", { class: "grow" }, name), h("button", { class: "btn primary", type: "submit" }, "Create database + user")));
      card.append(en.databases.length ? h("div", { class: "table-wrap" }, h("table", { class: "files sites" },
        h("thead", {}, h("tr", {}, ["Database", "Size", ""].map((t) => h("th", {}, t)))),
        h("tbody", {}, en.databases.map((db) => h("tr", {},
          h("td", { class: "name mono" }, db.name), h("td", { class: "muted" }, human(db.bytes)),
          h("td", { class: "act" }, h("div", { class: "row" },
            h("button", { class: "btn sm", onclick: async () => {
              if (!await ui.confirm(`Set a new password for user "${db.name}"? Apps using the old one will stop working.`)) return;
              try { showSecret(await api.post("api/databases/" + en.engine + "/" + db.name + "/password")); } catch (x) { say(x.message); }
            } }, "New password"),
            h("button", { class: "btn sm danger", onclick: async () => {
              const typed = await ui.prompt(`This permanently deletes the database "${db.name}" and its user. Type the database name to confirm:`);
              if (typed !== db.name) return;
              try { await api.del("api/databases/" + en.engine + "/" + db.name); say("Database deleted.", true); load(); } catch (x) { say(x.message); }
            } }, "Delete")))))))) : h("p", { class: "muted" }, "No databases yet."));
      return card;
    }));
  }
  load();
}

/* ---- backups ---- */
function backupsView(view) {
  const notice = h("div");
  const say = (m, ok) => notice.replaceChildren(...(m ? [h("div", { class: "msg " + (ok ? "ok" : "err") }, m)] : []));
  const makeBox = h("div"), schedBox = h("div"), listBox = h("div", { class: "card table-wrap" }, "Loading…");
  view.append(ui.pageHead("Backups", "Snapshots of your sites and databases, on demand or on a schedule."), notice, makeBox, schedBox, listBox);
  const human = (n) => n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : n < 1073741824 ? (n / 1048576).toFixed(1) + " MB" : (n / 1073741824).toFixed(2) + " GB";
  let schedCmd = "";

  async function drawMake() {
    let sites = [], dbs = [];
    try { sites = (await api.get("api/sites")).sites.filter((x) => x.type !== "proxy"); } catch { /* none */ }
    try { for (const e of (await api.get("api/databases")).engines) for (const d of e.databases) dbs.push({ engine: e.engine, name: d.name }); } catch { /* none */ }
    const siteSel = h("select", {}, sites.length ? sites.map((x) => h("option", { value: x.id }, x.domain)) : [h("option", { value: "" }, "No sites with files")]);
    const dbSel = h("select", {}, dbs.length ? dbs.map((d) => h("option", { value: d.engine + "/" + d.name }, d.name + " (" + d.engine + ")")) : [h("option", { value: "" }, "No databases")]);
    const run = (btn, fn, okMsg) => async () => {
      btn.disabled = true; say("Working, a big site can take a while…", true);
      try { await fn(); say(okMsg, true); loadList(); } catch (x) { say(x.message); } finally { btn.disabled = false; }
    };
    const b1 = h("button", { class: "btn primary" }, "Back up site files");
    const b2 = h("button", { class: "btn primary" }, "Back up database");
    b1.onclick = run(b1, () => api.post("api/backups/site", { site_id: +siteSel.value }), "Site backup created.");
    b2.onclick = run(b2, () => { const [engine, name] = dbSel.value.split("/"); return api.post("api/backups/db", { engine, name }); }, "Database backup created.");
    b1.disabled = !sites.length; b2.disabled = !dbs.length;
    makeBox.replaceChildren(h("div", { class: "card" }, h("h2", {}, "Back up now"),
      h("div", { class: "grid2" }, h("div", {}, h("label", { class: "field" }, h("span", {}, "Website"), siteSel), b1),
        h("div", {}, h("label", { class: "field" }, h("span", {}, "Database"), dbSel), b2))));
  }

  async function drawSchedule() {
    let job = null;
    try { job = (await api.get("api/cron")).jobs.find((j) => j.command.includes("backup-all")); } catch { /* ignore */ }
    const b = h("button", { class: job ? "btn danger" : "btn primary" }, job ? "Turn off nightly backups" : "Turn on nightly backups");
    b.onclick = async () => {
      b.disabled = true;
      try {
        if (job) await api.del("api/cron/" + job.id);
        else await api.post("api/cron", { name: "Nightly backup", schedule: "30 2 * * *", command: schedCmd, enabled: true });
        drawSchedule();
      } catch (x) { say(x.message); b.disabled = false; }
    };
    schedBox.replaceChildren(h("div", { class: "card" }, h("h2", {}, "Nightly backups"),
      h("p", { class: "muted" }, job ? "Every night at 02:30 all sites and databases are backed up, keeping the newest 7 of each. You can see or change it under Cron jobs."
        : "Back up every site and database at 02:30 each night and keep the newest 7 of each."), b));
  }

  async function loadList() {
    let d;
    try { d = await api.get("api/backups"); } catch (x) { listBox.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    schedCmd = d.schedule_command;
    if (!d.backups.length) { listBox.replaceChildren(h("p", { class: "muted" }, "No backups yet.")); return; }
    listBox.replaceChildren(h("table", { class: "files sites" },
      h("thead", {}, h("tr", {}, ["What", "Type", "Created (UTC)", "Size", ""].map((t) => h("th", {}, t)))),
      h("tbody", {}, d.backups.map((b) => h("tr", {},
        h("td", { class: "name mono" }, b.target),
        h("td", {}, h("span", { class: "badge" }, b.kind === "site" ? "Files" : b.engine === "mysql" ? "MySQL" : "PostgreSQL")),
        h("td", { class: "muted" }, new Date(b.created).toISOString().replace("T", " ").slice(0, 16)),
        h("td", { class: "muted" }, human(b.bytes)),
        h("td", { class: "act" }, h("div", { class: "row" },
          h("a", { class: "btn sm", href: "api/backups/" + encodeURIComponent(b.name) + "/download", download: "" }, "Download"),
          h("button", { class: "btn sm", onclick: async (e) => {
            const msg = b.kind === "site"
              ? `Restore the files of ${b.target} from this backup? Files with the same name will be overwritten.`
              : `Restore database "${b.target}" from this backup? Its current contents will be replaced.`;
            if (!await ui.confirm(msg)) return;
            const btn = e.currentTarget; btn.disabled = true; say("Restoring…", true);
            try { await api.post("api/backups/" + encodeURIComponent(b.name) + "/restore"); say("Restored.", true); } catch (x) { say(x.message); } finally { btn.disabled = false; }
          } }, "Restore"),
          h("button", { class: "btn sm danger", onclick: async () => {
            if (!await ui.confirm("Delete this backup file?")) return;
            try { await api.del("api/backups/" + encodeURIComponent(b.name)); loadList(); } catch (x) { say(x.message); }
          } }, "Delete"))))))));
  }
  drawMake(); loadList().then(drawSchedule);
}

/* ---- one-click apps ---- */
function appsView(view) {
  const notice = h("div"), resultBox = h("div");
  view.append(ui.pageHead("One-click apps", "Install ready-made apps on a domain in a minute."), notice, resultBox);
  const dom = h("input", { type: "text", placeholder: "blog.example.com", autocomplete: "off", class: "mono" });
  const title = h("input", { type: "text", placeholder: "My new site", autocomplete: "off" });
  const user = h("input", { type: "text", autocomplete: "off" }); user.value = "siteadmin";
  const mail = h("input", { type: "text", placeholder: "you@example.com", autocomplete: "off" });
  const ssl = h("input", { type: "checkbox" }); ssl.checked = true;
  const btn = h("button", { class: "btn primary", type: "submit" }, "Install WordPress");
  const show = (r) => resultBox.replaceChildren(h("div", { class: "card setup" }, h("h2", {}, "WordPress is ready"),
    r.setup_in_browser
      ? h("p", {}, "The site is installed. Open it to finish the 5-minute setup (site title, admin user and password):")
      : h("p", { class: "muted" }, "Save the admin password now; it is shown only once and is not stored anywhere."),
    h("dl", { class: "kv" },
      h("dt", {}, "Site"), h("dd", {}, h("a", { href: r.url, target: "_blank", rel: "noopener" }, r.url)),
      h("dt", {}, r.setup_in_browser ? "Finish setup" : "Admin"), h("dd", {}, h("a", { href: r.admin_url, target: "_blank", rel: "noopener" }, r.admin_url)),
      ...(r.admin_password ? [h("dt", {}, "Username"), h("dd", { class: "mono" }, r.admin_user), h("dt", {}, "Password"), h("dd", { class: "mono" }, r.admin_password)] : [])),
    !r.ssl ? h("p", { class: "muted small" }, "It runs on HTTP for now. Once the domain points to this server, use Websites → Get SSL.") : null,
    r.log ? h("pre", { class: "log" }, r.log) : null));
  view.append(h("div", { class: "card" }, h("h2", {}, "WordPress"),
    h("p", { class: "muted" }, "Creates a new website, a database and a user, downloads and checks WordPress, and sets it up. The domain's A record should already point to this server (DNS tab) so HTTPS can be issued. Installing Nginx, PHP and MariaDB for the first time can take a few minutes."),
    h("form", { onsubmit: async (e) => {
      e.preventDefault(); btn.disabled = true; resultBox.replaceChildren();
      notice.replaceChildren(h("div", { class: "msg ok" }, "Installing… this can take several minutes. Keep this page open."));
      try { const r = await api.post("api/apps/wordpress", { domain: dom.value, title: title.value, admin_user: user.value, admin_email: mail.value, ssl: ssl.checked }); notice.replaceChildren(); show(r); }
      catch (x) { notice.replaceChildren(h("div", { class: "msg err" }, x.message), x.log ? h("pre", { class: "log" }, x.log) : null); }
      finally { btn.disabled = false; }
    } },
    h("div", { class: "grid2" },
      h("label", { class: "field" }, h("span", {}, "Domain (a new website)"), dom),
      h("label", { class: "field" }, h("span", {}, "Site title"), title),
      h("label", { class: "field" }, h("span", {}, "Admin username"), user),
      h("label", { class: "field" }, h("span", {}, "Admin email"), mail)),
    h("div", { class: "row" }, h("label", { class: "row" }, ssl, "Get a free HTTPS certificate"), btn))));
}

/* ---- dns (Cloudflare) ---- */
function dnsView(view) {
  const notice = h("div");
  const say = (m, ok) => notice.replaceChildren(...(m ? [h("div", { class: "msg " + (ok ? "ok" : "err") }, m)] : []));
  const body = h("div");
  view.append(ui.pageHead("DNS", "Manage DNS records for domains on your Cloudflare account."), notice, body);
  let zoneId = "", serverIp = "";

  async function load() {
    let st;
    try { st = await api.get("api/dns"); } catch (x) { body.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    serverIp = st.server_ip || "";
    if (!st.configured) {
      const tok = h("input", { type: "password", placeholder: "Cloudflare API token", autocomplete: "off" });
      const b = h("button", { class: "btn primary", type: "submit" }, "Verify and save");
      body.replaceChildren(h("div", { class: "card setup" }, h("h2", {}, "Connect Cloudflare"),
        h("p", { class: "muted" }, "Create a token at Cloudflare → My Profile → API Tokens, using the template “Edit zone DNS” (or permissions Zone:Read and DNS:Edit). The token is checked with Cloudflare, stored only on this server and never shown again."),
        h("form", { onsubmit: async (e) => {
          e.preventDefault(); b.disabled = true; say("Checking the token…", true);
          try { await api.put("api/dns/token", { token: tok.value }); say("Connected.", true); load(); } catch (x) { say(x.message); } finally { b.disabled = false; }
        } }, h("label", { class: "field" }, h("span", {}, "API token"), tok), b)));
      return;
    }
    let zones;
    try { zones = (await api.get("api/dns/zones")).zones; } catch (x) { say(x.message); zones = []; }
    if (!zones.some((z) => z.id === zoneId)) zoneId = zones.length ? zones[0].id : "";
    const sel = h("select", {}, zones.map((z) => h("option", { value: z.id }, z.name)));
    sel.value = zoneId;
    sel.addEventListener("change", () => { zoneId = sel.value; drawRecords(); });
    const disconnect = h("button", { class: "btn sm danger", onclick: async () => {
      if (!await ui.confirm("Remove the saved Cloudflare token from this server?")) return;
      try { await api.del("api/dns/token"); zoneId = ""; load(); } catch (x) { say(x.message); }
    } }, "Disconnect");
    const recBox = h("div");
    body.replaceChildren(h("div", { class: "card" }, h("div", { class: "row between" }, h("div", { class: "grow" }, h("label", { class: "field" }, h("span", {}, "Domain"), sel)), disconnect)), recBox);
    window.__dnsBox = recBox;
    if (zoneId) drawRecords(); else recBox.replaceChildren(h("p", { class: "muted" }, "No domains found for this token."));
  }

  async function drawRecords() {
    const box = window.__dnsBox; box.replaceChildren(h("div", { class: "card" }, "Loading…"));
    let recs;
    try { recs = (await api.get("api/dns/zones/" + zoneId + "/records")).records; } catch (x) { box.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    let editing = null;
    const formBox = h("div");
    const pName = h("input", { type: "text", placeholder: "www  or  @ for the bare domain", autocomplete: "off", class: "mono" });
    const pIp = h("input", { type: "text", placeholder: "Server IPv4", autocomplete: "off", class: "mono" }); pIp.value = serverIp;
    const pProxy = h("input", { type: "checkbox" });
    const pBtn = h("button", { class: "btn primary", type: "submit" }, "Point to this server");
    const point = h("div", { class: "card" }, h("h2", {}, "Point a name to this server"),
      h("form", { onsubmit: async (e) => {
        e.preventDefault(); pBtn.disabled = true;
        try { const r = await api.post("api/dns/zones/" + zoneId + "/point", { name: pName.value, ip: pIp.value, proxied: pProxy.checked }); say("A record " + r.result + ".", true); drawRecords(); }
        catch (x) { say(x.message); } finally { pBtn.disabled = false; }
      } }, h("div", { class: "grid2" }, h("label", { class: "field" }, h("span", {}, "Name"), pName), h("label", { class: "field" }, h("span", {}, "IP address"), pIp)),
      h("div", { class: "row" }, h("label", { class: "row" }, pProxy, "Proxied through Cloudflare"), pBtn)));

    function drawForm() {
      const type = h("select", {}, ["A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "CAA"].map((t) => h("option", { value: t }, t)));
      const name = h("input", { type: "text", autocomplete: "off", class: "mono" });
      const content = h("input", { type: "text", autocomplete: "off", class: "mono" });
      const prio = h("input", { type: "text", autocomplete: "off", placeholder: "MX only" });
      const ttl = h("input", { type: "text", autocomplete: "off", placeholder: "1 = Auto" });
      const prox = h("input", { type: "checkbox" });
      if (editing) { type.value = editing.type; name.value = editing.name; content.value = editing.content; prio.value = editing.priority ?? ""; ttl.value = editing.ttl; prox.checked = editing.proxied; }
      const btn = h("button", { class: "btn primary", type: "submit" }, editing ? "Save record" : "Add record");
      formBox.replaceChildren(h("div", { class: "card" }, h("h2", {}, editing ? "Edit record" : "Add a record"),
        h("form", { onsubmit: async (e) => {
          e.preventDefault(); btn.disabled = true;
          const rec = { type: type.value, name: name.value, content: content.value, ttl: +ttl.value || 1, proxied: prox.checked };
          if (prio.value !== "") rec.priority = +prio.value;
          try {
            if (editing) await api.put("api/dns/zones/" + zoneId + "/records/" + editing.id, rec); else await api.post("api/dns/zones/" + zoneId + "/records", rec);
            say(editing ? "Record updated." : "Record added.", true); drawRecords();
          } catch (x) { say(x.message); } finally { btn.disabled = false; }
        } }, h("div", { class: "grid2" },
          h("label", { class: "field" }, h("span", {}, "Type"), type), h("label", { class: "field" }, h("span", {}, "Name"), name),
          h("label", { class: "field" }, h("span", {}, "Content"), content), h("label", { class: "field" }, h("span", {}, "Priority"), prio),
          h("label", { class: "field" }, h("span", {}, "TTL (seconds)"), ttl)),
        h("div", { class: "row" }, h("label", { class: "row" }, prox, "Proxied"), btn,
          editing ? h("button", { class: "btn", type: "button", onclick: () => { editing = null; drawForm(); } }, "Cancel") : null))));
    }
    drawForm();
    const table = h("div", { class: "card table-wrap" }, recs.length ? h("table", { class: "files sites" },
      h("thead", {}, h("tr", {}, ["Type", "Name", "Content", "TTL", ""].map((t) => h("th", {}, t)))),
      h("tbody", {}, recs.map((r) => h("tr", {},
        h("td", {}, h("span", { class: "badge" }, r.type)), h("td", { class: "mono name" }, r.name),
        h("td", { class: "mono muted" }, (r.priority != null ? r.priority + " " : "") + r.content + (r.proxied ? "  (proxied)" : "")),
        h("td", { class: "muted" }, r.ttl === 1 ? "Auto" : r.ttl),
        h("td", { class: "act" }, h("div", { class: "row" },
          h("button", { class: "btn sm", onclick: () => { editing = r; drawForm(); window.scrollTo(0, 0); } }, "Edit"),
          h("button", { class: "btn sm danger", onclick: async () => {
            if (!await ui.confirm(`Delete the ${r.type} record ${r.name}?`)) return;
            try { await api.del("api/dns/zones/" + zoneId + "/records/" + r.id); drawRecords(); } catch (x) { say(x.message); }
          } }, "Delete"))))))) : h("p", { class: "muted" }, "No records."));
    box.replaceChildren(point, formBox, table);
  }
  load();
}

/* ---- cron ---- */
const CRON_PRESETS = [
  ["Every minute", "* * * * *"], ["Every 5 minutes", "*/5 * * * *"], ["Every hour", "0 * * * *"],
  ["Every day at 03:00", "0 3 * * *"], ["Every Sunday at 04:00", "0 4 * * 0"], ["First of the month, 05:00", "0 5 1 * *"],
  ["At server start", "@reboot"], ["Custom…", ""],
];

function cronView(view) {
  const notice = h("div");
  const say = (m, ok, log) => notice.replaceChildren(
    ...[m ? h("div", { class: "msg " + (ok ? "ok" : "err") }, m) : null, log ? h("pre", { class: "log" }, log) : null].filter(Boolean));
  const formBox = h("div"), listBox = h("div", { class: "card table-wrap" }, "Loading…");
  view.append(ui.pageHead("Cron jobs", "Run commands on a schedule."), notice, formBox, listBox);
  let editing = null;

  function drawForm() {
    const name = h("input", { type: "text", placeholder: "Nightly backup", autocomplete: "off" });
    const sched = h("input", { type: "text", placeholder: "30 2 * * *", autocomplete: "off", class: "mono" });
    const preset = h("select", {}, CRON_PRESETS.map(([l, v], i) => h("option", { value: String(i) }, l)));
    const cmd = h("input", { type: "text", placeholder: "/usr/bin/php /var/www/example.com/public_html/cron.php", autocomplete: "off", class: "mono" });
    const on = h("input", { type: "checkbox" }); on.checked = true;
    if (editing) { name.value = editing.name; sched.value = editing.schedule; cmd.value = editing.command; on.checked = editing.enabled; preset.value = String(CRON_PRESETS.length - 1); }
    else { preset.value = "3"; sched.value = CRON_PRESETS[3][1]; }
    preset.addEventListener("change", () => { const v = CRON_PRESETS[+preset.value][1]; if (v) sched.value = v; else sched.focus(); });
    const btn = h("button", { class: "btn primary", type: "submit" }, editing ? "Save changes" : "Add job");
    formBox.replaceChildren(h("div", { class: "card" }, h("h2", {}, editing ? "Edit job" : "Add a job"),
      h("form", { onsubmit: async (e) => {
        e.preventDefault(); btn.disabled = true; say("");
        const body = { name: name.value, schedule: sched.value, command: cmd.value, enabled: on.checked };
        try {
          if (editing) await api.put("api/cron/" + editing.id, body); else await api.post("api/cron", body);
          say(editing ? "Job updated." : "Job added.", true); editing = null; drawForm(); loadList();
        } catch (x) { say(x.message); } finally { btn.disabled = false; }
      } },
      h("div", { class: "grid2" },
        h("label", { class: "field" }, h("span", {}, "Name"), name),
        h("label", { class: "field" }, h("span", {}, "When"), preset),
        h("label", { class: "field" }, h("span", {}, "Schedule (minute hour day month weekday)"), sched)),
      h("label", { class: "field" }, h("span", {}, "Command (runs as root)"), cmd),
      h("label", { class: "row" }, on, "Enabled"),
      h("div", { class: "row" }, btn, editing ? h("button", { class: "btn", type: "button", onclick: () => { editing = null; drawForm(); } }, "Cancel") : null))));
  }

  async function loadList() {
    let d;
    try { d = await api.get("api/cron"); } catch (x) { listBox.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    if (!d.jobs.length) { listBox.replaceChildren(h("p", { class: "muted" }, "No jobs yet.")); return; }
    listBox.replaceChildren(h("table", { class: "files sites" },
      h("thead", {}, h("tr", {}, ["Name", "Schedule", "Command", "Status", ""].map((t) => h("th", {}, t)))),
      h("tbody", {}, d.jobs.map((j) => h("tr", {},
        h("td", { class: "name" }, j.name),
        h("td", { class: "mono" }, j.schedule),
        h("td", { class: "mono muted" }, j.command),
        h("td", {}, h("span", { class: "badge " + (j.enabled ? "ok" : "") }, j.enabled ? "Enabled" : "Paused")),
        h("td", { class: "act" }, h("div", { class: "row" },
          h("button", { class: "btn sm", onclick: async (e) => {
            const b = e.currentTarget; b.disabled = true; say("Running…", true);
            try { const r = await api.post("api/cron/" + j.id + "/run"); say(r.ok ? "Finished." : "Finished with an error: " + r.error, r.ok, r.output || "(no output)"); }
            catch (x) { say(x.message); } finally { b.disabled = false; }
          } }, "Run now"),
          h("button", { class: "btn sm", onclick: () => { editing = j; drawForm(); window.scrollTo(0, 0); } }, "Edit"),
          h("button", { class: "btn sm", onclick: async () => {
            try { await api.put("api/cron/" + j.id, { name: j.name, schedule: j.schedule, command: j.command, enabled: !j.enabled }); loadList(); } catch (x) { say(x.message); }
          } }, j.enabled ? "Pause" : "Resume"),
          h("button", { class: "btn sm danger", onclick: async () => {
            if (!await ui.confirm(`Delete the job "${j.name}"?`)) return;
            try { await api.del("api/cron/" + j.id); loadList(); } catch (x) { say(x.message); }
          } }, "Delete")))))))); 
  }
  drawForm(); loadList();
}

/* ---- firewall ---- */
function firewallView(view) {
  const notice = h("div");
  const say = (m, ok, log) => notice.replaceChildren(
    ...[m ? h("div", { class: "msg " + (ok ? "ok" : "err") }, m) : null, log ? h("pre", { class: "log" }, log) : null].filter(Boolean));
  const body = h("div");
  view.append(ui.pageHead("Firewall", "Choose which ports are open to the internet."), notice, body);

  async function load() {
    let st;
    try { st = await api.get("api/firewall"); } catch (x) { body.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    if (!st.installed) {
      const btn = h("button", { class: "btn primary", onclick: async () => {
        btn.disabled = true; say("Installing ufw…", true);
        try { const r = await api.post("api/firewall/install"); say("ufw installed.", true, r.log); load(); }
        catch (x) { say(x.message, false, x.log); } finally { btn.disabled = false; }
      } }, "Install firewall (ufw)");
      body.replaceChildren(h("div", { class: "card setup" }, h("h2", {}, "Firewall not installed"),
        h("p", { class: "muted" }, "ufw is the standard simple firewall for Ubuntu and Debian. Installing it does not turn it on."), btn));
      return;
    }
    const toggle = h("button", { class: st.active ? "btn danger" : "btn primary", onclick: async () => {
      if (st.active && !await ui.confirm("Turn the firewall off? All ports will be reachable from the internet.")) return;
      if (!st.active && !await ui.confirm(`Turn the firewall on? Ports ${st.protected_ports.join(", ")} (SSH and this panel) will be allowed first so you stay connected. Everything else incoming is blocked unless you add a rule.`)) return;
      toggle.disabled = true;
      try { if (st.active) await api.post("api/firewall/disable"); else { const r = await api.post("api/firewall/enable"); say("Firewall enabled.", true, r.log); } load(); }
      catch (x) { say(x.message, false, x.log); } finally { toggle.disabled = false; }
    } }, st.active ? "Turn off" : "Turn on");

    const action = h("select", {}, ["allow", "deny", "limit", "reject"].map((a) => h("option", { value: a }, a[0].toUpperCase() + a.slice(1))));
    const port = h("input", { type: "text", placeholder: "443 or 8000:8100", autocomplete: "off" });
    const proto = h("select", {}, ["tcp", "udp", "any"].map((a) => h("option", { value: a }, a.toUpperCase())));
    const src = h("input", { type: "text", placeholder: "Anywhere, or 203.0.113.5 / 10.0.0.0/8", autocomplete: "off" });
    const add = async (rule, quiet) => { try { await api.post("api/firewall/rules", rule); if (!quiet) say("Rule added.", true); } catch (x) { say(x.message, false, x.log); throw x; } };
    const quick = (label, rules) => h("button", { class: "btn sm", type: "button", onclick: async () => {
      try { for (const r of rules) await add(r, true); say(label + " allowed.", true); load(); } catch { /* shown */ }
    } }, label);

    body.replaceChildren(
      h("div", { class: "card" }, h("div", { class: "row between" },
        h("div", {}, h("span", { class: "badge " + (st.active ? "ok" : "warn") }, st.active ? "Active" : "Inactive"),
          h("span", { class: "muted" }, ` Always kept open: ${st.protected_ports.join(", ")} (SSH and this panel)`)), toggle)),
      h("div", { class: "card" }, h("h2", {}, "Add a rule"),
        h("form", { onsubmit: async (e) => { e.preventDefault(); try { await add({ action: action.value, port: port.value, proto: proto.value, source: src.value }); port.value = src.value = ""; load(); } catch { /* shown */ } } },
          h("div", { class: "grid2" },
            h("label", { class: "field" }, h("span", {}, "Action"), action),
            h("label", { class: "field" }, h("span", {}, "Port"), port),
            h("label", { class: "field" }, h("span", {}, "Protocol"), proto),
            h("label", { class: "field" }, h("span", {}, "From (optional)"), src)),
          h("div", { class: "row" }, h("button", { class: "btn primary", type: "submit" }, "Add rule"),
            h("span", { class: "muted" }, "Quick:"),
            quick("Web (80, 443)", [{ action: "allow", port: "80", proto: "tcp" }, { action: "allow", port: "443", proto: "tcp" }]),
            quick("SSH (22)", [{ action: "allow", port: "22", proto: "tcp" }])))),
      h("div", { class: "card table-wrap" }, st.rules.length ? h("table", { class: "files sites" },
        h("thead", {}, h("tr", {}, ["#", "To", "Action", "From", ""].map((t) => h("th", {}, t)))),
        h("tbody", {}, st.rules.map((r) => h("tr", {},
          h("td", { class: "muted" }, r.number), h("td", { class: "mono" }, r.to),
          h("td", {}, h("span", { class: "badge " + (r.action === "ALLOW" ? "ok" : r.action === "LIMIT" ? "warn" : "bad") }, r.action)),
          h("td", { class: "mono muted" }, r.from),
          h("td", { class: "act" }, h("button", { class: "btn sm danger", onclick: async () => {
            if (!await ui.confirm(`Delete rule ${r.number} (${r.to})?`)) return;
            try { await api.del("api/firewall/rules/" + r.number + "?to=" + encodeURIComponent(r.to)); load(); } catch (x) { say(x.message); load(); }
          } }, "Delete")))))) : h("p", { class: "muted" }, "No rules yet.")));
  }
  load();
}

/* ---- terminal ---- */
function terminalView(view) {
  const status = h("span", { class: "muted" }, "connecting…");
  const host = h("div", { class: "term", tabindex: "0", spellcheck: "false" });
  let sock = null, term = null;

  function connect() {
    if (sock) { sock.onclose = null; try { sock.close(); } catch { /* already closed */ } }
    host.replaceChildren();
    const t = new WebTerminal(host, (bytes) => { if (sock && sock.readyState === 1) sock.send(bytes); });
    term = t;
    const url = new URL("api/terminal", location.href);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const s = new WebSocket(url);
    sock = s;
    s.binaryType = "arraybuffer";
    t.onResize = (cols, rows) => { if (s.readyState === 1) s.send(JSON.stringify({ type: "resize", cols, rows })); };
    s.onopen = () => { status.textContent = "connected"; t.fit(); t.onResize(t.cols, t.rows); host.focus(); };
    s.onmessage = (ev) => {
      if (typeof ev.data !== "string") { t.write(new Uint8Array(ev.data)); return; }
      try {
        const m = JSON.parse(ev.data);
        if (m.type === "exit") t.notice("[shell exited — press Reconnect for a new one]");
        else if (m.type === "error") t.notice("[" + m.message + "]");
      } catch { /* ignore malformed control message */ }
    };
    s.onclose = () => { status.textContent = "disconnected"; };
    s.onerror = () => { status.textContent = "connection error"; };
  }

  view.append(
    h("div", { class: "page-head" }, h("h1", { class: "page-title" }, "Terminal"),
      h("div", { class: "row" }, status, h("button", { class: "btn sm", onclick: connect }, "Reconnect"))),
    host,
    h("p", { class: "muted" }, "Runs as the panel's user (root). Full-screen programs like vim, nano and htop are not supported yet."));

  const ro = new ResizeObserver(() => { if (term) term.fit(); });
  ro.observe(host);
  state.onLeave = () => { ro.disconnect(); if (sock) { sock.onclose = null; sock.close(); } };
  connect();
}

/* ---- settings ---- */
function settingsView(view) {
  const pwMsg = h("div"), tfMsg = h("div");
  const say = (box, m, ok) => box.replaceChildren(h("div", { class: "msg " + (ok ? "ok" : "err") }, m));

  const cur = h("input", { type: "password", autocomplete: "current-password" });
  const nw = h("input", { type: "password", autocomplete: "new-password" });
  const pwCard = h("div", { class: "card" }, h("h2", {}, "Change password"), pwMsg,
    h("form", { onsubmit: async (e) => {
      e.preventDefault();
      try { await api.post("api/password", { current: cur.value, new: nw.value }); cur.value = nw.value = ""; say(pwMsg, "Password changed. Other devices were signed out.", true); }
      catch (x) { say(pwMsg, x.message); }
    } },
    h("label", { class: "field" }, h("span", {}, "Current password"), cur),
    h("label", { class: "field" }, h("span", {}, "New password (10+ characters)"), nw),
    h("button", { class: "btn primary", type: "submit" }, "Update password")));

  const acmeMsg = h("div");
  const email = h("input", { type: "text", placeholder: "you@example.com", autocomplete: "email" });
  api.get("api/settings").then((x) => { email.value = x.acme_email || ""; }).catch(() => {});
  const acmeCard = h("div", { class: "card" }, h("h2", {}, "Let's Encrypt"), acmeMsg,
    h("p", { class: "muted" }, "Used when requesting free SSL certificates. Let's Encrypt emails this address before a certificate expires."),
    h("form", { onsubmit: async (e) => {
      e.preventDefault();
      try { await api.put("api/settings", { acme_email: email.value }); say(acmeMsg, "Saved.", true); }
      catch (x) { say(acmeMsg, x.message); }
    } },
    h("label", { class: "field" }, h("span", {}, "Contact email"), email),
    h("button", { class: "btn primary", type: "submit" }, "Save")));

  const tfBody = h("div");
  const tfCard = h("div", { class: "card" }, h("h2", {}, "Two-factor authentication"), tfMsg, tfBody);
  function drawTwoFactor() {
    if (state.user.totp_enabled) {
      const pw = h("input", { type: "password", autocomplete: "current-password" });
      tfBody.replaceChildren(h("p", { class: "muted" }, "2FA is on. Sign-in needs a code from your authenticator app."),
        h("label", { class: "field" }, h("span", {}, "Confirm password to turn it off"), pw),
        h("button", { class: "btn danger", onclick: async () => {
          try { await api.post("api/2fa/disable", { password: pw.value }); state.user.totp_enabled = false; say(tfMsg, "2FA disabled.", true); drawTwoFactor(); }
          catch (x) { say(tfMsg, x.message); }
        } }, "Disable 2FA"));
    } else {
      tfBody.replaceChildren(h("p", { class: "muted" }, "Add a second step to sign-in using any authenticator app (Google Authenticator, Aegis, 1Password…)."),
        h("button", { class: "btn primary", onclick: async () => {
          try {
            const s = await api.post("api/2fa/setup");
            const code = h("input", { type: "text", inputmode: "numeric", maxlength: "6", autocomplete: "one-time-code" });
            tfBody.replaceChildren(
              h("p", {}, "In your authenticator app choose “enter a setup key” and add this key:"),
              h("p", { class: "mono" }, s.secret),
              h("p", { class: "muted" }, "Or open this link on your phone: "), h("p", { class: "mono muted" }, s.uri),
              h("label", { class: "field" }, h("span", {}, "Enter the 6-digit code to confirm"), code),
              h("button", { class: "btn primary", onclick: async () => {
                try { await api.post("api/2fa/enable", { code: code.value }); state.user.totp_enabled = true; say(tfMsg, "2FA enabled.", true); drawTwoFactor(); }
                catch (x) { say(tfMsg, x.message); }
              } }, "Enable 2FA"));
          } catch (x) { say(tfMsg, x.message); }
        } }, "Set up 2FA"));
    }
  }
  drawTwoFactor();
  const upMsg = h("div"), upBody = h("div");
  async function waitForRestart(oldVersion) {
    say(upMsg, "Restarting the panel…", true);
    for (let i = 0; i < 40; i++) {
      await new Promise((r) => setTimeout(r, 1500));
      try { const r = await (await fetch("api/ping", { cache: "no-store" })).json(); if (r.version && r.version !== oldVersion) { location.reload(); return; } } catch { /* still restarting */ }
    }
    say(upMsg, "The panel did not come back with the new version yet. Wait a little and reload the page.");
  }
  async function drawUpdate(force) {
    upBody.replaceChildren(h("p", { class: "muted" }, "Checking…"));
    let u;
    try { u = await api.get("api/update" + (force ? "?force=1" : "")); } catch (x) {
      upBody.replaceChildren(h("p", { class: "muted" }, "Could not check for updates: " + x.message),
        h("button", { class: "btn sm", onclick: () => drawUpdate(true) }, "Try again")); return;
    }
    state.update = u.available;
    const pw = h("input", { type: "password", autocomplete: "current-password" });
    const kids = [h("dl", { class: "kv" }, h("dt", {}, "Installed"), h("dd", { class: "mono" }, u.current), h("dt", {}, "Latest"), h("dd", { class: "mono" }, u.latest))];
    if (!u.can_update) kids.push(h("p", { class: "muted" }, "This is a development build, so it cannot compare versions. Install a release (the installer below) to enable one-click updates."));
    else if (u.available) {
      if (u.notes) kids.push(h("pre", { class: "log" }, u.notes));
      if (u.can_update) kids.push(h("label", { class: "field" }, h("span", {}, "Confirm password to update"), pw),
        h("button", { class: "btn primary", onclick: async (e) => {
          const btn = e.currentTarget; btn.disabled = true; say(upMsg, "Downloading and verifying the update…", true);
          try { const r = await api.post("api/update/apply", { password: pw.value }); waitForRestart(u.current); }
          catch (x) { say(upMsg, x.message); btn.disabled = false; }
        } }, "Update to " + u.latest));
    } else kids.push(h("p", { class: "muted" }, "You are on the latest version."));
    kids.push(h("p", { class: "muted small" }, "Or from a terminal: ", h("span", { class: "mono" }, "appanel update"), "  ·  or re-run the installer: ", h("span", { class: "mono" }, u.one_liner)),
      h("button", { class: "btn sm", onclick: () => drawUpdate(true) }, "Check again"));
    upBody.replaceChildren(...kids);
  }
  drawUpdate(false);
  const upCard = h("div", { class: "card" }, h("h2", {}, "Updates"), upMsg, upBody);
  const domMsg = h("div"), pathMsg = h("div");
  const domBody = h("div"), pathBody = h("div");
  const done = (box, r, what) => box.replaceChildren(h("div", { class: "msg ok" }, what + " The panel is restarting; open the new address in about 5 seconds:"),
    h("p", {}, h("a", { class: "mono", href: r.url }, r.url)), r.log ? h("pre", { class: "log" }, r.log) : null);
  function drawDomain(st) {
    const pw = h("input", { type: "password", autocomplete: "current-password" });
    if (st.panel_domain) {
      domBody.replaceChildren(h("p", {}, "The panel is served at ", h("span", { class: "mono" }, "https://" + st.panel_domain + st.login_path + "/"),
        ". The server's IP address does not open the panel."),
        h("label", { class: "field" }, h("span", {}, "Confirm password to remove the domain"), pw),
        h("button", { class: "btn danger", onclick: async (e) => {
          if (!await ui.confirm("Remove the panel domain? The panel will go back to its IP address and port.")) return;
          const btn = e.currentTarget; btn.disabled = true;
          try { done(domMsg, await api.call("DELETE", "api/panel/domain", { password: pw.value }), "Domain removed."); domBody.replaceChildren(); }
          catch (x) { say(domMsg, x.message); btn.disabled = false; }
        } }, "Remove domain"));
    } else {
      const dom = h("input", { type: "text", placeholder: "panel.example.com", autocomplete: "off", class: "mono" });
      domBody.replaceChildren(h("p", { class: "muted" }, "Open the panel at your own domain with a real HTTPS certificate. Once attached, the server's IP address will no longer open the panel at all. Point the domain's A record to this server first (see the DNS tab), and set the Let's Encrypt email above."),
        h("label", { class: "field" }, h("span", {}, "Domain"), dom),
        h("label", { class: "field" }, h("span", {}, "Confirm password"), pw),
        h("button", { class: "btn primary", onclick: async (e) => {
          const btn = e.currentTarget; btn.disabled = true; say(domMsg, "Installing Nginx if needed and requesting the certificate, this can take a minute…", true);
          try { done(domMsg, await api.post("api/panel/domain", { domain: dom.value, password: pw.value }), "Domain attached."); domBody.replaceChildren(); }
          catch (x) { domMsg.replaceChildren(h("div", { class: "msg err" }, x.message), x.log ? h("pre", { class: "log" }, x.log) : null); btn.disabled = false; }
        } }, "Attach domain"));
    }
  }
  function drawPath(st) {
    const path = h("input", { type: "text", autocomplete: "off", class: "mono" }); path.value = st.login_path;
    const pw = h("input", { type: "password", autocomplete: "current-password" });
    const send = async (e, body) => {
      const btn = e.currentTarget; btn.disabled = true;
      try { done(pathMsg, await api.post("api/panel/path", { ...body, password: pw.value }), "Login URL changed."); pathBody.replaceChildren(); }
      catch (x) { say(pathMsg, x.message); btn.disabled = false; }
    };
    pathBody.replaceChildren(h("p", { class: "muted" }, "The panel only exists under this secret path; every other URL is a blank 404. Use letters, digits, - or _ (8–64 characters)."),
      h("label", { class: "field" }, h("span", {}, "Login path"), path),
      h("label", { class: "field" }, h("span", {}, "Confirm password"), pw),
      h("div", { class: "row" }, h("button", { class: "btn primary", onclick: (e) => send(e, { path: path.value }) }, "Change login URL"),
        h("button", { class: "btn", onclick: (e) => send(e, { random: true }) }, "Generate a random one")));
  }
  api.get("api/settings").then((x) => { drawDomain(x); drawPath(x); }).catch((x) => { say(domMsg, x.message); });
  const domCard = h("div", { class: "card" }, h("h2", {}, "Panel domain"), domMsg, domBody);
  const pathCard = h("div", { class: "card" }, h("h2", {}, "Login URL"), pathMsg, pathBody);
  view.append(ui.pageHead("Settings", "Updates, panel address, security and certificates."), h("div", { class: "grid" }, upCard, domCard, pathCard, acmeCard, pwCard, tfCard));
}

/* ---- boot ---- */
async function boot() {
  try { state.user = await api.get("api/me"); } catch { state.user = null; }
  if (state.user) api.get("api/update").then((u) => { if (u.available && !state.update) { state.update = true; render(); } }).catch(() => {});
  if (state.user && !location.hash) location.hash = "/dashboard";
  render();
}
applyTheme();
boot();
