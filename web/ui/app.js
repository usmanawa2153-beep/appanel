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
    if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status})`);
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

function stopTimer() { if (state.timer) { clearInterval(state.timer); state.timer = null; } }

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
    h("nav", { class: "nav" }, link("/dashboard", "Dashboard"), link("/files", "File Manager"), link("/settings", "Settings")),
    h("div", { class: "spacer" }),
    h("div", { class: "muted" }, `Signed in as ${state.user.username}`),
    h("div", { class: "row" },
      h("button", { class: "btn sm", onclick: toggleTheme }, "Theme"),
      h("button", { class: "btn sm", onclick: async () => { try { await api.post("api/logout"); } catch { /* already out */ } state.user = null; render(); } }, "Sign out")));
  if (path.startsWith("/files")) filesView(view);
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
  view.append(h("div", { class: "topbar" }, h("h1", {}, "Settings")), h("div", { class: "grid" }, pwCard, tfCard));
}

/* ---- boot ---- */
async function boot() {
  try { state.user = await api.get("api/me"); } catch { state.user = null; }
  if (state.user && !location.hash) location.hash = "/dashboard";
  render();
}
applyTheme();
boot();
