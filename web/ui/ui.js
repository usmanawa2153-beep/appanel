"use strict";
/* Shared UI kit: DOM helper, icons, dialogs, toasts, page headers.
   Everything is built with createElement/textContent (never innerHTML). */

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

const ICONS = {
  dashboard: "M4 13h6V4H4zM14 20h6V11h-6zM4 20h6v-4H4zM14 7h6V4h-6z",
  globe: "M12 21a9 9 0 100-18 9 9 0 000 18zM3 12h18M12 3c2.6 2.4 4 5.6 4 9s-1.4 6.6-4 9c-2.6-2.4-4-5.6-4-9s1.4-6.6 4-9z",
  apps: "M12 3l8 4.5v9L12 21l-8-4.5v-9zM12 12l8-4.5M12 12v9M12 12L4 7.5",
  folder: "M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z",
  terminal: "M4 5h16a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1zM7 10l3 2-3 2M12 15h5",
  dns: "M4 6h16M4 12h16M4 18h16M7 6v0M7 12v0M7 18v0",
  database: "M12 4c4.4 0 8 1.3 8 3s-3.6 3-8 3-8-1.3-8-3 3.6-3 8-3zM4 7v10c0 1.7 3.6 3 8 3s8-1.3 8-3V7M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  backup: "M12 3v12M7 10l5 5 5-5M4 19h16",
  clock: "M12 21a9 9 0 100-18 9 9 0 000 18zM12 7v5l3 2",
  shield: "M12 3l8 3v6c0 4.5-3.2 7.8-8 9-4.8-1.2-8-4.5-8-9V6z",
  settings: "M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.7 1.7 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-1.8-.3 1.7 1.7 0 00-1 1.5V21a2 2 0 01-4 0v-.1a1.7 1.7 0 00-1.1-1.5 1.7 1.7 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00.3-1.8 1.7 1.7 0 00-1.5-1H3a2 2 0 010-4h.1a1.7 1.7 0 001.5-1.1 1.7 1.7 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.8.3H9a1.7 1.7 0 001-1.5V3a2 2 0 014 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.8V9a1.7 1.7 0 001.5 1H21a2 2 0 010 4h-.1a1.7 1.7 0 00-1.5 1z",
  sun: "M12 16a4 4 0 100-8 4 4 0 000 8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
  moon: "M21 13A9 9 0 1111 3a7 7 0 0010 10z",
  logout: "M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4M16 17l5-5-5-5M21 12H9",
  menu: "M4 6h16M4 12h16M4 18h16",
  search: "M11 19a8 8 0 100-16 8 8 0 000 16zM21 21l-4.3-4.3",
  check: "M5 12.5l4.5 4.5L19 7.5",
  x: "M6 6l12 12M18 6L6 18",
  alert: "M12 3l10 18H2zM12 10v5M12 18v.01",
  info: "M12 21a9 9 0 100-18 9 9 0 000 18zM12 11v5M12 8v.01",
  plug: "M9 2v5M15 2v5M6 7h12v4a6 6 0 01-12 0zM12 17v5",
  palette: "M12 21a9 9 0 110-18c5 0 9 3.6 9 8 0 2.5-2 4-4 4h-2a2 2 0 00-1.4 3.4A2 2 0 0112 21zM7.5 11v.01M10 7.5v.01M14.5 7.5v.01M17 11v.01",
  users: "M16 20v-1a4 4 0 00-4-4H7a4 4 0 00-4 4v1M9.5 11a3.5 3.5 0 100-7 3.5 3.5 0 000 7zM21 20v-1a4 4 0 00-3-3.9M16 4.1a3.5 3.5 0 010 6.8",
  key: "M15 7a4 4 0 11-3.9 4.9L4 19v2h3v-2h2v-2h2l1.1-1.1A4 4 0 0015 7zM16.5 8.5v.01",
  refresh: "M20 11a8 8 0 00-14.5-4M4 4v4h4M4 13a8 8 0 0014.5 4M20 20v-4h-4",
  external: "M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 01-1 1H5a1 1 0 01-1-1V7a1 1 0 011-1h5",
  copy: "M9 9h10v11H9zM5 15V4h10",
  trash: "M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3",
  power: "M12 3v9M6.4 7a8 8 0 1011.2 0",
  zap: "M13 2L4 14h7l-1 8 9-12h-7z",
  wrench: "M14.7 6.3a4 4 0 005 5L21 17l-4 4-5.7-5.7a4 4 0 00-5-5L10 7z",
  eye: "M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12zM12 15a3 3 0 100-6 3 3 0 000 6z",
  download: "M12 4v11M7 11l5 5 5-5M5 20h14",
  box: "M3 7.5L12 3l9 4.5v9L12 21l-9-4.5zM3 7.5L12 12l9-4.5M12 12v9",
};

function icon(name, cls) {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("class", "ic" + (cls ? " " + cls : ""));
  svg.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(NS, "path");
  p.setAttribute("d", ICONS[name] || ICONS.info);
  svg.append(p);
  return svg;
}

const ui = {
  icon,

  toast(msg, kind = "ok") {
    let box = document.querySelector(".toasts");
    if (!box) { box = h("div", { class: "toasts", role: "status", "aria-live": "polite" }); document.body.append(box); }
    const t = h("div", { class: "toast " + kind }, icon(kind === "ok" ? "check" : kind === "err" ? "alert" : "info"), h("div", {}, msg));
    box.append(t);
    setTimeout(() => { t.classList.add("out"); setTimeout(() => t.remove(), 220); }, kind === "err" ? 7000 : 3500);
  },

  /* Modal dialog. Resolves with a value (or null if dismissed). */
  modal({ title, body, actions, wide }) {
    return new Promise((resolve) => {
      const dlg = h("dialog", { class: "modal" + (wide ? " wide" : "") });
      const done = (v) => { dlg.close(); dlg.remove(); resolve(v); };
      const foot = h("div", { class: "modal-foot" }, actions(done));
      dlg.append(h("div", { class: "modal-body" }, title ? h("h2", {}, title) : null, body), foot);
      dlg.addEventListener("cancel", (e) => { e.preventDefault(); done(null); });
      dlg.addEventListener("click", (e) => { if (e.target === dlg) done(null); });
      document.body.append(dlg);
      dlg.showModal();
    });
  },

  confirm(message, opts = {}) {
    const danger = opts.danger ?? /delete|remove|permanently|turn the firewall off|cannot be undone|stop working/i.test(message);
    return ui.modal({
      title: opts.title || (danger ? "Are you sure?" : "Please confirm"),
      body: h("p", {}, message),
      actions: (done) => {
        const ok = h("button", { class: "btn " + (danger ? "danger solid" : "primary"), onclick: () => done(true) }, opts.ok || (danger ? "Yes, continue" : "Continue"));
        setTimeout(() => ok.focus(), 0);
        return [h("button", { class: "btn", onclick: () => done(null) }, "Cancel"), ok];
      },
    }).then((v) => v === true);
  },

  /* Returns the entered string, or null if cancelled/empty. */
  prompt(message, value = "", opts = {}) {
    const input = h("input", { type: opts.type || "text", value, autocomplete: "off", spellcheck: "false" });
    return ui.modal({
      title: opts.title || "Enter a value",
      body: h("div", {}, h("p", {}, message), input),
      actions: (done) => {
        input.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); done(input.value); } });
        setTimeout(() => { input.focus(); input.select(); }, 0);
        return [h("button", { class: "btn", onclick: () => done(null) }, "Cancel"), h("button", { class: "btn primary", onclick: () => done(input.value) }, opts.ok || "OK")];
      },
    }).then((v) => (v ? v : null));
  },

  /* Show a secret once, with a copy button. */
  secret(title, text, value) {
    const code = h("pre", { class: "log" }, value);
    return ui.modal({
      title,
      body: h("div", {}, h("p", {}, text), code),
      actions: (done) => [
        h("button", { class: "btn", onclick: async (e) => {
          try { await navigator.clipboard.writeText(value); e.currentTarget.textContent = "Copied"; } catch { ui.toast("Copy is blocked here. Select the text instead.", "err"); }
        } }, "Copy"),
        h("button", { class: "btn primary", onclick: () => done(true) }, "Done"),
      ],
    });
  },

  pageHead(title, desc, ...actions) {
    return h("div", { class: "page-head" },
      h("div", {}, h("h1", { class: "page-title" }, title), desc ? h("p", { class: "page-desc" }, desc) : null),
      actions.flat().length ? h("div", { class: "page-actions" }, actions) : null);
  },

  empty(ic, title, text, ...actions) {
    return h("div", { class: "empty" }, icon(ic), h("h3", {}, title), text ? h("p", {}, text) : null, actions.length ? h("div", { class: "row", style: null }, actions) : null);
  },

  skeleton(n = 3) {
    return h("div", { class: "card" }, Array.from({ length: n }, () => h("div", { class: "skeleton" })));
  },

  switch(checked, onchange) {
    const i = h("input", { type: "checkbox", onchange: (e) => onchange(e.target.checked) });
    i.checked = !!checked;
    return h("label", { class: "switch" }, i, h("span", { class: "track" }));
  },
};

/* Drop-in async replacements for the browser dialogs. */
window.confirmAsync = ui.confirm;
