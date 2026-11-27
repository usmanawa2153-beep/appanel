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

const state = { user: null, ticket: null, timer: null };

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "value") el.value = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

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
    card.replaceChildren(h("img", { class: "logo", src: "favicon.svg", alt: "" }), h("h1", {}, "Sign in to Appanel"), err, form);
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
    card.replaceChildren(h("img", { class: "logo", src: "favicon.svg", alt: "" }), h("h1", {}, "Two-factor check"), err, form);
  }
  passwordStep();
  return h("div", { class: "auth" }, card);
}

/* ---- shell ---- */
function shell() {
  const { path } = route();
  const link = (to, label) => h("a", { class: path.startsWith(to) ? "on" : "", onclick: () => go(to) }, label);
  const view = h("main", { class: "main" });
  const side = h("aside", { class: "side" },
    h("div", { class: "brand" }, h("img", { src: "favicon.svg", alt: "" }), "Appanel"),
    h("nav", { class: "nav" }, link("/dashboard", "Dashboard"), link("/websites", "Websites"), link("/files", "File Manager"), link("/terminal", "Terminal"), link("/dns", "DNS"), link("/databases", "Databases"), link("/backups", "Backups"), link("/cron", "Cron jobs"), link("/firewall", "Firewall"), link("/settings", "Settings")),
    h("div", { class: "spacer" }),
    h("div", { class: "muted" }, `Signed in as ${state.user.username}`),
    h("div", { class: "row" },
      h("button", { class: "btn sm", onclick: toggleTheme }, "Theme"),
      h("button", { class: "btn sm", onclick: async () => { try { await api.post("api/logout"); } catch { /* already out */ } state.user = null; render(); } }, "Sign out")));
  if (path.startsWith("/files")) filesView(view);
  else if (path.startsWith("/websites")) websitesView(view);
  else if (path.startsWith("/terminal")) terminalView(view);
  else if (path.startsWith("/dns")) dnsView(view);
  else if (path.startsWith("/databases")) databasesView(view);
  else if (path.startsWith("/backups")) backupsView(view);
  else if (path.startsWith("/cron")) cronView(view);
  else if (path.startsWith("/firewall")) firewallView(view);
  else if (path.startsWith("/settings")) settingsView(view);
  else dashboardView(view);
  return h("div", { class: "shell" }, side, view);
}

/* ---- dashboard ---- */
function dashboardView(view) {
  const body = h("div", { class: "grid" });
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Dashboard")), body);

  async function refresh() {
    let s;
    try { s = await api.get("api/system"); } catch (x) { body.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
    const memP = pct(s.mem.used, s.mem.total), diskP = pct(s.disk.used, s.disk.total);
    const stat = (label, value, p, sub) => h("div", { class: "card stat" },
      h("div", { class: "label" }, label), h("div", { class: "value" }, value),
      p == null ? null : h("div", { class: "bar" }, (() => { const i = h("i", { class: level(p) }); i.style.width = p.toFixed(1) + "%"; return i; })()),
      h("div", { class: "sub" }, sub));
    const spark = h("canvas", { class: "spark" });
    body.replaceChildren(
      stat("CPU", s.cpu_percent.toFixed(1) + "%", s.cpu_percent, `${s.cores} cores · load ${s.load.map((v) => v.toFixed(2)).join(" ")}`),
      stat("Memory", memP.toFixed(0) + "%", memP, `${fmtBytes(s.mem.used)} of ${fmtBytes(s.mem.total)}` + (s.mem.swap_total ? ` · swap ${fmtBytes(s.mem.swap_used)}/${fmtBytes(s.mem.swap_total)}` : "")),
      stat("Disk /", diskP.toFixed(0) + "%", diskP, `${fmtBytes(s.disk.used)} of ${fmtBytes(s.disk.total)}`),
      stat("Network", "↓ " + fmtBytes(s.net.rx_rate) + "/s", null, `↑ ${fmtBytes(s.net.tx_rate)}/s · total ↓ ${fmtBytes(s.net.rx_bytes)} ↑ ${fmtBytes(s.net.tx_bytes)}`),
      h("div", { class: "card wide" }, h("h2", {}, "CPU history (last 2 minutes)"), spark),
      h("div", { class: "card wide" }, h("h2", {}, "Server"),
        h("dl", { class: "kv" },
          h("dt", {}, "Hostname"), h("dd", {}, s.hostname),
          h("dt", {}, "OS"), h("dd", {}, s.os),
          h("dt", {}, "Kernel"), h("dd", {}, `${s.kernel} (${s.arch})`),
          h("dt", {}, "Uptime"), h("dd", {}, fmtUptime(s.uptime_seconds)),
          h("dt", {}, "Panel"), h("dd", {}, state.user.version))));
    drawSpark(spark, s.cpu_history);
  }
  refresh();
  state.timer = setInterval(refresh, 3000);
}

function drawSpark(c, data) {
  const dpr = window.devicePixelRatio || 1;
  const w = c.clientWidth, hgt = c.clientHeight;
  c.width = w * dpr; c.height = hgt * dpr;
  const g = c.getContext("2d"); g.scale(dpr, dpr);
  const css = getComputedStyle(document.documentElement);
  const accent = css.getPropertyValue("--accent").trim(), border = css.getPropertyValue("--border").trim();
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
    h("div", { class: "topbar" }, h("h1", {}, "File Manager"),
      h("div", { class: "row" },
        h("button", { class: "btn", onclick: async () => {
          const n = prompt("New folder name"); if (!n) return;
          try { await api.post("api/files/mkdir", { path: joinPath(cur, n) }); load(); } catch (x) { say(x.message); }
        } }, "New folder"),
        h("button", { class: "btn", onclick: async () => {
          const n = prompt("New file name"); if (!n) return;
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
          const n = prompt("Rename to", e.name); if (!n || n === e.name) return;
          try { await api.post("api/files/rename", { from: e.path, to: joinPath(cur, n) }); load(); } catch (x) { say(x.message); }
        } }, "Rename"),
        h("button", { class: "btn sm danger", onclick: async () => {
          if (!confirm(`Delete ${e.name}${e.is_dir ? " and everything inside it" : ""}? This cannot be undone.`)) return;
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
  view.append(h("div", { class: "topbar" }, h("h1", { class: "mono" }, file),
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Websites")), notice, setupBox, formBox, listBox);

  let status = null;

  async function loadStatus() {
    try { status = await api.get("api/sites/status"); } catch (x) { say(x.message); return; }
    if (status.nginx_installed && status.certbot_installed) { setupBox.replaceChildren(); return; }
    const php = h("input", { type: "checkbox" });
    const btn = h("button", { class: "btn primary", onclick: async () => {
      if (!confirm("Install Nginx and Certbot with apt now? This can take a few minutes.")) return;
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
          (s.aliases || []).length ? h("div", { class: "muted small" }, s.aliases.join(", ")) : null),
        h("td", { class: "muted" }, typeName),
        h("td", {}, sslBadge(s), s.force_https ? h("span", { class: "badge" }, "HTTPS only") : null),
        h("td", {}, h("span", { class: "badge " + (s.enabled ? "ok" : "") }, s.enabled ? "Enabled" : "Disabled")),
        h("td", { class: "act" }, h("div", { class: "row" },
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
            if (!confirm(`Get one certificate for ${s.domain} and *.${s.domain}? It is verified through your Cloudflare account (DNS tab must be connected). This can take a minute.`)) return;
            const b = e.currentTarget; b.disabled = true; say("Requesting a wildcard certificate, this takes about a minute…", true);
            try { const r = await api.post("api/sites/" + s.id + "/ssl/wildcard"); say("Wildcard certificate installed.", true, r.log); loadList(); }
            catch (x) { say(x.message, false, x.log); } finally { b.disabled = false; }
          } }, "Wildcard SSL"),
          h("button", { class: "btn sm", onclick: () => patch(s, { enabled: !s.enabled }) }, s.enabled ? "Disable" : "Enable"),
          h("button", { class: "btn sm danger", onclick: async () => {
            if (!confirm(`Remove ${s.domain} from Nginx? Its files and certificate are kept on the server.`)) return;
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Databases")), notice, secretBox, body);
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
              if (!confirm(`Set a new password for user "${db.name}"? Apps using the old one will stop working.`)) return;
              try { showSecret(await api.post("api/databases/" + en.engine + "/" + db.name + "/password")); } catch (x) { say(x.message); }
            } }, "New password"),
            h("button", { class: "btn sm danger", onclick: async () => {
              const typed = prompt(`This permanently deletes the database "${db.name}" and its user. Type the database name to confirm:`);
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Backups")), notice, makeBox, schedBox, listBox);
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
            if (!confirm(msg)) return;
            const btn = e.currentTarget; btn.disabled = true; say("Restoring…", true);
            try { await api.post("api/backups/" + encodeURIComponent(b.name) + "/restore"); say("Restored.", true); } catch (x) { say(x.message); } finally { btn.disabled = false; }
          } }, "Restore"),
          h("button", { class: "btn sm danger", onclick: async () => {
            if (!confirm("Delete this backup file?")) return;
            try { await api.del("api/backups/" + encodeURIComponent(b.name)); loadList(); } catch (x) { say(x.message); }
          } }, "Delete"))))))));
  }
  drawMake(); loadList().then(drawSchedule);
}

/* ---- dns (Cloudflare) ---- */
function dnsView(view) {
  const notice = h("div");
  const say = (m, ok) => notice.replaceChildren(...(m ? [h("div", { class: "msg " + (ok ? "ok" : "err") }, m)] : []));
  const body = h("div");
  view.append(h("div", { class: "topbar" }, h("h1", {}, "DNS (Cloudflare)")), notice, body);
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
      if (!confirm("Remove the saved Cloudflare token from this server?")) return;
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
            if (!confirm(`Delete the ${r.type} record ${r.name}?`)) return;
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Cron jobs")), notice, formBox, listBox);
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
            if (!confirm(`Delete the job "${j.name}"?`)) return;
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Firewall")), notice, body);

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
      if (st.active && !confirm("Turn the firewall off? All ports will be reachable from the internet.")) return;
      if (!st.active && !confirm(`Turn the firewall on? Ports ${st.protected_ports.join(", ")} (SSH and this panel) will be allowed first so you stay connected. Everything else incoming is blocked unless you add a rule.`)) return;
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
            if (!confirm(`Delete rule ${r.number} (${r.to})?`)) return;
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
    h("div", { class: "topbar" }, h("h1", {}, "Terminal"),
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Settings")), h("div", { class: "grid" }, acmeCard, pwCard, tfCard));
}

/* ---- boot ---- */
async function boot() {
  try { state.user = await api.get("api/me"); } catch { state.user = null; }
  if (state.user && !location.hash) location.hash = "/dashboard";
  render();
}
applyTheme();
boot();
