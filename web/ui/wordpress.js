"use strict";
/* WordPress manager: overview, plugins, themes, users and settings for one site. */

function wordpressView(view, id) {
  const base = "api/wp/" + id;
  const body = h("div");
  const head = h("div");
  const tabsBar = h("div", { class: "tabs", role: "tablist" });
  view.append(head, tabsBar, body);

  let site = null, overview = null, tab = "overview";

  /* Run a slow change: lock the button, toast the outcome. */
  async function run(btn, working, fn, done) {
    if (btn) btn.disabled = true;
    ui.toast(working, "info");
    try { const r = await fn(); ui.toast(done || "Done"); return r || true; }
    catch (x) { ui.toast(x.message, "err"); return null; }
    finally { if (btn) btn.disabled = false; }
  }

  const scheme = () => (site && site.ssl ? "https" : "http");

  function drawHead() {
    const dom = site ? site.domain : "WordPress";
    head.replaceChildren(
      h("div", { class: "row", style: null }, h("a", { class: "btn ghost sm", href: "#/websites" }, "← Websites")),
      ui.pageHead(dom, overview ? (overview.title || "WordPress site") : "Loading…",
        site ? h("a", { class: "btn", href: scheme() + "://" + dom + "/", target: "_blank", rel: "noopener noreferrer" }, icon("external", "sm"), "Visit site") : null,
        site ? h("a", { class: "btn primary", href: scheme() + "://" + dom + "/wp-admin/", target: "_blank", rel: "noopener noreferrer" }, "Open WP Admin") : null));
  }

  function drawTabs() {
    const upd = overview ? overview.plugin_updates + overview.theme_updates + (overview.update ? 1 : 0) : 0;
    const t = (key, label, n) => h("button", { class: tab === key ? "on" : "", role: "tab", "aria-selected": String(tab === key), onclick: () => { tab = key; drawTabs(); drawBody(); } },
      label, n ? h("span", { class: "count" }, String(n)) : null);
    tabsBar.replaceChildren(t("overview", "Overview", upd), t("plugins", "Plugins", overview && overview.plugin_updates), t("themes", "Themes", overview && overview.theme_updates), t("users", "Users"), t("settings", "Settings"));
  }

  function drawBody() {
    if (!overview) { body.replaceChildren(ui.skeleton(4)); return; }
    ({ overview: overviewTab, plugins: (el) => itemsTab(el, "plugin"), themes: (el) => itemsTab(el, "theme"), users: usersTab, settings: settingsTab })[tab](body);
  }

  async function load() {
    try {
      const [sites, o] = await Promise.all([api.get("api/sites"), api.get(base)]);
      site = sites.sites.find((s) => s.id === id) || null;
      overview = o;
    } catch (x) {
      body.replaceChildren(h("div", { class: "msg err" }, x.message));
      return;
    }
    drawHead(); drawTabs(); drawBody();
  }

  /* ---- overview ---- */
  function overviewTab(el) {
    const o = overview;
    const coreBtn = h("button", { class: "btn primary", onclick: async (e) => {
      if (!(await ui.confirm(`Update WordPress from ${o.version} to ${o.update}? Back up the site first if you have not.`, { ok: "Update WordPress", danger: false }))) return;
      if (await run(e.currentTarget, "Updating WordPress…", () => api.post(base + "/core/update"), "WordPress updated")) load();
    } }, "Update to " + o.update);
    const stats = h("div", { class: "stat-row" },
      h("div", { class: "card" }, h("div", { class: "k" }, "WordPress"), h("div", { class: "v" }, o.version), o.update ? h("span", { class: "badge warn" }, o.update + " available") : h("span", { class: "badge ok" }, "Up to date")),
      h("div", { class: "card" }, h("div", { class: "k" }, "Plugin updates"), h("div", { class: "v" }, String(o.plugin_updates))),
      h("div", { class: "card" }, h("div", { class: "k" }, "Theme updates"), h("div", { class: "v" }, String(o.theme_updates))),
      h("div", { class: "card" }, h("div", { class: "k" }, "Visibility"), h("div", { class: "v" }, o.maintenance ? "Maintenance" : "Live"), o.maintenance ? h("span", { class: "badge warn" }, "Visitors see a notice") : h("span", { class: "badge ok" }, "Open to visitors")));

    const maint = ui.switch(o.maintenance, async (on) => {
      if (await run(null, on ? "Turning maintenance mode on…" : "Turning maintenance mode off…", () => api.post(base + "/maintenance", { on }), on ? "Maintenance mode is on" : "Site is live again")) o.maintenance = on; else load();
    });
    const flush = h("button", { class: "btn", onclick: (e) => run(e.currentTarget, "Clearing the cache…", () => api.post(base + "/cache/flush"), "Cache cleared") }, "Clear cache");

    el.replaceChildren(stats,
      h("div", { class: "split" },
        h("div", {},
          o.update ? h("div", { class: "card setup" }, h("h2", {}, "WordPress " + o.update + " is ready"), h("p", { class: "muted" }, "Updates fix security problems and bugs. Back up the site first if you have not."), coreBtn) : null,
          (o.plugin_updates || o.theme_updates) ? h("div", { class: "card" }, h("h2", {}, "Updates waiting"),
            h("p", { class: "muted" }, `${o.plugin_updates} plugin and ${o.theme_updates} theme update${o.plugin_updates + o.theme_updates === 1 ? "" : "s"} are available.`),
            h("div", { class: "row" },
              o.plugin_updates ? h("button", { class: "btn", onclick: () => { tab = "plugins"; drawTabs(); drawBody(); } }, "Review plugins") : null,
              o.theme_updates ? h("button", { class: "btn", onclick: () => { tab = "themes"; drawTabs(); drawBody(); } }, "Review themes") : null)) : null,
          (!o.update && !o.plugin_updates && !o.theme_updates) ? h("div", { class: "card" }, ui.empty("check", "Everything is up to date", "WordPress, plugins and themes are all on their latest versions.")) : null),
        h("div", {},
          h("div", { class: "card" }, h("h2", {}, "Quick actions"),
            h("div", { class: "setting" }, h("div", { class: "what" }, h("b", {}, "Maintenance mode"), h("span", {}, "Show visitors a short notice while you work.")), maint),
            h("div", { class: "setting" }, h("div", { class: "what" }, h("b", {}, "Cache"), h("span", {}, "Clears the object cache and expired transients.")), flush)),
          h("div", { class: "card" }, h("h2", {}, "Details"),
            h("dl", { class: "kv" }, h("dt", {}, "Address"), h("dd", {}, o.url), h("dt", {}, "Admin email"), h("dd", {}, o.admin_email), h("dt", {}, "Permalinks"), h("dd", {}, permalinkLabel(o.permalinks)), h("dt", {}, "Debug mode"), h("dd", {}, o.debug ? "On" : "Off"))))));
  }

  /* ---- plugins and themes ---- */
  function itemsTab(el, kind) {
    const label = kind === "plugin" ? "plugin" : "theme";
    const listBox = h("div", { class: "card flush" }, h("div", { class: "checks" }, ui.skeleton(3)));
    const findBox = h("div", { class: "card flush" });
    el.replaceChildren(h("div", { class: "split" }, h("div", {}, listBox), h("div", {}, findBox)));

    async function loadList() {
      let items;
      try { items = (await api.get(`${base}/${kind}s`)).items || []; }
      catch (x) { listBox.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
      const withUpdate = items.filter((i) => i.update === "available");
      const row = (i) => {
        const active = i.status === "active" || i.status === "parent";
        const act = (action, text, opts = {}) => h("button", { class: "btn sm " + (opts.cls || ""), onclick: async (e) => {
          if (opts.confirm && !(await ui.confirm(opts.confirm))) return;
          if (await run(e.currentTarget, `${text}…`, () => api.post(`${base}/${kind}s/${encodeURIComponent(i.name)}/${action}`), opts.done || "Done")) { loadList(); refreshOverview(); }
        } }, text);
        return h("div", { class: "pitem" },
          h("div", { class: "what" }, h("b", {}, i.title || i.name), h("span", {}, "Version " + i.version + (i.update === "available" ? ` → ${i.update_version} available` : ""))),
          h("span", { class: "chip " + (active ? "ok" : "") }, i.status === "parent" ? "Parent theme" : active ? "Active" : "Inactive"),
          h("div", { class: "row" },
            i.update === "available" ? act("update", "Update", { done: "Updated" }) : null,
            kind === "plugin" ? (active ? act("deactivate", "Deactivate", { done: "Deactivated" }) : act("activate", "Activate", { done: "Activated" })) : (!active ? act("activate", "Activate", { done: "Theme activated" }) : null),
            !active ? act("delete", "Delete", { cls: "danger", confirm: `Delete the ${label} "${i.title || i.name}" from this site? This cannot be undone.`, done: "Deleted" }) : null));
      };
      listBox.replaceChildren(
        h("div", { class: "card-head" }, h("h2", {}, kind === "plugin" ? "Installed plugins" : "Installed themes"),
          withUpdate.length ? h("button", { class: "btn sm primary", onclick: async (e) => {
            if (await run(e.currentTarget, `Updating ${withUpdate.length} ${label}${withUpdate.length === 1 ? "" : "s"}…`, () => api.post(`${base}/${kind}s/update-all`), "All updated")) { loadList(); refreshOverview(); }
          } }, `Update all (${withUpdate.length})`) : null),
        items.length ? h("div", {}, items.map(row)) : ui.empty(kind === "plugin" ? "plug" : "palette", `No ${label}s installed`, `Search the WordPress.org directory to add one.`));
    }

    /* search the wordpress.org directory */
    const q = h("input", { type: "search", placeholder: kind === "plugin" ? "Search plugins, e.g. seo, backup, cache" : "Search themes, e.g. blog, shop", "aria-label": "Search" });
    const results = h("div");
    const go2 = async () => {
      const term = q.value.trim();
      if (!term) return;
      results.replaceChildren(h("div", { class: "checks" }, ui.skeleton(3)));
      let res;
      try { res = (await api.get(`${base}/${kind}s/search?q=` + encodeURIComponent(term))).results || []; }
      catch (x) { results.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
      if (!res.length) { results.replaceChildren(ui.empty("search", "Nothing found", "Try a shorter or different word.")); return; }
      results.replaceChildren(...res.map((r) => {
        const inst = (activate) => async (e) => {
          const b = e.currentTarget;
          if (await run(b, `Installing ${r.name}…`, () => api.post(`${base}/${kind}s/install`, { slug: r.slug, activate }), activate ? `${r.name} installed and activated` : `${r.name} installed`)) { b.textContent = "Installed"; loadList(); refreshOverview(); } };
        return h("div", { class: "result" },
          h("div", { class: "what" }, h("b", {}, r.name), h("p", {}, (r.short_description || r.description || "").slice(0, 160)),
            h("p", {}, [r.rating ? `${(r.rating / 20).toFixed(1)} of 5` : null, r.active_installs ? `${r.active_installs.toLocaleString()}+ installs` : null, r.version ? "v" + r.version : null].filter(Boolean).join(", "))),
          h("div", { class: "row" }, h("button", { class: "btn sm", onclick: inst(false) }, "Install"), h("button", { class: "btn sm primary", onclick: inst(true) }, kind === "plugin" ? "Install & activate" : "Install & use")));
      }));
    };
    findBox.append(h("div", { class: "card-head" }, h("h2", {}, kind === "plugin" ? "Add a plugin" : "Add a theme")),
      h("form", { class: "searchbar", onsubmit: (e) => { e.preventDefault(); go2(); } }, q, h("button", { class: "btn primary", type: "submit" }, "Search")),
      results);
    results.replaceChildren(ui.empty("search", "Search WordPress.org", `Find free ${label}s from the official directory.`));
    loadList();
  }

  async function refreshOverview() {
    try { overview = await api.get(base); drawTabs(); } catch { /* keep the old numbers */ }
  }

  /* ---- users ---- */
  function usersTab(el) {
    const box = h("div", { class: "card flush" }, h("div", { class: "checks" }, ui.skeleton(3)));
    el.replaceChildren(box);
    (async () => {
      let users;
      try { users = (await api.get(base + "/users")).users || []; }
      catch (x) { box.replaceChildren(h("div", { class: "msg err" }, x.message)); return; }
      box.replaceChildren(
        h("div", { class: "card-head" }, h("h2", {}, "Users")),
        h("div", {}, users.map((u) => h("div", { class: "pitem" },
          h("div", { class: "what" }, h("b", {}, u.display_name || u.user_login), h("span", {}, `${u.user_login}, ${u.user_email}`)),
          h("span", { class: "badge teal" }, u.roles || "no role"),
          h("button", { class: "btn sm", onclick: () => changePassword(u) }, icon("key", "sm"), "Change password")))));
    })();
  }

  async function changePassword(u) {
    const input = h("input", { type: "text", autocomplete: "off", spellcheck: "false", placeholder: "Leave empty to generate a strong one" });
    const v = await ui.modal({
      title: "Change password",
      body: h("div", {}, h("p", {}, `Set a new password for ${u.user_login}. Leave it empty and a strong 20-character password is generated for you.`), h("label", { class: "field" }, h("span", {}, "New password"), input)),
      actions: (done) => {
        setTimeout(() => input.focus(), 0);
        return [h("button", { class: "btn", onclick: () => done(null) }, "Cancel"), h("button", { class: "btn primary", onclick: () => done({ pw: input.value }) }, "Set password")];
      },
    });
    if (!v) return;
    try {
      const r = await api.post(`${base}/users/${u.ID}/password`, { password: v.pw });
      await ui.secret("Password changed", `This is the new password for ${u.user_login}. It is shown once and is not stored by Appanel.`, r.password);
    } catch (x) { ui.toast(x.message, "err"); }
  }

  /* ---- settings ---- */
  const PERMALINKS = [["", "Plain (?p=123)"], ["/%postname%/", "Post name (/sample-post/)"], ["/%year%/%monthnum%/%postname%/", "Month and name (/2026/11/sample-post/)"],
    ["/%year%/%monthnum%/%day%/%postname%/", "Day and name (/2026/11/28/sample-post/)"], ["/archives/%post_id%", "Numeric (/archives/123)"]];
  const permalinkLabel = (p) => (PERMALINKS.find(([v]) => v === p) || [p, p || "Plain"])[1];

  function settingsTab(el) {
    const o = overview;
    const title = h("input", { type: "text", value: o.title });
    const tag = h("input", { type: "text", value: o.tagline });
    const mail = h("input", { type: "email", value: o.admin_email });
    const perma = h("select", {}, PERMALINKS.map(([v, l]) => h("option", { value: v, selected: v === o.permalinks }, l)));
    let visible = o.search_visible, debug = o.debug;
    const save = h("button", { class: "btn primary", type: "submit" }, "Save changes");
    const form = h("form", { onsubmit: async (e) => {
      e.preventDefault();
      if (await run(save, "Saving settings…", () => api.put(base + "/settings", { title: title.value, tagline: tag.value, admin_email: mail.value, search_visible: visible, permalinks: perma.value, debug }), "Settings saved")) { await refreshOverview(); drawHead(); }
    } },
      h("div", { class: "card" }, h("h2", {}, "General"),
        h("label", { class: "field" }, h("span", {}, "Site title"), title),
        h("label", { class: "field" }, h("span", {}, "Tagline"), tag),
        h("label", { class: "field" }, h("span", {}, "Admin email"), mail),
        h("label", { class: "field" }, h("span", {}, "Permalink structure"), perma)),
      h("div", { class: "card" }, h("h2", {}, "Visibility and debugging"),
        h("div", { class: "setting" }, h("div", { class: "what" }, h("b", {}, "Let search engines index this site"), h("span", {}, "Turn off while the site is under construction.")), ui.switch(visible, (v) => { visible = v; })),
        h("div", { class: "setting" }, h("div", { class: "what" }, h("b", {}, "Debug mode"), h("span", {}, "Logs PHP errors to wp-content/debug.log. Leave off on live sites.")), ui.switch(debug, (v) => { debug = v; }))),
      h("div", { class: "row" }, save));
    el.replaceChildren(form);
  }

  drawHead(); drawTabs(); drawBody();
  load();
}
