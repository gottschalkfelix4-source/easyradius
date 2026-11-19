"use strict";

/* ============ Hilfsfunktionen ============ */
const $ = (sel, root = document) => root.querySelector(sel);
const app = $("#app");
const modal = $("#modal");

const esc = (v) => String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

/* Anfrage senden; Fehler mit dem Text des Servers (und dem HTTP-Status in err.status) werfen. */
async function send(method, url, body) {
  const opt = { method, headers: { "X-EasyRadius": "1" } };
  if (body instanceof FormData) opt.body = body;
  else if (body !== undefined) { opt.body = JSON.stringify(body); opt.headers["Content-Type"] = "application/json"; }
  const res = await fetch(url, opt);
  if (res.ok) return res;
  let data = null;
  try { data = await res.json(); } catch { /* leer */ }
  if (res.status === 401 && !url.startsWith("/api/login")) { state.user = ""; render(); throw new Error("Bitte anmelden"); }
  const err = new Error((data && data.error) || "Fehler " + res.status);
  err.status = res.status;
  err.plain = !(data && data.error); // keine Antwort von der API selbst
  throw err;
}

async function api(method, url, body) {
  const res = await send(method, url, body);
  try { return await res.json(); } catch { return null; }
}

/* Datei vom Server holen (POST mit JSON) und im Browser speichern. */
async function download(url, body, fallbackName) {
  const res = await send("POST", url, body);
  const cd = res.headers.get("Content-Disposition") || "";
  const m = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(cd);
  let name = fallbackName;
  if (m) { try { name = decodeURIComponent(m[1]); } catch { name = m[1]; } }
  saveBlob(await res.blob(), name);
}

function saveBlob(blob, name) {
  const href = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = href;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(href), 10000);
}

/* Endpunkt fehlt in dieser Server-Version? */
const notAvailable = (err) => !!err && (err.status === 405 || (err.status === 404 && err.plain));
const NOT_AVAILABLE = "Mit dieser Server-Version noch nicht verfügbar";

function toast(msg, bad = false) {
  const t = document.createElement("div");
  t.className = "toast" + (bad ? " bad" : "");
  t.textContent = msg;
  const box = $("#toasts");
  box.append(t);
  // Über einem offenen Dialog anzeigen (Top Layer), wo der Browser das kann.
  if (modal.open && box.showPopover) {
    try { if (box.matches(":popover-open")) box.hidePopover(); box.showPopover(); } catch { /* ältere Browser */ }
  }
  setTimeout(() => t.remove(), 3200);
}

const ICONS = {
  home: '<path d="M3 10.5 12 3l9 7.5"/><path d="M5 9.5V20h14V9.5"/><path d="M10 20v-6h4v6"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><circle cx="17" cy="9" r="2.5"/><path d="M16 14.2a5 5 0 0 1 5.5 4.8"/>',
  router: '<rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 16.5h.01M11 16.5h.01"/><path d="M8.5 9a5 5 0 0 1 7 0M6 6.3a8.5 8.5 0 0 1 12 0"/><path d="M12 13v-2"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13"/><path d="M3.5 6h.01M3.5 12h.01M3.5 18h.01"/>',
  shield: '<path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.3 7.5 9.5 4.3-1.2 7.5-4.9 7.5-9.5V6z"/><path d="m9 12 2 2 4-4"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  wifi: '<path d="M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0"/><path d="M12 19.5h.01"/><path d="M2 9a14.5 14.5 0 0 1 20 0"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>',
  trash: '<path d="M4 7h16M10 11v6M14 11v6"/><path d="M6 7l1 13h10l1-13M9 7V4h6v3"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  alert: '<path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h8"/>',
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  dice: '<rect x="4" y="4" width="16" height="16" rx="3"/><path d="M9 9h.01M15 15h.01M15 9h.01M9 15h.01"/>',
  download: '<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',
  upload: '<path d="M12 20V9M7 14l5-5 5 5M5 4h14"/>',
  refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>',
  logout: '<path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h11"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3M14 9l2 2"/>',
  power: '<path d="M12 3v9"/><path d="M6.3 7a8 8 0 1 0 11.4 0"/>',
  activity: '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  team: '<rect x="3" y="4" width="18" height="16" rx="2.5"/><circle cx="9" cy="10" r="2"/><circle cx="15.5" cy="10" r="1.6"/><path d="M5.8 16.5a3.4 3.4 0 0 1 6.4 0M13.6 16a2.6 2.6 0 0 1 4.6 0"/>',
  building: '<path d="M4 21V5a1 1 0 0 1 1-1h9a1 1 0 0 1 1 1v16"/><path d="M15 9h4a1 1 0 0 1 1 1v11M3 21h18"/><path d="M8 8h3M8 12h3M8 16h3"/>',
  phone: '<rect x="6.5" y="2.5" width="11" height="19" rx="2.5"/><path d="M11 18.5h2"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.7 3.8 5.7 3.8 9S14.5 18.3 12 21c-2.5-2.7-3.8-5.7-3.8-9S9.5 5.7 12 3z"/>',
  printer: '<path d="M7 9V3h10v6"/><rect x="3" y="9" width="18" height="8" rx="2"/><path d="M7 14h10v7H7z"/><path d="M17 12h.01"/>',
  plug: '<path d="M9 3v5M15 3v5"/><path d="M6 8h12v3a6 6 0 0 1-12 0z"/><path d="M12 17v4"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
  unlock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 7.5-2"/>',
  badge: '<circle cx="12" cy="8" r="4"/><path d="M4.5 21a7.5 7.5 0 0 1 15 0"/><path d="m15.5 15.5 1.5 1.5 3-3"/>',
  archive: '<rect x="3" y="4" width="18" height="5" rx="1"/><path d="M5 9v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9"/><path d="M10 13h4"/>',
  devices: '<rect x="2.5" y="4" width="13" height="10" rx="1.5"/><path d="M6 18h6"/><rect x="17" y="8" width="4.5" height="12" rx="1"/>',
};
const icon = (n, cls = "") => `<svg class="i ${cls}" viewBox="0 0 24 24">${ICONS[n] || ""}</svg>`;

const fmtDate = (s) => {
  if (!s || s.startsWith("0001")) return "–";
  const d = new Date(s);
  return d.toLocaleString("de-DE", { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
};
const fmtRel = (s) => {
  if (!s || s.startsWith("0001")) return "nie";
  const diff = (Date.now() - new Date(s).getTime()) / 1000;
  if (diff < 60) return "gerade eben";
  if (diff < 3600) return `vor ${Math.floor(diff / 60)} Min.`;
  if (diff < 86400) return `vor ${Math.floor(diff / 3600)} Std.`;
  if (diff < 86400 * 7) return `vor ${Math.floor(diff / 86400)} Tag${diff >= 172800 ? "en" : ""}`;
  return fmtDate(s).split(",")[0];
};
const fmtTime = (s) => new Date(s).toLocaleTimeString("de-DE", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
const fmtDay = (s) => (s ? fmtDate(s).split(",")[0] : "–");
const fmtDur = (sec) => {
  sec = Math.max(0, Math.round(sec || 0));
  if (sec < 60) return `${sec} Sek.`;
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
  return h ? `${h} Std. ${m} Min.` : `${m} Min.`;
};
const fmtMonth = (ym) => {
  const [y, m] = String(ym).split("-").map(Number);
  return y && m ? new Date(y, m - 1, 1).toLocaleDateString("de-DE", { month: "long", year: "numeric" }) : String(ym);
};
const isPast = (s) => !!s && !s.startsWith("0001") && new Date(s).getTime() < Date.now();
const fmtBytes = (n) => { const u = ["B", "KB", "MB", "GB", "TB"]; let i = 0; while (n >= 1024 && i < 4) { n /= 1024; i++; } return n.toFixed(i ? 1 : 0) + " " + u[i]; };

function randomSecret(len = 24) {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
  const b = new Uint32Array(len);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => chars[x % chars.length]).join("");
}
function randomPassword() {
  const words = randomSecret(12);
  return words.slice(0, 4) + "-" + words.slice(4, 8) + "-" + words.slice(8, 12);
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast("In die Zwischenablage kopiert"); }
  catch { prompt("Zum Kopieren markieren:", text); }
}

/* ============ Zustand & Routing ============ */
const state = { user: "", role: "", setup: false, version: "", server: "", route: "", es: null, logFilter: "all", logSearch: "",
  secure: true, loopback: true, httpsPort: "8443", httpsOn: false };
const NAV = [
  ["uebersicht", "Übersicht", "home"],
  ["benutzer", "Benutzer", "users"],
  ["mac", "MAC-Adressen", "printer"],
  ["gruppen", "Gruppen", "team"],
  ["geraete", "Geräte", "router"],
  ["sitzungen", "Sitzungen", "activity"],
  ["protokoll", "Protokoll", "list"],
  ["zertifikat", "Zertifikat", "shield"],
  ["ad", "Active Directory", "building"],
  ["konten", "Konten", "badge", true], // nur für Administratoren
];

/* Rollen: "viewer" darf nur lesen. */
const isAdmin = () => state.role !== "viewer";
const adm = (html) => (isAdmin() ? html : "");

function applyState(s) {
  Object.assign(state, { user: s.user || "", role: s.role || "", setup: !!s.setup, version: s.version, server: s.server,
    secure: !!s.secure, loopback: !!s.loopback, httpsPort: s.httpsPort || "8443", httpsOn: !!s.httpsOn });
}

async function boot() {
  try {
    applyState(await api("GET", "/api/state"));
  } catch (e) { app.innerHTML = `<div class="auth-wrap"><div class="card">Server nicht erreichbar: ${esc(e.message)}</div></div>`; return; }
  render();
}

/* Hinweis bei unverschlüsselter Verbindung aus dem Netz */
function bannerHidden() { try { return sessionStorage.getItem("er-nohttps") === "1"; } catch { return false; } }
function secureBanner() {
  if (state.secure || state.loopback || bannerHidden()) return "";
  const target = `https://${location.hostname}:${state.httpsPort}${location.pathname}${location.hash}`;
  return `<div class="topwarn" id="topwarn">${icon("alert")}
    <span class="tw-text">Unverschlüsselte Verbindung – Passwörter und Secrets gehen im Klartext übers Netz.
      ${state.httpsOn ? `<a href="${esc(target)}">Per HTTPS öffnen</a>` : ""}</span>
    <button class="btn ghost sm" data-action="hideBanner" title="Ausblenden">${icon("x")}</button></div>`;
}

window.addEventListener("hashchange", render);

function render() {
  if (state.es && state.route !== "protokoll") { state.es.close(); state.es = null; }
  if (state.setup) return renderSetup();
  if (!state.user) return renderLogin();
  const route = location.hash.slice(2) || "uebersicht";
  state.route = route;
  if (!$(".shell")) renderShell();
  document.querySelectorAll(".nav a").forEach((a) => a.classList.toggle("active", a.dataset.r === route));
  const view = VIEWS[route] || VIEWS.uebersicht;
  const main = $(".main");
  if (!main.firstChild) main.innerHTML = `<div class="muted" style="padding:40px 0">Lädt …</div>`;
  Promise.resolve().then(() => view(main)).catch((err) => {
    if (state.user) main.innerHTML = `<div class="callout warn"><h3>Fehler beim Laden</h3>${esc(err.message)}</div>`;
  });
}

function renderShell() {
  app.innerHTML = secureBanner() + `
  <div class="shell">
    <aside class="side">
      <div class="brand"><div class="logo">${icon("wifi")}</div>EasyRadius</div>
      <nav class="nav">${NAV.filter((n) => !n[3] || isAdmin()).map(([r, t, i]) => `<a href="#/${r}" data-r="${r}" title="${t}">${icon(i)}<span>${t}</span></a>`).join("")}</nav>
      <div class="spacer"></div>
      <div class="foot">
        <div class="account"><span class="avatar">${esc(state.user.slice(0, 1).toUpperCase())}</span><span class="name" title="${esc(state.user)}">${esc(state.user)}</span>
          ${isAdmin() ? "" : `<span class="badge warn ro-badge" title="Dieses Konto darf nichts ändern">Nur lesen</span>`}
          <button class="btn ghost sm" data-action="logoutAll" title="Überall abmelden">${icon("devices")}</button>
          <button class="btn ghost sm" data-action="logout" title="Abmelden">${icon("logout")}</button></div>
        <nav class="nav"><a href="#/einstellungen" data-r="einstellungen" title="Einstellungen">${icon("gear")}<span>Einstellungen</span></a></nav>
      </div>
    </aside>
    <div class="content"><main class="main"></main></div>
  </div>`;
}

/* ============ Ereignis-Delegation ============ */
const ACTIONS = {};
document.addEventListener("click", (e) => {
  const el = e.target.closest("[data-action]");
  if (!el) return;
  const fn = ACTIONS[el.dataset.action];
  if (fn) {
    e.preventDefault();
    Promise.resolve().then(() => fn(el, e)).catch((err) => {
      const box = modal.open && modal.contains(el) && $(".msg", modal);
      if (box) box.innerHTML = `<div class="error-box">${esc(err.message)}</div>`;
      else toast(err.message, true);
    });
  }
});
ACTIONS.logout = async () => { await api("POST", "/api/logout"); state.user = ""; location.hash = ""; render(); };
ACTIONS.logoutAll = () => confirmDialog("Überall abmelden",
  "Alle Anmeldungen dieses Kontos werden beendet – auf allen Rechnern und in allen Browsern, auch hier.", "Überall abmelden", async () => {
    await api("POST", "/api/logout-all");
    state.user = ""; location.hash = ""; render();
    toast("Überall abgemeldet");
  });
ACTIONS.hideBanner = () => {
  try { sessionStorage.setItem("er-nohttps", "1"); } catch { /* egal */ }
  const b = $("#topwarn");
  if (b) b.remove();
};
ACTIONS.copy = (el) => copyText(el.dataset.value);
ACTIONS.close = () => modal.close();
ACTIONS.reveal = (el) => {
  const inp = el.closest(".input-group").querySelector("input");
  inp.type = inp.type === "password" ? "text" : "password";
};
ACTIONS.genpw = (el) => {
  const inp = el.closest(".input-group").querySelector("input");
  inp.value = el.dataset.kind === "secret" ? randomSecret() : randomPassword();
  inp.type = "text";
};

/* ============ Dialog ============ */
function openModal(title, body, { submit = "Speichern", danger = false, onSubmit, wide = false } = {}) {
  if (modal.open) modal.close();
  modal.style.width = wide ? "min(760px, calc(100vw - 32px))" : "";
  modal.innerHTML = `
    <form method="dialog" novalidate>
      <div class="dlg-head"><h2>${esc(title)}</h2></div>
      <div class="dlg-body"><div class="msg"></div>${body}</div>
      <div class="dlg-foot">
        ${onSubmit ? `<button type="submit" class="btn ${danger ? "danger solid" : "primary"}">${esc(submit)}</button>` : "<span></span>"}
        <button type="button" class="btn" data-action="close">${onSubmit ? "Abbrechen" : "Schließen"}</button>
      </div>
    </form>`;
  const form = $("form", modal);
  form.onsubmit = async (e) => {
    e.preventDefault();
    const btn = $("button[type=submit]", form);
    btn.disabled = true;
    try {
      const keep = await onSubmit(new FormData(form), form);
      if (keep !== true) modal.close();
    } catch (err) {
      $(".msg", form).innerHTML = `<div class="error-box">${esc(err.message)}</div>`;
    } finally { btn.disabled = false; }
  };
  modal.showModal();
  const first = $("input:not([type=hidden]):not([readonly]), textarea", modal);
  if (first) first.focus();
}

function confirmDialog(title, text, label, fn) {
  openModal(title, `<p>${text}</p>`, { submit: label, danger: true, onSubmit: fn });
}

/* ============ Einrichtung & Anmeldung ============ */
function authCard(title, lead, inner) {
  app.innerHTML = secureBanner() + `
  <div class="auth-wrap"><div class="auth">
    <div class="brand"><div class="logo">${icon("wifi")}</div>EasyRadius</div>
    <p class="lead">${lead}</p>
    <div class="card"><h2>${title}</h2><form id="authform" novalidate><div class="msg"></div>${inner}</form></div>
  </div></div>`;
  return $("#authform");
}

function renderSetup() {
  const f = authCard("Administrator anlegen", "Willkommen! In einer Minute ist dein RADIUS-Server bereit.", `
    <label class="f"><span>Benutzername</span><input type="text" name="username" value="admin" autocomplete="username"></label>
    <label class="f"><span>Passwort</span><input type="password" name="password" autocomplete="new-password"><small>Mindestens 8 Zeichen.</small></label>
    <label class="f"><span>Passwort wiederholen</span><input type="password" name="password2" autocomplete="new-password"></label>
    <button class="btn primary" type="submit">Los geht’s</button>`);
  f.onsubmit = async (e) => {
    e.preventDefault();
    const d = Object.fromEntries(new FormData(f));
    if (d.password !== d.password2) { $(".msg", f).innerHTML = `<div class="error-box">Die Passwörter stimmen nicht überein.</div>`; return; }
    try {
      await api("POST", "/api/setup", { username: d.username, password: d.password });
      state.setup = false; state.user = d.username; state.role = "admin";
      try { applyState(await api("GET", "/api/state")); } catch { /* Werte von oben reichen */ }
      app.innerHTML = ""; render();
    } catch (err) { $(".msg", f).innerHTML = `<div class="error-box">${esc(err.message)}</div>`; }
  };
  $("input[name=password]", f).focus();
}

function renderLogin() {
  const f = authCard("Anmelden", "Verwaltung deines RADIUS-Servers", `
    <label class="f"><span>Benutzername</span><input type="text" name="username" autocomplete="username"></label>
    <label class="f"><span>Passwort</span><input type="password" name="password" autocomplete="current-password"></label>
    <button class="btn primary" type="submit">Anmelden</button>`);
  f.onsubmit = async (e) => {
    e.preventDefault();
    const d = Object.fromEntries(new FormData(f));
    const btn = $("button", f); btn.disabled = true;
    try {
      const r = await api("POST", "/api/login", d);
      state.user = d.username.trim(); state.role = (r && r.role) || "admin";
      try { applyState(await api("GET", "/api/state")); } catch { /* Werte von oben reichen */ }
      app.innerHTML = ""; render();
    } catch (err) { $(".msg", f).innerHTML = `<div class="error-box">${esc(err.message)}</div>`; btn.disabled = false; }
  };
  $("input", f).focus();
}

/* ============ Ansichten ============ */
const VIEWS = {};

function header(title, sub, actions = "") {
  return `<div class="head"><div><h1>${title}</h1>${sub ? `<div class="sub">${sub}</div>` : ""}</div><div class="row">${actions}</div></div>`;
}

const KIND = {
  accept: ["check", "Angenommen"],
  reject: ["x", "Abgelehnt"],
  warning: ["alert", "Warnung"],
  info: ["info", "System"],
  acct: ["activity", "Sitzung"],
  audit: ["edit", "Änderung"],
};

function eventRow(e, absolute = false) {
  const [ic, label] = KIND[e.kind] || KIND.info;
  const audit = e.kind === "audit";
  const who = audit ? (e.admin ? `<b>${esc(e.admin)}</b> · ` : "") : e.user ? `<b>${esc(e.user)}</b> · ` : "";
  const meta = (audit
    ? ["Änderung", e.clientIp && `von ${esc(e.clientIp)}`]
    : [e.client && `über ${esc(e.client)}`, e.station && `Gerät ${esc(e.station)}`, e.method && esc(e.method)]).filter(Boolean).join(" · ");
  return `<div class="ev"><div class="k ${esc(e.kind)}" title="${esc(label)}">${icon(ic)}</div>
    <div class="t"><div>${who}${esc(e.message)}</div>${meta ? `<div class="meta">${meta}</div>` : ""}</div>
    <div class="time" title="${esc(fmtDate(e.time))}">${absolute ? fmtDate(e.time) : fmtRel(e.time)}</div></div>`;
}

/* ---------- Übersicht ---------- */
VIEWS.uebersicht = async (main) => {
  const d = await api("GET", "/api/dashboard");
  if (state.route !== "uebersicht") return;
  const st = d.status;
  const ip = d.ips[0] || "–";
  const max = Math.max(1, ...d.stats.hours.map((h) => h.accepted + h.rejected));
  const bars = d.stats.hours.map((h) => {
    const t = new Date(h.start).getHours();
    return `<div class="bar" title="${t}:00 Uhr – ${h.accepted} angenommen, ${h.rejected} abgelehnt">
      <span class="a" style="height:${(h.accepted / max) * 100}%"></span><span class="r" style="height:${(h.rejected / max) * 100}%"></span></div>`;
  }).join("");
  const steps = [
    [d.clients > 0, `Access Point oder Switch unter <a href="#/geraete">Geräte</a> freigeben`],
    [d.users > 0, `Ersten <a href="#/benutzer">Benutzer</a> anlegen`],
    [d.stats.accepted24h > 0, `Am Access Point „WPA2-/WPA3-Enterprise“ mit Server <b>${esc(ip)}</b>, Port <b>${st.authPort}</b> einstellen und verbinden`],
  ];
  const setupDone = steps.every((s) => s[0]);

  main.innerHTML = header("Übersicht", "Alles Wichtige auf einen Blick") + `
    ${st.running ? "" : `<div class="callout warn" style="margin-bottom:16px"><h3>Der RADIUS-Dienst läuft nicht</h3>${esc(st.error || "Unbekannter Fehler")}</div>`}
    ${d.unknown.length ? `<div class="callout warn" style="margin-bottom:16px"><h3>${icon("alert")} Unbekannte Geräte fragen an</h3>
      ${d.unknown.map((u) => `<div class="row" style="justify-content:space-between;margin-top:6px"><span><b class="mono">${esc(u.ip)}</b> – ${fmtRel(u.lastSeen)}. Anfragen werden ignoriert, bis das Gerät freigegeben ist.</span>
        ${adm(`<button class="btn sm primary" data-action="addClientIp" data-ip="${esc(u.ip)}">${icon("plus")} Freigeben</button>`)}</div>`).join("")}</div>` : ""}
    ${d.lockouts > 0 ? `<div class="callout warn" style="margin-bottom:16px"><div class="row" style="justify-content:space-between">
      <span>${icon("lock")} <b>${d.lockouts}</b> ${d.lockouts === 1 ? "Sperre" : "Sperren"} nach zu vielen Fehlversuchen aktiv.</span>
      <a class="btn sm" href="#/sitzungen">Ansehen</a></div></div>` : ""}
    <div class="grid g4">
      <div class="card stat"><div class="label"><span class="ico ${st.running ? "ok" : "bad"}">${icon("power")}</span>Status</div>
        <div class="value" style="font-size:20px;margin-top:12px">${st.running ? `<span class="badge ok"><span class="dot pulse"></span> Läuft</span>` : `<span class="badge bad">Gestoppt</span>`}</div>
        <div class="hint" style="margin-top:8px">${esc(ip)} · UDP ${st.authPort}/${st.acctPort}</div></div>
      <div class="card stat"><div class="label"><span class="ico">${icon("users")}</span>Benutzer</div><div class="value">${d.users}</div>
        <div class="hint">${d.activeUsers} aktiv${d.macs > 0 ? ` · <a href="#/mac">${d.macs} MAC-Adresse${d.macs === 1 ? "" : "n"}</a>` : ""}</div></div>
      <div class="card stat"><div class="label"><span class="ico ok">${icon("check")}</span>Erfolgreich (24 h)</div><div class="value">${d.stats.accepted24h}</div><div class="hint">Anmeldungen</div></div>
      <div class="card stat"><div class="label"><span class="ico bad">${icon("x")}</span>Abgelehnt (24 h)</div><div class="value">${d.stats.rejected24h}</div><div class="hint">Anmeldungen</div></div>
    </div>
    <div class="grid g2" style="margin-top:16px">
      <div>
        <div class="card"><div class="row" style="justify-content:space-between"><h2>Anmeldungen der letzten 24 Stunden</h2>
          <div class="legend"><span><i style="background:var(--accent)"></i>angenommen</span><span><i style="background:var(--critical)"></i>abgelehnt</span></div></div>
          <div class="chart">${bars}</div><div class="chart-axis"><span>vor 24 h</span><span>vor 12 h</span><span>jetzt</span></div></div>
        <div class="card"><div class="row" style="justify-content:space-between"><h2>Letzte Ereignisse</h2><a href="#/protokoll">Alle anzeigen</a></div>
          ${d.recent.length ? d.recent.map(eventRow).join("") : `<p class="muted">Noch keine Ereignisse.</p>`}</div>
      </div>
      <div>
        ${setupDone ? "" : `<div class="card"><h2>Erste Schritte</h2><ol class="steps">${steps.map(([ok, t]) => `<li class="${ok ? "done" : ""}"><span class="s-text">${t}</span></li>`).join("")}</ol></div>`}
        <div class="card"><div class="row" style="justify-content:space-between"><h2>Jetzt verbunden</h2><a href="#/sitzungen">Alle Sitzungen</a></div>
          ${d.online.length ? d.online.slice(0, 12).map((o) => `<div class="ev"><div class="k acct">${icon("wifi")}</div><div class="t"><b>${esc(o.user || "?")}</b>
            <div class="meta">${esc(o.station || "")} ${o.ip ? "· " + esc(o.ip) : ""} · ${esc(o.client)}</div></div><div class="time">${fmtRel(o.started)}</div></div>`).join("")
            : `<p class="muted">Keine aktiven Sitzungen bekannt. <span title="Damit Sitzungen erscheinen, am Access Point RADIUS-Accounting (Port ${st.acctPort}) aktivieren.">${icon("info")}</span></p>`}
        </div>
        <div class="card"><h2>Zertifikat</h2>
          <p><b>${esc(d.cert.subject)}</b><br><span class="muted">gültig bis ${fmtDate(d.cert.notAfter).split(",")[0]}</span></p>
          <a class="btn sm" href="#/zertifikat">${icon("shield")} Verwalten</a></div>
      </div>
    </div>`;
};

ACTIONS.addClientIp = (el) => clientDialog({ address: el.dataset.ip });

/* ---------- Check- und Reply-Attribute (Verfahren wie TekRADIUS) ---------- */
let ATTRDICT = null;
async function loadAttrDict() {
  if (!ATTRDICT) ATTRDICT = await api("GET", "/api/attributes");
  return ATTRDICT;
}
const findDef = (vendorId, code) => ATTRDICT.dictionary.find((d) => d.vendorId === vendorId && d.code === code);
const usable = (d, kind) => d.use === "both" || d.use === kind;

function attrEditorHTML(list) {
  state.attrEdit = (list || []).map((a) => ({ ...a, kind: a.kind || "reply" }));
  state.attrSel = -1;
  return `<div class="tek">
    <div class="tek-table-wrap"><table class="tek-table">
      <thead><tr><th>Attribut</th><th style="width:80px">Typ</th><th>Wert</th></tr></thead>
      <tbody id="tekrows"></tbody></table></div>
    <div class="tek-bar">
      <select id="tekkind" title="Check = muss in der Anfrage passen, Reply = wird mitgeschickt">
        <option value="check">Check</option><option value="reply" selected>Reply</option></select>
      <select id="tekattr"></select>
      <span id="tekcustom" style="display:none;gap:4px">
        <input id="tekvendor" type="number" min="1" placeholder="Herst.-ID" style="width:96px" title="Hersteller-ID, z. B. 890 = Zyxel">
        <input id="tekcode" type="number" min="1" max="255" placeholder="Nr." style="width:64px" title="Attributnummer">
        <select id="tektype" style="width:96px">${["string", "integer", "ipaddr", "octets"].map((t) => `<option>${t}</option>`).join("")}</select>
      </span>
      <input id="tektag" type="number" min="0" max="31" value="0" style="width:56px;display:none" title="Tag (0–31)">
      <input id="tekval" type="text" list="tekdl" placeholder="Wert"><datalist id="tekdl"></datalist>
      <button type="button" class="btn" id="tekadd">Hinzufügen</button>
      <button type="button" class="btn" id="tekdel" disabled>Löschen</button>
    </div>
    <div id="tekhelp" class="tek-help"></div>
    <div class="row" style="margin-top:8px"><select id="attrpreset" style="max-width:320px"><option value="">Vorlage einfügen …</option>
      ${ATTRDICT.presets.map((p, i) => `<option value="${i}">${esc(p.name)}</option>`).join("")}</select></div>
  </div>`;
}

function tekRows() {
  const body = $("#tekrows");
  if (!body) return;
  body.innerHTML = state.attrEdit.length ? state.attrEdit.map((a, i) => `
    <tr data-i="${i}" class="${i === state.attrSel ? "sel" : ""}" data-action="tekSelect">
      <td class="${a.kind}">${esc(a.name)}${a.vendorId ? `<small> · ${a.vendorId}/${a.code}</small>` : ""}</td>
      <td class="${a.kind}">${a.kind === "check" ? "Check" : "Reply"}</td>
      <td class="mono">${a.tagged && a.tag ? `<span class="muted">:${a.tag}</span> ` : ""}${esc(a.value)}</td></tr>`).join("")
    : `<tr><td colspan="3" class="muted" style="text-align:center;padding:14px">Keine Attribute – unten hinzufügen</td></tr>`;
  $("#tekadd").textContent = state.attrSel >= 0 ? "Ändern" : "Hinzufügen";
  $("#tekdel").disabled = state.attrSel < 0;
}

function tekFillAttrs(selectValue) {
  const kind = $("#tekkind").value;
  const groups = [["Standard (RFC)", ATTRDICT.dictionary.filter((d) => !d.vendor && usable(d, kind))]]
    .concat(ATTRDICT.vendors.map((v) => [v, ATTRDICT.dictionary.filter((d) => d.vendor === v && usable(d, kind))]))
    .filter(([, defs]) => defs.length);
  $("#tekattr").innerHTML = groups.map(([label, defs]) => `<optgroup label="${esc(label)}">${defs.map((d) =>
    `<option value="${d.vendorId}:${d.code}">${esc(d.name)}</option>`).join("")}</optgroup>`).join("") +
    `<optgroup label="Eigene"><option value="custom-vsa">Eigenes Herstellerattribut (VSA) …</option><option value="custom-std">Eigenes Standardattribut …</option></optgroup>`;
  if (selectValue && [...$("#tekattr").options].some((o) => o.value === selectValue)) $("#tekattr").value = selectValue;
  tekAttrChanged();
}

function tekAttrChanged() {
  const v = $("#tekattr").value;
  const custom = v.startsWith("custom");
  $("#tekcustom").style.display = custom ? "inline-flex" : "none";
  $("#tekvendor").style.display = v === "custom-vsa" ? "" : "none";
  const d = custom ? null : findDef(...v.split(":").map(Number));
  $("#tektag").style.display = d && d.tagged ? "" : "none";
  $("#tekdl").innerHTML = d && d.values ? Object.keys(d.values).map((x) => `<option value="${esc(x)}">`).join("") : "";
  const kind = $("#tekkind").value;
  const how = kind === "check" ? "Check: muss in der Anfrage des Geräts vorkommen und passen – sonst wird abgelehnt. Erlaubt: * und ? als Platzhalter, Netze wie 10.0.0.0/24, ! am Anfang = darf nicht."
    : "Reply: wird bei erfolgreicher Anmeldung an das Gerät zurückgeschickt.";
  $("#tekhelp").innerHTML = `${d && d.help ? `<b>${esc(d.name)}:</b> ${esc(d.help)}<br>` : ""}${how}`;
}

function tekLoad(i) {
  const a = state.attrEdit[i];
  state.attrSel = i;
  $("#tekkind").value = a.kind;
  const def = findDef(a.vendorId, a.code);
  if (def && usable(def, a.kind)) {
    tekFillAttrs(`${a.vendorId}:${a.code}`);
  } else {
    tekFillAttrs(a.vendorId ? "custom-vsa" : "custom-std");
    $("#tekvendor").value = a.vendorId || "";
    $("#tekcode").value = a.code;
    $("#tektype").value = a.type;
  }
  $("#tekval").value = a.value;
  $("#tektag").value = a.tag || 0;
  tekRows();
}

function tekCommit(silent) {
  const val = $("#tekval").value.trim();
  if (!val) { if (!silent) toast("Bitte einen Wert eintragen", true); return false; }
  const kind = $("#tekkind").value;
  const v = $("#tekattr").value;
  let a;
  if (v.startsWith("custom")) {
    const vendorId = v === "custom-vsa" ? +$("#tekvendor").value : 0;
    const code = +$("#tekcode").value;
    if (!code || (v === "custom-vsa" && !vendorId)) { if (!silent) toast("Bitte Hersteller-ID und Nummer angeben", true); return false; }
    a = { name: vendorId ? `VSA ${vendorId}/${code}` : `Attribut ${code}`, vendorId, code, type: $("#tektype").value };
  } else {
    const d = findDef(...v.split(":").map(Number));
    a = { name: d.name, vendorId: d.vendorId, code: d.code, type: d.type, tagged: !!d.tagged };
  }
  Object.assign(a, { kind, value: val, tag: a.tagged ? +$("#tektag").value || 0 : 0 });
  let i = state.attrSel;
  if (i < 0) i = state.attrEdit.findIndex((x) => x.kind === kind && x.vendorId === a.vendorId && x.code === a.code);
  if (i >= 0) state.attrEdit[i] = a; else state.attrEdit.push(a);
  state.attrSel = -1;
  $("#tekval").value = "";
  tekRows();
  return true;
}

function attrEditorMount() {
  tekFillAttrs();
  tekRows();
  $("#tekkind").onchange = () => tekFillAttrs($("#tekattr").value);
  $("#tekattr").onchange = tekAttrChanged;
  $("#tekadd").onclick = () => tekCommit(false);
  $("#tekval").onkeydown = (e) => { if (e.key === "Enter") { e.preventDefault(); tekCommit(false); } };
  $("#tekdel").onclick = () => {
    if (state.attrSel < 0) return;
    state.attrEdit.splice(state.attrSel, 1);
    state.attrSel = -1;
    $("#tekval").value = "";
    tekRows();
  };
  $("#attrpreset").onchange = (e) => {
    const p = ATTRDICT.presets[+e.target.value];
    e.target.value = "";
    if (!p) return;
    for (const a of p.attrs) {
      const x = { ...a, kind: a.kind || "reply" };
      const i = state.attrEdit.findIndex((y) => y.kind === x.kind && y.vendorId === x.vendorId && y.code === x.code);
      if (i >= 0) state.attrEdit[i] = x; else state.attrEdit.push(x);
    }
    tekRows();
    toast(`Vorlage „${p.name}“ eingefügt – Werte bei Bedarf anpassen`);
  };
}
ACTIONS.tekSelect = (el) => { if (el.dataset.i !== undefined) tekLoad(+el.dataset.i); };

function attrCollect() {
  if ($("#tekval") && $("#tekval").value.trim()) tekCommit(true); // nicht übernommenen Wert mitnehmen
  return state.attrEdit.map((a) => ({ kind: a.kind, name: a.name, vendorId: a.vendorId, code: a.code, type: a.type,
    tagged: !!a.tagged, tag: a.tag || 0, value: a.value }));
}

const attrSection = (list, who) => `
  <div class="section-title" style="margin-top:8px">Check- und Reply-Attribute <span class="muted" style="font-weight:400">(optional)</span></div>
  <p class="muted" style="font-size:12px;margin-bottom:8px"><b class="tek-c">Check</b> = Bedingung, die die Anfrage des Switches/Access Points erfüllen muss (z. B. nur über SSH, nur bestimmte MAC).
    <b class="tek-r">Reply</b> = wird bei Erfolg zurückgeschickt (z. B. Zyxel-Adminrechte, VLAN per 802.1X). ${who}</p>
  ${attrEditorHTML(list)}`;

const attrSummary = (list) => (list || []).map((a) => `${a.kind === "check" ? "Check" : "Reply"}: ${a.name} = ${a.value}`).join("\n");

/* ---------- Benutzer ---------- */
let usersCache = [];
let groupsCache = [];
const groupName = (id) => (groupsCache.find((g) => g.id === id) || {}).name || "";
VIEWS.benutzer = async (main) => {
  let settings;
  [usersCache, groupsCache, settings] = await Promise.all([api("GET", "/api/users"), api("GET", "/api/groups"),
    api("GET", "/api/settings").catch(() => null)]);
  state.eapTls = !!(settings && settings.eap && settings.eap.tls);
  if (state.route !== "benutzer") return;
  main.innerHTML = header("Benutzer", "Personen, die sich am WLAN oder an Geräten anmelden dürfen",
    `<div class="search">${icon("search")}<input type="search" id="usearch" placeholder="Suchen …"></div>
     <a class="btn" href="/api/users.csv" download title="Als CSV-Datei herunterladen">${icon("download")} Export</a>
     ${adm(`<button class="btn" data-action="importUsers" title="Aus CSV-Datei übernehmen">${icon("upload")} Import</button>
     <button class="btn" data-action="testUser">${icon("key")} Anmeldung testen</button>
     <button class="btn primary" data-action="newUser">${icon("plus")} Benutzer hinzufügen</button>`)}`) +
    `<div class="card flush"><div class="table-wrap" id="utable"></div></div>`;
  $("#usearch").oninput = drawUsers;
  drawUsers();
};

function drawUsers() {
  const q = ($("#usearch")?.value || "").toLowerCase();
  const list = usersCache.filter((u) => !q || [u.username, u.fullName, u.note, u.vlan, groupName(u.groupId)].join(" ").toLowerCase().includes(q));
  const el = $("#utable");
  if (!usersCache.length) {
    el.innerHTML = `<div class="empty">${icon("users")}<h3>Noch keine Benutzer</h3>${adm(`<p>Lege den ersten Benutzer an – er kann sich danach sofort am WLAN anmelden.</p>
      <button class="btn primary" data-action="newUser">${icon("plus")} Benutzer hinzufügen</button>`)}</div>`;
    return;
  }
  el.innerHTML = `<table><thead><tr><th>Benutzer</th><th>Status</th><th>Gruppe</th><th>VLAN</th><th>Läuft ab</th><th>Letzte Anmeldung</th><th></th></tr></thead><tbody>
    ${list.map((u) => `<tr>
      <td><div class="who">${esc(u.username)}${u.fullName || u.note ? `<small>${esc(u.fullName)}${u.fullName && u.note ? " · " : ""}${esc(u.note)}</small>` : ""}</div></td>
      <td>${!u.enabled ? `<span class="badge">Deaktiviert</span>` : u.expired ? `<span class="badge bad">Abgelaufen</span>` : `<span class="badge ok"><span class="dot"></span>Aktiv</span>`}</td>
      <td>${u.groupId ? esc(groupName(u.groupId)) : `<span class="muted">–</span>`}${(u.attributes || []).length ? `<br><span class="badge" title="${esc(attrSummary(u.attributes))}">+${u.attributes.length} Attribut${u.attributes.length > 1 ? "e" : ""}</span>` : ""}</td>
      <td>${u.vlan ? `<span class="badge info">VLAN ${esc(u.vlan)}</span>` : (() => { const g = groupsCache.find((x) => x.id === u.groupId); return g && g.vlan ? `<span class="badge info" title="von der Gruppe">VLAN ${esc(g.vlan)}</span>` : `<span class="muted">Standard</span>`; })()}</td>
      <td>${u.expiresAt ? esc(new Date(u.expiresAt).toLocaleDateString("de-DE")) : `<span class="muted">nie</span>`}</td>
      <td class="muted">${fmtRel(u.lastLoginAt)}</td>
      <td class="actions">${adm(`
        ${state.eapTls ? `<button class="btn ghost sm" data-action="userCert" data-id="${esc(u.id)}" title="Zertifikat (EAP-TLS)">${icon("shield")}</button>` : ""}
        <button class="btn ghost sm" data-action="editUser" data-id="${esc(u.id)}" title="Bearbeiten">${icon("edit")}</button>
        <button class="btn ghost sm" data-action="deleteUser" data-id="${esc(u.id)}" title="Löschen">${icon("trash")}</button>`)}</td>
    </tr>`).join("")}
    ${list.length ? "" : `<tr><td colspan="7" class="muted" style="text-align:center;padding:30px">Keine Treffer</td></tr>`}
  </tbody></table>`;
}

function userDialog(u = { enabled: true }) {
  const isNew = !u.id;
  openModal(isNew ? "Benutzer hinzufügen" : "Benutzer bearbeiten", `
    <div class="two">
      <label class="f"><span>Benutzername</span><input type="text" name="username" value="${esc(u.username)}" autocomplete="off" required></label>
      <label class="f"><span>Name <span class="muted">(optional)</span></span><input type="text" name="fullName" value="${esc(u.fullName)}"></label>
    </div>
    <label class="f"><span>${isNew ? "Passwort" : "Neues Passwort"}</span>
      <div class="input-group"><input type="password" name="password" autocomplete="new-password" placeholder="${isNew ? "" : "leer lassen = unverändert"}">
      <button type="button" class="btn" data-action="reveal" title="Anzeigen">${icon("eye")}</button>
      <button type="button" class="btn" data-action="genpw" title="Zufällig erzeugen">${icon("dice")}</button></div></label>
    <label class="f"><span>Gruppe</span><select name="groupId"><option value="">Keine Gruppe</option>
      ${groupsCache.map((g) => `<option value="${esc(g.id)}" ${g.id === u.groupId ? "selected" : ""}>${esc(g.name)}${g.vlan ? " (VLAN " + esc(g.vlan) + ")" : ""}</option>`).join("")}</select>
      <small>Der Benutzer übernimmt VLAN und Regeln der Gruppe.</small></label>
    <div class="two">
      <label class="f"><span>Eigenes VLAN <span class="muted">(optional)</span></span><input type="number" name="vlan" min="1" max="4094" value="${esc(u.vlan)}" placeholder="z. B. 20">
        <small>Hat Vorrang vor dem VLAN der Gruppe.</small></label>
      <label class="f"><span>Gültig bis <span class="muted">(optional)</span></span><input type="date" name="expiresAt" value="${esc(u.expiresAt)}">
        <small>Praktisch für Gäste.</small></label>
    </div>
    <label class="f"><span>Notiz</span><input type="text" name="note" value="${esc(u.note)}" placeholder="z. B. Laptop Büro"></label>
    <label class="switch"><input type="checkbox" name="enabled" ${u.enabled ? "checked" : ""}> Benutzer darf sich anmelden</label>
    ${attrSection(u.attributes, "Gleichnamige Attribute der Gruppe werden dadurch ersetzt.")}`,
  {
    submit: isNew ? "Anlegen" : "Speichern",
    wide: true,
    onSubmit: async (fd) => {
      const body = Object.fromEntries(fd);
      body.enabled = fd.has("enabled");
      body.attributes = attrCollect();
      if (isNew && !body.password) throw new Error("Bitte ein Passwort vergeben (oder mit dem Würfel erzeugen).");
      if (body.password) {
        const pw = body.password;
        await api(isNew ? "POST" : "PUT", isNew ? "/api/users" : "/api/users/" + u.id, body);
        showCredentials(body.username, pw);
      } else {
        await api("PUT", "/api/users/" + u.id, body);
        toast("Gespeichert");
      }
      VIEWS.benutzer($(".main"));
      return !!body.password;
    },
  });
  attrEditorMount();
}

function showCredentials(user, pw) {
  openModal("Zugangsdaten", `
    <div class="ok-box">Gespeichert. Das Passwort wird aus Sicherheitsgründen nur jetzt angezeigt.</div>
    <dl class="kv"><dt>Benutzername</dt><dd><b class="mono copy" data-action="copy" data-value="${esc(user)}">${esc(user)}</b></dd>
    <dt>Passwort</dt><dd><b class="mono copy" data-action="copy" data-value="${esc(pw)}">${esc(pw)}</b></dd></dl>
    <p class="muted" style="margin-top:14px">Beim Verbinden mit dem WLAN: Methode <b>PEAP</b>, innere Authentifizierung <b>MSCHAPv2</b>. Eine Anleitung für jedes Gerät gibt es unter <a href="/einrichten" target="_blank">/einrichten</a> (QR-Code unter „Zertifikat“).</p>`, {});
}

ACTIONS.newUser = async () => { await loadAttrDict(); userDialog(); };
ACTIONS.editUser = async (el) => { await loadAttrDict(); userDialog(usersCache.find((u) => u.id === el.dataset.id)); };
ACTIONS.deleteUser = (el) => {
  const u = usersCache.find((x) => x.id === el.dataset.id);
  confirmDialog("Benutzer löschen", `Soll <b>${esc(u.username)}</b> wirklich gelöscht werden? Der Benutzer kann sich danach nicht mehr anmelden.`, "Löschen", async () => {
    await api("DELETE", "/api/users/" + u.id);
    toast("Benutzer gelöscht");
    VIEWS.benutzer($(".main"));
  });
};
ACTIONS.testUser = () => openModal("Anmeldung testen", `
  <p class="muted">Prüft Benutzername und Passwort genau wie bei einer echten Anmeldung – lokal und im Active Directory.</p>
  <label class="f"><span>Benutzername</span><input type="text" name="username" autocomplete="off" placeholder="max oder FIRMA\\max"></label>
  <label class="f"><span>Passwort</span><input type="password" name="password" autocomplete="off"></label>
  <div id="testres"></div>`,
{
  submit: "Testen",
  onSubmit: async (fd, form) => {
    const r = await api("POST", "/api/users/test", Object.fromEntries(fd));
    $(".msg", form).innerHTML = `<div class="${r.ok ? "ok-box" : "error-box"}"><div>${esc(r.message)}</div></div>`;
    $("#testres").innerHTML = r.source ? `<dl class="kv" style="grid-template-columns:140px 1fr">
      <dt>Quelle</dt><dd>${esc(r.source)}</dd>
      ${r.ok ? `<dt>Gruppe</dt><dd>${r.group ? esc(r.group) : '<span class="muted">keine</span>'}</dd><dt>VLAN</dt><dd>${r.vlan ? esc(r.vlan) : '<span class="muted">Standard</span>'}</dd>` : ""}
      <dt>WLAN (PEAP)</dt><dd>${r.wlanOk ? '<span class="badge ok">funktioniert</span>' : `<span class="badge bad">nicht möglich</span> <span class="muted">${esc(r.wlanNote || "")}</span>`}</dd>
      ${r.groups && r.groups.length ? `<dt>AD-Gruppen</dt><dd style="font-size:12px">${r.groups.map(esc).join("<br>")}</dd>` : ""}
    </dl>` : "";
    return true;
  },
});

/* CSV-Import (Benutzer und MAC-Adressen) */
function importDialog(title, url, columns, notes, reload) {
  openModal(title, `
    <p class="muted">CSV-Datei mit den Spaltennamen in der ersten Zeile. Trennzeichen Semikolon oder Komma – so, wie Excel speichert.</p>
    <div class="callout" style="margin-bottom:16px"><div style="font-weight:600;margin-bottom:4px">Spalten</div>
      <div class="mono" style="font-size:12px;overflow-wrap:anywhere">${columns.map(esc).join(";")}</div>
      <div class="muted" style="font-size:12px;margin-top:6px">${notes}</div></div>
    <label class="f"><span>Datei</span><input type="file" name="file" accept=".csv,text/csv"></label>`,
  {
    submit: "Importieren",
    onSubmit: async (fd) => {
      const f = fd.get("file");
      if (!f || !f.size) throw new Error("Bitte eine CSV-Datei auswählen.");
      const r = await api("POST", url, fd);
      toast(`${r.created} neu, ${r.updated} aktualisiert`);
      reload();
    },
  });
}

ACTIONS.importUsers = () => importDialog("Benutzer importieren", "/api/users/import",
  ["Benutzername", "Name", "Passwort", "Gruppe", "VLAN", "Aktiv", "Läuft ab", "Notiz"],
  `Pflicht ist nur <b>Benutzername</b>. Neue Benutzer brauchen ein Passwort – bei vorhandenen bleibt es ohne Eintrag unverändert.
   Gruppe per Name, Aktiv = ja/nein, Datum als JJJJ-MM-TT. Englische Spaltennamen (username, password, group …) gehen auch.
   Bei einem Fehler wird nichts übernommen; die Meldung nennt die Zeile.`,
  () => VIEWS.benutzer($(".main")));

/* Benutzerzertifikat für EAP-TLS */
ACTIONS.userCert = (el) => {
  const u = usersCache.find((x) => x.id === el.dataset.id);
  if (!u) return;
  openModal("Zertifikat (EAP-TLS)", `
    <p class="muted">Erstellt ein Benutzerzertifikat für <b>${esc(u.username)}</b>. Damit meldet sich das Gerät per EAP-TLS ohne Passwort am WLAN an.
      Die Datei (.p12) enthält Zertifikat und privaten Schlüssel – nur an diese Person weitergeben.</p>
    <label class="f"><span>Gültigkeit in Tagen</span><input type="number" name="days" min="1" max="3650" value="365"></label>
    <label class="f"><span>Passwort für die Datei</span>
      <div class="input-group"><input type="password" name="password" autocomplete="new-password">
      <button type="button" class="btn" data-action="reveal" title="Anzeigen">${icon("eye")}</button>
      <button type="button" class="btn" data-action="genpw" title="Zufällig erzeugen">${icon("dice")}</button></div>
      <small>Mindestens 6 Zeichen. Wird beim Installieren auf dem Gerät abgefragt.</small></label>`,
  {
    submit: "Erstellen und herunterladen",
    onSubmit: async (fd) => {
      const days = +fd.get("days"), password = String(fd.get("password") || "");
      if (!(days >= 1 && days <= 3650)) throw new Error("Gültigkeit: 1 bis 3650 Tage.");
      if (password.length < 6) throw new Error("Das Passwort braucht mindestens 6 Zeichen.");
      try {
        await download("/api/users/" + encodeURIComponent(u.id) + "/certificate", { days, password }, u.username + ".p12");
      } catch (err) { throw notAvailable(err) ? new Error(NOT_AVAILABLE) : err; }
      toast("Zertifikat erstellt");
    },
  });
};

/* ---------- MAC-Adressen (MAB) ---------- */
let macsCache = [];
VIEWS.mac = async (main) => {
  [macsCache, groupsCache] = await Promise.all([api("GET", "/api/macs"), api("GET", "/api/groups")]);
  if (state.route !== "mac") return;
  main.innerHTML = header("MAC-Adressen", "Drucker, Telefone, Kameras und andere Geräte ohne 802.1X – Anmeldung über die MAC-Adresse (MAB)",
    `<div class="search">${icon("search")}<input type="search" id="msearch" placeholder="Suchen …"></div>
     <a class="btn" href="/api/macs.csv" download title="Als CSV-Datei herunterladen">${icon("download")} Export</a>
     ${adm(`<button class="btn" data-action="importMacs" title="Aus CSV-Datei übernehmen">${icon("upload")} Import</button>
     <button class="btn primary" data-action="newMac">${icon("plus")} MAC-Adresse hinzufügen</button>`)}`) +
    `<div class="card flush"><div class="table-wrap" id="mtable"></div></div>
    <div class="card"><h2>So richtest du MAB am Switch ein</h2>
      <ol class="steps">
        <li><span class="s-text">Den Switch (oder Access Point) unter <a href="#/geraete">Geräte</a> freigeben.</span></li>
        <li><span class="s-text">Am Port die MAC-Authentifizierung einschalten (MAB, „MAC-based Authentication“) und diesen Server als RADIUS-Server eintragen.</span></li>
        <li><span class="s-text">Hier die MAC-Adresse eintragen. Der Switch schickt sie als Benutzername – EasyRadius versteht alle üblichen Schreibweisen.</span></li>
      </ol>
      <p class="muted" style="margin:8px 0 0;font-size:12px">Beispiel Cisco am Port: <span class="mono">authentication port-control auto</span> und <span class="mono">mab</span>.
        Mit VLAN oder Gruppe landet das Gerät im passenden Netz. MAC-Adressen lassen sich fälschen – MAB nur für Geräte nutzen, die kein 802.1X können.</p></div>`;
  $("#msearch").oninput = drawMacs;
  drawMacs();
};

function drawMacs() {
  const el = $("#mtable");
  if (!el) return;
  const q = ($("#msearch")?.value || "").toLowerCase().trim();
  const qMac = q.replace(/[^0-9a-f]/g, "");
  const list = macsCache.filter((m) => {
    if (!q) return true;
    const hay = [m.mac, m.name, m.note, m.vlan, groupName(m.groupId)].join(" ").toLowerCase();
    return hay.includes(q) || (qMac.length >= 2 && String(m.mac).replace(/[^0-9a-f]/gi, "").toLowerCase().includes(qMac));
  });
  if (!macsCache.length) {
    el.innerHTML = `<div class="empty">${icon("printer")}<h3>Noch keine MAC-Adressen</h3><p>Für Geräte, die sich nicht selbst per 802.1X anmelden können – der Switch prüft dann nur die MAC-Adresse.</p>
      ${adm(`<button class="btn primary" data-action="newMac">${icon("plus")} MAC-Adresse hinzufügen</button>`)}</div>`;
    return;
  }
  el.innerHTML = `<table><thead><tr><th>MAC-Adresse</th><th>Name</th><th>Status</th><th>Gruppe</th><th>VLAN</th><th>Zuletzt gesehen</th><th></th></tr></thead><tbody>
    ${list.map((m) => `<tr>
      <td class="mono">${esc(m.mac)}</td>
      <td><div class="who">${m.name ? esc(m.name) : `<span class="muted">–</span>`}${m.note ? `<small>${esc(m.note)}</small>` : ""}</div></td>
      <td>${!m.enabled ? `<span class="badge">Deaktiviert</span>` : m.expired ? `<span class="badge bad">Abgelaufen</span>` : `<span class="badge ok"><span class="dot"></span>Aktiv</span>`}
        ${m.expiresAt && !m.expired ? `<br><small class="muted">bis ${esc(fmtDay(m.expiresAt))}</small>` : ""}</td>
      <td>${m.groupId ? esc(groupName(m.groupId)) : `<span class="muted">–</span>`}${(m.attributes || []).length ? `<br><span class="badge" title="${esc(attrSummary(m.attributes))}">+${m.attributes.length} Attribut${m.attributes.length > 1 ? "e" : ""}</span>` : ""}</td>
      <td>${m.vlan ? `<span class="badge info">VLAN ${esc(m.vlan)}</span>` : (() => { const g = groupsCache.find((x) => x.id === m.groupId); return g && g.vlan ? `<span class="badge info" title="von der Gruppe">VLAN ${esc(g.vlan)}</span>` : `<span class="muted">Standard</span>`; })()}</td>
      <td class="muted">${fmtRel(m.lastSeen)}</td>
      <td class="actions">${adm(`<button class="btn ghost sm" data-action="editMac" data-id="${esc(m.id)}" title="Bearbeiten">${icon("edit")}</button>
        <button class="btn ghost sm" data-action="deleteMac" data-id="${esc(m.id)}" title="Löschen">${icon("trash")}</button>`)}</td>
    </tr>`).join("")}
    ${list.length ? "" : `<tr><td colspan="7" class="muted" style="text-align:center;padding:30px">Keine Treffer</td></tr>`}
  </tbody></table>`;
}

function macDialog(m = { enabled: true }) {
  const isNew = !m.id;
  openModal(isNew ? "MAC-Adresse hinzufügen" : "MAC-Adresse bearbeiten", `
    <div class="two">
      <label class="f"><span>MAC-Adresse</span><input type="text" name="mac" value="${esc(m.mac)}" placeholder="aa-bb-cc-dd-ee-ff" autocomplete="off" spellcheck="false">
        <small>Jede übliche Schreibweise, z. B. AA:BB:CC:DD:EE:FF oder aabb.ccdd.eeff.</small></label>
      <label class="f"><span>Name <span class="muted">(optional)</span></span><input type="text" name="name" value="${esc(m.name)}" placeholder="z. B. Drucker Flur"></label>
    </div>
    <label class="f"><span>Gruppe</span><select name="groupId"><option value="">Keine Gruppe</option>
      ${groupsCache.map((g) => `<option value="${esc(g.id)}" ${g.id === m.groupId ? "selected" : ""}>${esc(g.name)}${g.vlan ? " (VLAN " + esc(g.vlan) + ")" : ""}</option>`).join("")}</select>
      <small>Das Gerät übernimmt VLAN und Regeln der Gruppe.</small></label>
    <div class="two">
      <label class="f"><span>Eigenes VLAN <span class="muted">(optional)</span></span><input type="number" name="vlan" min="1" max="4094" value="${esc(m.vlan)}" placeholder="z. B. 40">
        <small>Hat Vorrang vor dem VLAN der Gruppe.</small></label>
      <label class="f"><span>Gültig bis <span class="muted">(optional)</span></span><input type="date" name="expiresAt" value="${esc(m.expiresAt)}"></label>
    </div>
    <label class="f"><span>Notiz</span><input type="text" name="note" value="${esc(m.note)}" placeholder="z. B. Raum 2.14"></label>
    <label class="switch"><input type="checkbox" name="enabled" ${m.enabled ? "checked" : ""}> Gerät darf sich anmelden</label>
    ${attrSection(m.attributes, "Gleichnamige Attribute der Gruppe werden dadurch ersetzt.")}`,
  {
    submit: isNew ? "Anlegen" : "Speichern",
    wide: true,
    onSubmit: async (fd) => {
      const body = {
        mac: String(fd.get("mac") || "").trim(), name: fd.get("name"), enabled: fd.has("enabled"), groupId: fd.get("groupId"),
        vlan: fd.get("vlan"), expiresAt: fd.get("expiresAt"), note: fd.get("note"), attributes: attrCollect(),
      };
      if (!body.mac) throw new Error("Bitte die MAC-Adresse eintragen.");
      await api(isNew ? "POST" : "PUT", isNew ? "/api/macs" : "/api/macs/" + m.id, body);
      toast(isNew ? "MAC-Adresse angelegt" : "Gespeichert");
      VIEWS.mac($(".main"));
    },
  });
  attrEditorMount();
}
ACTIONS.newMac = async () => { await loadAttrDict(); macDialog(); };
ACTIONS.editMac = async (el) => { await loadAttrDict(); macDialog(macsCache.find((m) => m.id === el.dataset.id)); };
ACTIONS.deleteMac = (el) => {
  const m = macsCache.find((x) => x.id === el.dataset.id);
  if (!m) return;
  confirmDialog("MAC-Adresse löschen", `Soll <b class="mono">${esc(m.mac)}</b>${m.name ? ` (${esc(m.name)})` : ""} gelöscht werden? Das Gerät wird danach am Switch abgewiesen.`, "Löschen", async () => {
    await api("DELETE", "/api/macs/" + m.id);
    toast("MAC-Adresse gelöscht");
    VIEWS.mac($(".main"));
  });
};
ACTIONS.importMacs = () => importDialog("MAC-Adressen importieren", "/api/macs/import",
  ["MAC", "Name", "Gruppe", "VLAN", "Aktiv", "Läuft ab", "Notiz"],
  `Pflicht ist nur <b>MAC</b> (jede übliche Schreibweise). Vorhandene Adressen werden aktualisiert.
   Gruppe per Name, Aktiv = ja/nein, Datum als JJJJ-MM-TT. Bei einem Fehler wird nichts übernommen; die Meldung nennt die Zeile.`,
  () => VIEWS.mac($(".main")));

/* ---------- Gruppen ---------- */
VIEWS.gruppen = async (main) => {
  const [groups, clients, ad] = await Promise.all([api("GET", "/api/groups"), api("GET", "/api/clients"), api("GET", "/api/ad")]);
  groupsCache = groups; clientsCache = clients; state.adMode = ad.mode;
  if (state.route !== "gruppen") return;
  const clientName = (id) => (clients.find((c) => c.id === id) || {}).name || "?";
  main.innerHTML = header("Gruppen", "Regeln für mehrere Benutzer auf einmal – VLAN, Sperre, erlaubte WLANs und Laufzeit",
    adm(`<button class="btn primary" data-action="newGroup">${icon("plus")} Gruppe hinzufügen</button>`)) +
    `<div class="card flush"><div class="table-wrap">${groups.length ? `<table><thead><tr><th>Gruppe</th><th>Status</th><th>Mitglieder</th><th>VLAN</th><th>Einschränkungen</th><th></th></tr></thead><tbody>
    ${groups.map((g) => {
      const limits = [
        g.allowedSsids.length ? "WLAN: " + g.allowedSsids.map(esc).join(", ") : "",
        g.allowedClients.length ? "Geräte: " + g.allowedClients.map((id) => esc(clientName(id))).join(", ") : "",
        g.expiresAt ? "bis " + esc(new Date(g.expiresAt).toLocaleDateString("de-DE")) : "",
        g.adGroups.length ? "AD: " + g.adGroups.map(esc).join(", ") : "",
        (g.attributes || []).length ? (g.attributes || []).map((a) => `<span class="${a.kind === "check" ? "tek-c" : "tek-r"}">${a.kind === "check" ? "Check" : "Reply"}</span> ${esc(a.name)} = ${esc(a.value)}`).join("<br>") : "",
      ].filter(Boolean);
      return `<tr>
      <td><div class="who">${esc(g.name)}${g.description ? `<small>${esc(g.description)}</small>` : ""}</div></td>
      <td>${!g.enabled ? `<span class="badge bad">Gesperrt</span>` : g.expired ? `<span class="badge bad">Abgelaufen</span>` : `<span class="badge ok"><span class="dot"></span>Aktiv</span>`}</td>
      <td>${g.members}</td>
      <td>${g.vlan ? `<span class="badge info">VLAN ${esc(g.vlan)}</span>` : `<span class="muted">Standard</span>`}</td>
      <td class="muted" style="font-size:12px">${limits.length ? limits.join("<br>") : "keine"}</td>
      <td class="actions">${adm(`
        <button class="btn ghost sm" data-action="toggleGroup" data-id="${esc(g.id)}" title="${g.enabled ? "Sperren" : "Entsperren"}">${icon(g.enabled ? "x" : "check")}</button>
        <button class="btn ghost sm" data-action="editGroup" data-id="${esc(g.id)}" title="Bearbeiten">${icon("edit")}</button>
        <button class="btn ghost sm" data-action="deleteGroup" data-id="${esc(g.id)}" title="Löschen">${icon("trash")}</button>`)}</td></tr>`;
    }).join("")}</tbody></table>`
    : `<div class="empty">${icon("team")}<h3>Noch keine Gruppen</h3><p>Mit Gruppen steuerst du VLAN, Zugang und Laufzeit für viele Benutzer gleichzeitig.</p>
       ${adm(`<button class="btn primary" data-action="newGroup">${icon("plus")} Gruppe hinzufügen</button>`)}</div>`}</div></div>
    <div class="callout" style="margin-top:12px">Ein Benutzer gehört zu einer Gruppe. Eigene Werte am Benutzer (z. B. VLAN) haben Vorrang.
      ${ad.mode !== "off" ? "AD-Benutzer erhalten die erste passende Gruppe (alphabetisch), deren verknüpfte AD-Gruppe sie angehören." : ""}</div>`;
};

function groupDialog(g = { enabled: true, allowedClients: [], allowedSsids: [], adGroups: [], attributes: [] }) {
  const isNew = !g.id;
  openModal(isNew ? "Gruppe hinzufügen" : "Gruppe bearbeiten", `
    <label class="f"><span>Name</span><input type="text" name="name" value="${esc(g.name)}" placeholder="z. B. Mitarbeiter"></label>
    <label class="f"><span>Beschreibung <span class="muted">(optional)</span></span><input type="text" name="description" value="${esc(g.description)}"></label>
    <div class="two">
      <label class="f"><span>VLAN <span class="muted">(optional)</span></span><input type="number" name="vlan" min="1" max="4094" value="${esc(g.vlan)}" placeholder="z. B. 30"></label>
      <label class="f"><span>Gültig bis <span class="muted">(optional)</span></span><input type="date" name="expiresAt" value="${esc(g.expiresAt)}"></label>
    </div>
    <label class="f"><span>Erlaubte WLANs (SSID) <span class="muted">(optional)</span></span><input type="text" name="allowedSsids" value="${esc(g.allowedSsids.join(", "))}" placeholder="leer = alle, z. B. Firma, Firma-5G">
      <small>Mehrere mit Komma trennen. Der Access Point muss die SSID mitsenden (Called-Station-Id).</small></label>
    <div class="f" style="margin-bottom:16px"><span style="display:block;margin-bottom:8px">Erlaubte Geräte <span class="muted">(keins gewählt = alle)</span></span>
      ${clientsCache.length ? clientsCache.map((c) => `<label class="switch" style="margin-bottom:8px"><input type="checkbox" name="allowedClients" value="${esc(c.id)}" ${g.allowedClients.includes(c.id) ? "checked" : ""}> ${esc(c.name)} <span class="muted mono" style="font-size:12px">${esc(c.address)}</span></label>`).join("") : `<span class="muted">Noch keine Geräte angelegt.</span>`}</div>
    ${state.adMode && state.adMode !== "off" ? `<label class="f"><span>Verknüpfte AD-Gruppen</span><input type="text" name="adGroups" value="${esc(g.adGroups.join(", "))}" placeholder="z. B. WLAN-Mitarbeiter">
      <small>Mitglieder dieser AD-Gruppen (auch verschachtelt) erhalten die Regeln dieser Gruppe.</small></label>` : `<input type="hidden" name="adGroups" value="${esc(g.adGroups.join(", "))}">`}
    <label class="switch"><input type="checkbox" name="enabled" ${g.enabled ? "checked" : ""}> Gruppe ist aktiv (aus = alle Mitglieder gesperrt)</label>
    ${attrSection(g.attributes, "Gelten für alle Mitglieder (auch AD-Benutzer über verknüpfte AD-Gruppen).")}`,
  {
    submit: isNew ? "Anlegen" : "Speichern",
    wide: true,
    onSubmit: async (fd) => {
      const split = (v) => String(v || "").split(",").map((x) => x.trim()).filter(Boolean);
      const body = {
        name: fd.get("name"), description: fd.get("description"), vlan: fd.get("vlan"), expiresAt: fd.get("expiresAt"),
        enabled: fd.has("enabled"), allowedSsids: split(fd.get("allowedSsids")), allowedClients: fd.getAll("allowedClients"),
        adGroups: split(fd.get("adGroups")), attributes: attrCollect(),
      };
      await api(isNew ? "POST" : "PUT", isNew ? "/api/groups" : "/api/groups/" + g.id, body);
      toast(isNew ? "Gruppe angelegt" : "Gespeichert");
      render();
    },
  });
  attrEditorMount();
}
ACTIONS.newGroup = async () => { await loadAttrDict(); groupDialog(); };
ACTIONS.editGroup = async (el) => { await loadAttrDict(); groupDialog(groupsCache.find((g) => g.id === el.dataset.id)); };
ACTIONS.toggleGroup = async (el) => {
  const g = groupsCache.find((x) => x.id === el.dataset.id);
  await api("PUT", "/api/groups/" + g.id, { ...g, enabled: !g.enabled });
  toast(g.enabled ? `Gruppe „${g.name}“ gesperrt` : `Gruppe „${g.name}“ entsperrt`);
  render();
};
ACTIONS.deleteGroup = (el) => {
  const g = groupsCache.find((x) => x.id === el.dataset.id);
  confirmDialog("Gruppe löschen", `Soll die Gruppe <b>${esc(g.name)}</b> gelöscht werden? ${g.members ? `Die ${g.members} Mitglieder bleiben erhalten, sind aber keiner Gruppe mehr zugeordnet.` : ""}`, "Löschen", async () => {
    await api("DELETE", "/api/groups/" + g.id);
    toast("Gruppe gelöscht");
    render();
  });
};

/* ---------- Active Directory ---------- */
VIEWS.ad = async (main) => {
  const a = await api("GET", "/api/ad");
  if (state.route !== "ad") return;
  state.adMode = a.mode;
  const modeText = { off: "Aus", domain: "Domäne " + (a.domain || ""), ldap: "LDAP" }[a.mode];
  main.innerHTML = header("Active Directory", "Benutzer aus dem Firmen-AD anmelden lassen") + `
    ${a.status ? `<div class="callout warn"><h3>${icon("alert")} AD nicht verfügbar</h3>${esc(a.status)}</div>` : ""}
    <div class="card"><div class="row" style="gap:16px">
      <span class="stat"><span class="ico" style="width:48px;height:48px">${icon("building")}</span></span>
      <div style="flex:1"><div style="font:600 20px/28px 'Segoe UI Variable Display','Segoe UI',sans-serif">${esc(modeText)}</div>
        <div class="muted">${a.platform !== "windows" ? "EasyRadius läuft unter Linux – Active Directory ist hier per LDAP möglich." : a.joined ? `Dieser Rechner ist Mitglied der Domäne <b>${esc(a.domain)}</b>.` : "Dieser Rechner ist keiner Domäne beigetreten."}</div></div>
      ${a.mode !== "off" ? adm(`<button class="btn" data-action="testUser">${icon("key")} Anmeldung testen</button>`) : ""}
    </div></div>
    <form id="adform" novalidate><div class="msg" style="margin-top:12px"></div><fieldset class="plain" ${isAdmin() ? "" : "disabled"}>
    <div class="section-title">Anbindung</div>
    <div class="card">
      <label class="switch"><input type="radio" name="mode" value="off" ${a.mode === "off" ? "checked" : ""} style="border-radius:50%"> <span><b>Aus</b> – nur lokale Benutzer</span></label>
      <label class="switch"><input type="radio" name="mode" value="domain" ${a.mode === "domain" ? "checked" : ""} ${a.joined ? "" : "disabled"} style="border-radius:50%">
        <span><b>Domänenmitglied</b> (empfohlen) – WLAN und Geräte-Logins mit AD-Passwort, Gruppen inkl. Verschachtelung. ${a.joined ? "" : a.platform !== "windows" ? '<span class="muted">Nur wenn EasyRadius unter Windows läuft.</span>' : '<span class="muted">Erst nach Domänenbeitritt verfügbar.</span>'}</span></label>
      <label class="switch" style="margin-bottom:0"><input type="radio" name="mode" value="ldap" ${a.mode === "ldap" ? "checked" : ""} style="border-radius:50%">
        <span><b>LDAP</b> – ohne Domänenbeitritt. Nur für Switch-/Router-/VPN-Logins (PAP), <b>nicht für WLAN</b>.</span></label>
    </div>
    <div id="ldapbox" style="${a.mode === "ldap" ? "" : "display:none"}">
      <div class="section-title">LDAP-Verbindung</div>
      ${setting("globe", "Server", "z. B. ldaps://dc01.firma.local (Port 636) oder ldap://… mit StartTLS", `<input type="text" name="ldapUrl" value="${esc(a.ldapUrl)}" placeholder="ldaps://dc01.firma.local" style="width:280px">`)}
      ${setting("list", "Basis-DN", "Ab hier wird nach Benutzern gesucht", `<input type="text" name="baseDn" value="${esc(a.baseDn)}" placeholder="DC=firma,DC=local" style="width:280px">`)}
      ${setting("users", "Dienstkonto", "Konto mit Leserechten im AD", `<input type="text" name="bindUser" value="${esc(a.bindUser)}" placeholder="svc-radius@firma.local" style="width:280px">`)}
      ${setting("key", "Passwort des Dienstkontos", a.hasBindPassword ? "Gespeichert – leer lassen, um es zu behalten" : "", `<input type="password" name="bindPassword" autocomplete="new-password" style="width:280px">`)}
      ${setting("edit", "Anmeldename-Attribut", "Standard: sAMAccountName", `<input type="text" name="userAttribute" value="${esc(a.userAttribute)}" style="width:180px">`)}
      ${setting("shield", "StartTLS verwenden", "Nur bei ldap://-Adressen", `<label class="switch" style="margin:0"><input type="checkbox" name="startTls" ${a.startTls ? "checked" : ""}></label>`)}
      ${setting("alert", "Zertifikat des Domänencontrollers nicht prüfen", "Nur zum Testen – unsicher", `<label class="switch" style="margin:0"><input type="checkbox" name="skipVerify" ${a.skipVerify ? "checked" : ""}></label>`)}
    </div>
    <div class="section-title">Regeln</div>
    ${setting("team", "Nur AD-Benutzer mit verknüpfter Gruppe zulassen", "Empfohlen. Verknüpfung unter „Gruppen“ → AD-Gruppen. Sonst darf sich jedes AD-Konto anmelden.",
      `<label class="switch" style="margin:0"><input type="checkbox" name="requireGroup" ${a.requireGroup ? "checked" : ""}></label>`)}
    ${adm(`<div class="row" style="justify-content:flex-end;margin-top:12px"><button class="btn primary" type="submit">Speichern</button></div>`)}
    </fieldset></form>
    <div class="callout" style="margin-top:16px"><h3>Hinweise</h3>
      Lokale Benutzer haben immer Vorrang vor gleichnamigen AD-Konten. Anmeldenamen dürfen als <span class="mono">name</span>, <span class="mono">FIRMA\\name</span> oder <span class="mono">name@firma.local</span> eingegeben werden.
      ${a.joined || a.platform !== "windows" ? "" : "<br>Für WLAN mit AD-Passwörtern muss der Rechner der Domäne beitreten (Einstellungen → Konten → Auf Arbeits- oder Schulkonto zugreifen → „Diesen Computer einer lokalen Active Directory-Domäne hinzufügen“). Danach EasyRadius-Dienst neu starten."}</div>`;
  const f = $("#adform");
  f.querySelectorAll("input[name=mode]").forEach((r) => r.onchange = () => { $("#ldapbox").style.display = f.mode.value === "ldap" ? "" : "none"; });
  f.onsubmit = async (e) => {
    e.preventDefault();
    const fd = new FormData(f);
    const body = Object.fromEntries(fd);
    ["startTls", "skipVerify", "requireGroup"].forEach((k) => body[k] = fd.has(k));
    try {
      await api("PUT", "/api/ad", body);
      toast("AD-Einstellungen gespeichert");
      render();
    } catch (err) { $(".msg", f).innerHTML = `<div class="error-box"><div>${esc(err.message)}</div></div>`; }
  };
};

/* ---------- Geräte ---------- */
let clientsCache = [];
VIEWS.geraete = async (main) => {
  const [clients, dash] = await Promise.all([api("GET", "/api/clients"), api("GET", "/api/dashboard")]);
  clientsCache = clients;
  if (state.route !== "geraete") return;
  const ip = dash.ips[0] || "–";
  main.innerHTML = header("Geräte", "Access Points, Switches und Router, die beim Server anfragen dürfen",
    adm(`<button class="btn primary" data-action="newClient">${icon("plus")} Gerät hinzufügen</button>`)) +
    `<div class="card flush"><div class="table-wrap">${clients.length ? `<table><thead><tr><th>Name</th><th>IP-Adresse / Netz</th><th>Shared Secret</th><th>Zuletzt gesehen</th><th></th></tr></thead><tbody>
      ${clients.map((c) => `<tr>
        <td><div class="who">${esc(c.name)}${c.note ? `<small>${esc(c.note)}</small>` : ""}</div>
          ${c.requireMA ? "" : `<span class="badge warn" title="Das Gerät muss keinen Message-Authenticator mitsenden – kein Schutz vor Blast-RADIUS. Wenn das Gerät es kann, unter „Bearbeiten“ einschalten.">ohne Blast-RADIUS-Schutz</span>`}</td>
        <td class="mono">${esc(c.address)}</td>
        <td>${c.hasSecret === false ? `<span class="badge bad">fehlt</span>` : `<span class="mono muted">••••••••</span>
          ${adm(`<button class="btn ghost sm" data-action="copySecret" data-id="${esc(c.id)}" title="Kopieren">${icon("copy")}</button>`)}`}</td>
        <td>${c.lastSeen && !c.lastSeen.startsWith("0001") ? `<span class="badge ok">${fmtRel(c.lastSeen)}</span>` : `<span class="badge">noch nie</span>`}</td>
        <td class="actions">${adm(`<button class="btn ghost sm" data-action="editClient" data-id="${esc(c.id)}" title="Bearbeiten">${icon("edit")}</button>
          <button class="btn ghost sm" data-action="deleteClient" data-id="${esc(c.id)}" title="Löschen">${icon("trash")}</button>`)}</td></tr>`).join("")}
      </tbody></table>` : `<div class="empty">${icon("router")}<h3>Noch keine Geräte</h3><p>Füge deinen Access Point (oder Switch) hinzu, damit er Anmeldungen prüfen lassen darf.</p>
        ${adm(`<button class="btn primary" data-action="newClient">${icon("plus")} Gerät hinzufügen</button>`)}</div>`}</div></div>
    <div class="card"><h2>So richtest du deinen Access Point ein</h2>
      <div class="grid g2e">
        <ol class="steps">
          <li><span class="s-text">Hier das Gerät mit seiner IP-Adresse anlegen und ein Shared Secret erzeugen.</span></li>
          <li><span class="s-text">Im WLAN-Profil des Access Points die Sicherheit auf <b>WPA2-Enterprise</b> (oder WPA3-Enterprise) stellen.</span></li>
          <li><span class="s-text">Als RADIUS-Server die Werte rechts eintragen. Accounting ist optional.</span></li>
        </ol>
        <div class="callout"><dl class="kv">
          <dt>RADIUS-Server</dt><dd><b class="mono copy" data-action="copy" data-value="${esc(ip)}">${esc(ip)}</b>${dash.ips.length > 1 ? `<br><small class="muted">weitere: ${dash.ips.slice(1).map(esc).join(", ")}</small>` : ""}</dd>
          <dt>Auth-Port</dt><dd class="mono">${dash.status.authPort}</dd>
          <dt>Accounting-Port</dt><dd class="mono">${dash.status.acctPort}</dd>
          <dt>Shared Secret</dt><dd>wie beim Gerät hinterlegt</dd>
        </dl></div>
      </div>
      <p class="muted" style="margin:12px 0 0;font-size:12px">Neue Geräte müssen den <b>Message-Authenticator</b> mitsenden (Schutz vor Blast-RADIUS). Lehnt EasyRadius die Anfragen eines älteren Geräts deshalb ab, den Schutz bei diesem Gerät ausschalten – besser ist ein Firmware-Update.</p></div>`;
};

function clientDialog(c = {}) {
  const isNew = !c.id;
  openModal(isNew ? "Gerät hinzufügen" : "Gerät bearbeiten", `
    <label class="f"><span>Name</span><input type="text" name="name" value="${esc(c.name)}" placeholder="z. B. Access Point Büro"></label>
    <label class="f"><span>IP-Adresse oder Netz</span><input type="text" name="address" value="${esc(c.address)}" placeholder="192.168.1.10 oder 192.168.1.0/24">
      <small>Die Adresse, von der das Gerät anfragt. Mit einem Netz (CIDR) gibst du mehrere Geräte auf einmal frei.</small></label>
    <label class="f"><span>Shared Secret</span>
      <div class="input-group"><input type="${isNew ? "text" : "password"}" name="secret" value="${isNew ? esc(randomSecret()) : ""}" placeholder="${isNew ? "" : "leer lassen = unverändert"}" autocomplete="off">
      ${isNew ? "" : `<button type="button" class="btn" data-action="loadSecret" data-id="${esc(c.id)}" title="Aktuelles Secret anzeigen">${icon("eye")}</button>`}
      <button type="button" class="btn" data-action="genpw" data-kind="secret" title="Neu erzeugen">${icon("dice")}</button></div>
      <small>Dieses Geheimnis musst du genauso am Gerät eintragen. Mindestens 8 Zeichen.</small></label>
    <label class="switch"><input type="checkbox" name="requireMA" ${isNew || c.requireMA ? "checked" : ""}>
      <span>Message-Authenticator verlangen (Schutz vor Blast-RADIUS)<br><small class="muted">Empfohlen. Nur für ältere Geräte ausschalten, die ihn nicht mitsenden.</small></span></label>
    <div class="two">
      <label class="f"><span>CoA-Port <span class="muted">(optional)</span></span><input type="number" name="coaPort" min="1" max="65535" value="${c.coaPort ? esc(c.coaPort) : ""}" placeholder="3799">
        <small>Hierhin schickt EasyRadius „Sitzung trennen“. Leer = 3799.</small></label>
      <label class="f"><span>Notiz</span><input type="text" name="note" value="${esc(c.note)}"></label>
    </div>`,
  {
    submit: isNew ? "Hinzufügen" : "Speichern",
    onSubmit: async (fd) => {
      const body = { name: fd.get("name"), address: fd.get("address"), secret: fd.get("secret"), note: fd.get("note"),
        requireMA: fd.has("requireMA"), coaPort: parseInt(fd.get("coaPort"), 10) || 0 };
      await api(isNew ? "POST" : "PUT", isNew ? "/api/clients" : "/api/clients/" + c.id, body);
      toast(isNew ? "Gerät hinzugefügt" : "Gespeichert");
      if (body.secret) setTimeout(() => copyText(body.secret), 50);
      render();
    },
  });
}
const fetchSecret = async (id) => (await api("GET", "/api/clients/" + encodeURIComponent(id) + "/secret")).secret || "";
ACTIONS.copySecret = async (el) => copyText(await fetchSecret(el.dataset.id));
ACTIONS.loadSecret = async (el) => {
  const inp = el.closest(".input-group").querySelector("input");
  inp.value = await fetchSecret(el.dataset.id);
  inp.type = "text";
};
ACTIONS.newClient = () => clientDialog();
ACTIONS.editClient = (el) => clientDialog(clientsCache.find((c) => c.id === el.dataset.id));
ACTIONS.deleteClient = (el) => {
  const c = clientsCache.find((x) => x.id === el.dataset.id);
  confirmDialog("Gerät entfernen", `Soll <b>${esc(c.name)}</b> entfernt werden? Anfragen von ${esc(c.address)} werden danach ignoriert.`, "Entfernen", async () => {
    await api("DELETE", "/api/clients/" + c.id);
    toast("Gerät entfernt");
    render();
  });
};

/* ---------- Protokoll ---------- */
let logItems = [];
VIEWS.protokoll = async (main) => {
  logItems = await api("GET", "/api/events?limit=1000");
  if (state.route !== "protokoll") return;
  const chips = [["all", "Alle"], ["accept", "Angenommen"], ["reject", "Abgelehnt"], ["acct", "Sitzungen"], ["audit", "Änderungen"], ["system", "System"]];
  main.innerHTML = header("Protokoll", `<span class="badge ok"><span class="dot pulse"></span> Live</span> &nbsp;Neue Ereignisse erscheinen automatisch`,
    `<div class="search">${icon("search")}<input type="search" id="lsearch" placeholder="Benutzer, Gerät, MAC …" value="${esc(state.logSearch)}"></div>`) +
    `<div class="chips" style="margin-bottom:14px">${chips.map(([k, t]) => `<button class="chip ${state.logFilter === k ? "on" : ""}" data-action="logFilter" data-k="${k}">${t}</button>`).join("")}</div>
     <div class="card" id="loglist"></div>`;
  $("#lsearch").oninput = (e) => { state.logSearch = e.target.value; drawLog(); };
  drawLog();
  if (!state.es) {
    state.es = new EventSource("/api/events/stream");
    state.es.onmessage = (m) => { logItems.unshift(JSON.parse(m.data)); if (logItems.length > 2000) logItems.pop(); drawLog(); };
  }
};
ACTIONS.logFilter = (el) => { state.logFilter = el.dataset.k; document.querySelectorAll(".chip").forEach((c) => c.classList.toggle("on", c === el)); drawLog(); };

function drawLog() {
  const el = $("#loglist");
  if (!el) return;
  const f = state.logFilter, q = state.logSearch.toLowerCase();
  const list = logItems.filter((e) => {
    if (f === "system" && !(e.kind === "info" || e.kind === "warning")) return false;
    if (f !== "all" && f !== "system" && e.kind !== f) return false;
    if (q && ![e.user, e.admin, e.client, e.clientIp, e.station, e.message, e.method].join(" ").toLowerCase().includes(q)) return false;
    return true;
  }).slice(0, 400);
  el.innerHTML = list.length ? list.map((e) => eventRow(e, true)).join("")
    : `<div class="empty">${icon("list")}<h3>Keine Einträge</h3><p>Sobald sich jemand anmeldet, erscheint es hier.</p></div>`;
}

/* ---------- Zertifikat ---------- */
VIEWS.zertifikat = async (main) => {
  const [c, le, dash, settings, ucerts] = await Promise.all([api("GET", "/api/cert"), api("GET", "/api/acme"), api("GET", "/api/dashboard"),
    api("GET", "/api/settings").catch(() => null), api("GET", "/api/clientcerts").catch((err) => err)]);
  if (state.route !== "zertifikat") return;
  const eapTls = !!(settings && settings.eap && settings.eap.tls);
  const days = Math.round((new Date(c.notAfter) - Date.now()) / 86400000);
  const local = /^(localhost|127\.|\[::1\])/.test(location.hostname);
  const base = local && dash.ips[0] ? `http://${dash.ips[0]}${location.port ? ":" + location.port : ""}` : location.origin;
  const onboardURL = base + "/einrichten";
  const issuer = c.public ? "öffentliche Zertifizierungsstelle" : c.selfSigned ? "eigene Zertifizierungsstelle" : "eigenes Zertifikat";
  let leState = `<span class="badge">aus</span>`;
  if (le.enabled) leState = le.running ? `<span class="badge info"><span class="dot pulse"></span>wird beantragt …</span>`
    : le.lastError ? `<span class="badge bad">Fehler</span>` : `<span class="badge ok"><span class="dot"></span>aktiv</span>`;
  main.innerHTML = header("Zertifikat", "Damit weisen sich Server und WLAN gegenüber den Endgeräten aus") + `
    <div class="card"><div class="row" style="gap:16px;align-items:flex-start">
      <div class="stat"><span class="ico" style="width:48px;height:48px">${icon("shield")}</span></div>
      <div style="flex:1;min-width:240px">
        <div style="font:600 20px/28px 'Segoe UI Variable Display','Segoe UI',sans-serif">${esc(c.subject)}</div>
        <div class="muted">Ausgestellt von ${esc(c.issuer)} (${issuer})</div>
        <div style="margin-top:8px" class="row">${days < 30 ? `<span class="badge bad">läuft in ${days} Tagen ab</span>` : `<span class="badge ok"><span class="dot"></span>Gültig bis ${fmtDate(c.notAfter).split(",")[0]}</span>`}
          ${c.public ? `<span class="badge info">Android ohne Zertifikat-Installation</span>` : ""}</div>
      </div></div>
      <dl class="kv" style="margin-top:16px"><dt>SHA-256-Fingerabdruck</dt><dd class="mono" style="font-size:12px">${esc(c.fingerprint)}</dd></dl>
    </div>

    <div class="section-title">Geräte einrichten</div>
    <div class="card"><div class="qr">
      <img src="/api/qr.png?scale=6&text=${encodeURIComponent(onboardURL)}" alt="QR-Code zur Einrichtungsseite">
      <div style="flex:1;min-width:240px">
        <div style="font-weight:600">Mit dem Handy scannen</div>
        <p class="muted" style="margin:4px 0 12px">Öffnet eine Anleitung für Android, iPhone, Windows und Mac – mit den passenden Werten${c.hasCa ? " und dem Zertifikat zum Herunterladen" : ""}. Das Handy muss dafür im selben Netz sein.</p>
        <div class="row"><a class="btn" href="/einrichten" target="_blank">${icon("phone")} Anleitung öffnen</a>
          <button class="btn ghost" data-action="copy" data-value="${esc(onboardURL)}">${icon("copy")} Link kopieren</button></div>
        <div class="muted mono" style="font-size:12px;margin-top:8px">${esc(onboardURL)}</div>
      </div></div></div>

    <div class="section-title">Let's Encrypt</div>
    ${setting("globe", `Öffentliches Zertifikat ${leState}`,
      le.enabled ? `Domain <b>${esc(le.domain)}</b> · ${esc({ dns: "DNS-API", acmedns: "CNAME-Delegation", http: "HTTP (Port 80)" }[le.method] || "")}${le.lastSuccess && !le.lastSuccess.startsWith("0001") ? ` · zuletzt erneuert ${fmtRel(le.lastSuccess)}` : ""}${le.staging ? " · Testsystem" : ""}`
        : "Mit eigener Domain: Android-Geräte brauchen dann keine Zertifikat-Installation mehr („Systemzertifikate verwenden“).",
      adm(le.enabled ? `<button class="btn" data-action="acmeRun" ${le.running ? "disabled" : ""}>Jetzt erneuern</button><button class="btn" data-action="acmeSetup">Ändern</button>`
        : `<button class="btn primary" data-action="acmeSetup">Einrichten</button>`))}
    ${le.lastError ? `<div class="error-box" style="margin-top:4px"><div>${esc(le.lastError)}</div></div>` : ""}
    ${le.cnameName ? `<div class="callout warn" style="margin-top:4px"><h3>${icon("alert")} Noch ein Schritt: CNAME-Eintrag anlegen</h3>
      <p>Lege bei deinem Domain-Anbieter <b>einmalig</b> diesen DNS-Eintrag an. Danach erneuert EasyRadius das Zertifikat dauerhaft automatisch.</p>
      <dl class="kv" style="grid-template-columns:90px 1fr;margin-bottom:12px">
        <dt>Typ</dt><dd class="mono">CNAME</dd>
        <dt>Name</dt><dd><b class="mono copy" data-action="copy" data-value="${esc(le.cnameName)}">${esc(le.cnameName)}</b></dd>
        <dt>Ziel</dt><dd><b class="mono copy" data-action="copy" data-value="${esc(le.cnameTarget)}">${esc(le.cnameTarget)}</b></dd>
      </dl>
      <p class="muted" style="font-size:12px">Manche Anbieter wollen beim Namen nur den vorderen Teil (ohne deine Domain). Es kann einige Minuten dauern, bis der Eintrag sichtbar ist.</p>
      ${adm(`<button class="btn primary" data-action="acmeRun" ${le.running ? "disabled" : ""}>Eintrag ist angelegt – jetzt prüfen</button>`)}</div>` : ""}

    <div class="section-title">Benutzerzertifikate (EAP-TLS)</div>
    ${userCertsHTML(ucerts, eapTls)}

    <div class="section-title">Weitere Aktionen</div>
    ${c.hasCa ? setting("download", "CA-Zertifikat herunterladen", "Zum Installieren auf Endgeräten, die das Zertifikat prüfen.",
      `<a class="btn" href="/api/cert/ca.crt">Herunterladen</a>`) : ""}
    ${adm(`${!c.selfSigned ? setting("shield", "Wieder das Zertifikat der eigenen CA verwenden", "Geräte, die die EasyRadius-CA installiert haben, vertrauen ihm sofort. Let's Encrypt wird dabei ausgeschaltet.",
      `<button class="btn" data-action="certOwn">Verwenden</button>`) : ""}
    ${setting("refresh", "Komplett neu erzeugen", "Neue Zertifizierungsstelle und neues Serverzertifikat.", `<button class="btn" data-action="certGenerate">Erzeugen</button>`)}
    ${setting("upload", "Eigenes Zertifikat verwenden", "Zertifikat und Schlüssel im PEM-Format hochladen, z. B. von deiner Firmen-CA.", `<button class="btn" data-action="certUpload">Hochladen</button>`)}`)}`;
  if (le.running) setTimeout(() => { if (state.route === "zertifikat" && !modal.open) render(); }, 4000);
};

function userCertsHTML(list, eapTls) {
  if (list instanceof Error) {
    return `<div class="callout">${notAvailable(list) ? NOT_AVAILABLE + "." : `Liste nicht abrufbar: ${esc(list.message)}`}</div>`;
  }
  const hint = eapTls ? `Neue Zertifikate gibt es unter <a href="#/benutzer">Benutzer</a> → ${icon("shield")} „Zertifikat (EAP-TLS)“.`
    : `EAP-TLS ist ausgeschaltet – einschalten unter <a href="#/einstellungen">Einstellungen</a> → WLAN-Verfahren.`;
  if (!list || !list.length) return `<div class="callout">Noch keine Benutzerzertifikate ausgestellt. ${hint}</div>`;
  return `<div class="card flush"><div class="table-wrap"><table><thead><tr><th>Benutzer</th><th>Status</th><th>Ausgestellt</th><th>Gültig bis</th><th>Seriennummer</th><th></th></tr></thead><tbody>
    ${list.map((x) => `<tr>
      <td>${esc(x.user)}</td>
      <td>${x.revoked ? `<span class="badge bad">Gesperrt</span>` : isPast(x.notAfter) ? `<span class="badge">Abgelaufen</span>` : `<span class="badge ok"><span class="dot"></span>Gültig</span>`}</td>
      <td class="muted">${fmtDay(x.created)}</td>
      <td class="muted">${fmtDay(x.notAfter)}</td>
      <td class="mono muted serial">${esc(x.serial)}</td>
      <td class="actions">${x.revoked ? "" : adm(`<button class="btn sm" data-action="revokeCert" data-serial="${esc(x.serial)}" data-user="${esc(x.user)}">${icon("lock")} Sperren</button>`)}</td></tr>`).join("")}
  </tbody></table></div></div><p class="muted" style="font-size:12px;margin-top:8px">${hint}</p>`;
}
ACTIONS.revokeCert = (el) => {
  const { serial, user } = el.dataset;
  confirmDialog("Zertifikat sperren", `Das Zertifikat von <b>${esc(user)}</b> wird gesperrt. Damit ist keine Anmeldung per EAP-TLS mehr möglich – das lässt sich nicht rückgängig machen.`, "Sperren", async () => {
    try { await api("POST", "/api/clientcerts/" + encodeURIComponent(serial) + "/revoke"); }
    catch (err) { throw notAvailable(err) ? new Error(NOT_AVAILABLE) : err; }
    toast("Zertifikat gesperrt");
    render();
  });
};

ACTIONS.acmeRun = async () => { await api("POST", "/api/acme/run"); toast("Zertifikat wird beantragt …"); render(); };
ACTIONS.certOwn = async () => { await api("POST", "/api/cert/own"); toast("Zertifikat der eigenen CA ist aktiv"); render(); };
ACTIONS.acmeSetup = async () => {
  const [le, provs] = await Promise.all([api("GET", "/api/acme"), api("GET", "/api/acme/providers")]);
  const popular = ["ionos", "hetzner", "netcup", "inwx", "allinkl", "cloudflare", "godaddy", "ovh", "hostingde", "strato"];
  const opt = (p) => `<option value="${esc(p.code)}" ${p.code === le.provider ? "selected" : ""}>${esc(p.name)}</option>`;
  openModal("Let's Encrypt einrichten", `
    <p class="muted">Du brauchst eine eigene Domain – egal bei welchem Anbieter. Der RADIUS-Server muss dafür (außer beim HTTP-Weg) nicht aus dem Internet erreichbar sein.</p>
    <label class="f"><span>Domain für den RADIUS-Server</span><input type="text" name="domain" value="${esc(le.domain)}" placeholder="radius.example.de">
      <small>Diesen Namen tragen Android-Geräte später als „Domain“ ein.</small></label>
    <div class="f" style="margin-bottom:8px"><span style="display:block;margin-bottom:8px">Wie soll die Domain bestätigt werden?</span>
      <label class="switch"><input type="radio" name="method" value="dns" ${le.method === "dns" || !le.method ? "checked" : ""}>
        <span><b>Automatisch über meinen DNS-Anbieter</b> – ${provs.length} Anbieter mit Schnittstelle, z. B. IONOS, Hetzner, netcup, INWX, all-inkl, GoDaddy, OVH.</span></label>
      <label class="switch"><input type="radio" name="method" value="acmedns" ${le.method === "acmedns" ? "checked" : ""}>
        <span><b>Jeder Anbieter (einmal CNAME eintragen)</b> – z. B. Strato, 1&amp;1, United Domains: Du legst einmalig einen DNS-Eintrag an, danach läuft alles automatisch.</span></label>
      <label class="switch"><input type="radio" name="method" value="http" ${le.method === "http" ? "checked" : ""}>
        <span><b>Über Port 80</b> – die Domain zeigt auf deine öffentliche IP und Port 80 ist an diesen Rechner weitergeleitet.</span></label>
    </div>
    <div id="m-dns" class="card" style="margin-bottom:16px">
      <label class="f"><span>DNS-Anbieter</span><select name="provider" id="provsel"><option value="">Bitte wählen …</option>
        <optgroup label="Häufig">${provs.filter((p) => popular.includes(p.code)).map(opt).join("")}</optgroup>
        <optgroup label="Alle">${provs.map(opt).join("")}</optgroup></select>
        <small>Dein Anbieter fehlt? Dann den CNAME-Weg wählen – der funktioniert überall.</small></label>
      <div id="provfields"></div>
    </div>
    <div id="m-acmedns" class="card" style="margin-bottom:16px">
      <p style="margin-bottom:8px">Nach dem Speichern zeigt EasyRadius dir den <b>CNAME-Eintrag</b>, den du einmalig bei deinem Anbieter anlegst.
        Die Bestätigung läuft dann über den Dienst <a href="https://github.com/joohoi/acme-dns" target="_blank" rel="noopener">acme-dns</a> – dorthin werden nur die Prüfwerte von Let's Encrypt übertragen, keine Zugangsdaten.</p>
      <label class="f" style="margin:0"><span>acme-dns-Server <span class="muted">(optional, eigener Server)</span></span><input type="text" name="acmeDnsUrl" value="${esc(le.acmeDnsUrl)}" placeholder="${esc(le.defaultAcmeDnsUrl)}"></label>
    </div>
    <div id="m-http" class="card" style="margin-bottom:16px"><p style="margin:0">Let's Encrypt ruft zur Prüfung <span class="mono">http://${esc(le.domain || "deine-domain")}/.well-known/…</span> auf.
      Dafür muss im Router Port 80 an diesen Rechner weitergeleitet sein und die Domain auf deine öffentliche IP zeigen. EasyRadius öffnet Port 80 nur kurz während der Prüfung.</p></div>
    <label class="f"><span>E-Mail <span class="muted">(optional)</span></span><input type="text" name="email" value="${esc(le.email)}" placeholder="für Hinweise von Let's Encrypt"></label>
    <label class="switch"><input type="checkbox" name="staging" ${le.staging ? "checked" : ""}> <span>Testsystem verwenden <span class="muted">(Zertifikate sind nicht vertrauenswürdig – nur zum Ausprobieren)</span></span></label>
    <label class="switch"><input type="checkbox" name="acceptTos"> <span>Ich akzeptiere die <a href="https://letsencrypt.org/repository/" target="_blank" rel="noopener">Nutzungsbedingungen von Let's Encrypt</a>.</span></label>
    ${le.enabled ? `<label class="switch"><input type="checkbox" name="disable"> <span>Let's Encrypt ausschalten</span></label>` : ""}`,
  {
    submit: "Speichern und beantragen",
    wide: true,
    onSubmit: async (fd) => {
      const off = fd.has("disable");
      const creds = {};
      document.querySelectorAll("#provfields input[data-env]").forEach((i) => { if (i.value.trim()) creds[i.dataset.env] = i.value.trim(); });
      await api("PUT", "/api/acme", {
        enabled: !off, domain: fd.get("domain"), email: fd.get("email"), method: fd.get("method"),
        provider: fd.get("provider") || "", credentials: creds, acmeDnsUrl: fd.get("acmeDnsUrl") || "",
        staging: fd.has("staging"), acceptTos: fd.has("acceptTos"),
      });
      toast(off ? "Let's Encrypt ausgeschaltet" : "Zertifikat wird beantragt – das dauert meist 1–5 Minuten");
      render();
    },
  });
  const form = $("form", modal);
  const showMethod = () => ["dns", "acmedns", "http"].forEach((m) => { $("#m-" + m).style.display = form.method.value === m ? "" : "none"; });
  const showFields = () => {
    const p = provs.find((x) => x.code === $("#provsel").value);
    $("#provfields").innerHTML = !p ? "" : `
      <p class="muted" style="font-size:12px;margin-bottom:8px">Zugangsdaten für die DNS-Schnittstelle von <a href="${esc(p.url)}" target="_blank" rel="noopener">${esc(p.name)}</a>.
        ${p.fields.length > 1 ? "Je nach Anmeldeart reichen einige der Felder." : ""}</p>
      ${p.fields.map((f) => `<label class="f"><span class="mono" style="font-size:12px">${esc(f.env)}</span>
        <input type="password" data-env="${esc(f.env)}" autocomplete="off" placeholder="${p.code === le.provider && le.savedCredentials.includes(f.env) ? "gespeichert – leer lassen zum Behalten" : ""}">
        <small>${esc(f.help)}</small></label>`).join("")}`;
  };
  form.querySelectorAll("input[name=method]").forEach((r) => r.onchange = showMethod);
  $("#provsel").onchange = showFields;
  showMethod();
  showFields();
};
ACTIONS.certGenerate = () => openModal("Neues Zertifikat erzeugen", `
  <div class="callout warn">Bereits verbundene Geräte müssen dem neuen Zertifikat beim nächsten Verbinden erneut vertrauen.</div>
  <label class="f"><span>Servername im Zertifikat</span><input type="text" name="name" value="${esc(state.server || "EasyRadius")}"><small>Wird auf den Endgeräten angezeigt, z. B. „radius.zuhause.lan“.</small></label>`,
{ submit: "Erzeugen", onSubmit: async (fd) => { await api("POST", "/api/cert/generate", Object.fromEntries(fd)); toast("Neues Zertifikat aktiv"); render(); } });
ACTIONS.certUpload = () => openModal("Eigenes Zertifikat hochladen", `
  <p class="muted">Zum Beispiel von deiner eigenen CA oder Let’s Encrypt. Beide Dateien im PEM-Format.</p>
  <label class="f"><span>Zertifikat (inkl. Zwischenzertifikaten)</span><input type="file" name="cert" accept=".pem,.crt,.cer"></label>
  <label class="f"><span>Privater Schlüssel</span><input type="file" name="key" accept=".pem,.key"></label>`,
{ submit: "Hochladen", onSubmit: async (fd) => { await api("POST", "/api/cert/upload", fd); toast("Zertifikat übernommen"); render(); } });

/* Einstellungs-Karte wie in den Windows-Einstellungen */
function setting(ic, title, desc, ctrl) {
  return `<div class="setting">${icon(ic)}<div class="s-body"><div>${title}</div>${desc ? `<div class="s-desc">${desc}</div>` : ""}</div><div class="s-ctrl">${ctrl}</div></div>`;
}

/* ---------- Einstellungen ---------- */
VIEWS.einstellungen = async (main) => {
  const s = await api("GET", "/api/settings");
  if (state.route !== "einstellungen") return;
  const lock = s.lockout || {}, eap = s.eap || {};
  const toggle = (name, on, extra = "") => `<label class="switch" style="margin:0"><input type="checkbox" name="${name}" ${on ? "checked" : ""} ${extra}></label>`;
  const httpsUrl = `https://${location.hostname}:${state.httpsPort}${location.pathname}`;
  // „Nur HTTPS“ lässt sich nur über HTTPS oder direkt am Server einschalten.
  const httpsLocked = !s.httpsOnly && (!s.httpsAddr || (!state.secure && !state.loopback));
  main.innerHTML = header("Einstellungen", "") + `
    <form id="sform" novalidate><div class="msg"></div><fieldset class="plain" ${isAdmin() ? "" : "disabled"}>
      <div class="section-title" style="margin-top:0">RADIUS-Server</div>
      ${setting("router", "Servername", "Wird den Geräten bei der Anmeldung genannt.", `<input type="text" name="serverName" value="${esc(s.serverName)}">`)}
      ${setting("key", "Authentifizierungs-Port", "UDP-Port für Anmeldungen. Standard: 1812", `<input type="number" name="authPort" min="1" max="65535" value="${esc(s.authPort)}">`)}
      ${setting("activity", "Accounting-Port", "UDP-Port für Sitzungsdaten. Standard: 1813", `<input type="number" name="acctPort" min="1" max="65535" value="${esc(s.acctPort)}">`)}

      <div class="section-title">Schutz vor Passwort-Raten</div>
      ${setting("lock", "Nach Fehlversuchen sperren",
        "Wer zu oft ein falsches Passwort eingibt, wird für eine Weile gesperrt – der Benutzer und zusätzlich das Endgerät (MAC-Adresse). Aufheben lässt sich eine Sperre unter <a href=\"#/sitzungen\">Sitzungen</a>.",
        toggle("lockEnabled", lock.enabled))}
      ${setting("alert", "Fehlversuche bis zur Sperre", "3 bis 100. Gezählt wird innerhalb der Sperrdauer.", `<input type="number" name="lockAttempts" min="3" max="100" value="${esc(lock.attempts || 10)}">`)}
      ${setting("refresh", "Sperrdauer in Minuten", "1 bis 1440 (ein Tag).", `<input type="number" name="lockMinutes" min="1" max="1440" value="${esc(lock.minutes || 15)}">`)}

      <div class="section-title">WLAN-Verfahren</div>
      ${setting("wifi", "PEAP (MSCHAPv2)", "Immer an – funktioniert mit Windows, Android, iPhone und Mac.", `<span class="badge ok">an</span>`)}
      ${setting("key", "EAP-TTLS zusätzlich anbieten",
        "Für Linux, Android und Apple-Profile, die TTLS bevorzugen (innen PAP oder MSCHAPv2). PEAP bleibt daneben aktiv.", toggle("eapTtls", eap.ttls))}
      ${setting("shield", "EAP-TLS (Benutzerzertifikate)",
        "Anmeldung mit einem Zertifikat statt Passwort. Zertifikate stellst du unter Benutzer aus; dafür muss EasyRadius die eigene Zertifizierungsstelle verwenden.", toggle("eapTls", eap.tls))}

      <div class="section-title">Weboberfläche</div>
      ${setting("globe", "Aus dem Netz nur HTTPS erlauben",
        `Unverschlüsselte Aufrufe von anderen Rechnern werden auf <span class="mono">${esc(httpsUrl)}</span> umgeleitet. Die Einrichtungsseite <span class="mono">/einrichten</span> bleibt auch per HTTP erreichbar, damit Handys sie ohne Zertifikatswarnung öffnen können.
         ${httpsLocked ? `<br><b>Einschalten geht nur über eine HTTPS-Verbindung oder direkt am Server.</b>` : ""}`,
        toggle("httpsOnly", s.httpsOnly, httpsLocked ? "disabled" : ""))}
      ${setting("list", "Protokoll-Einträge behalten", "So viele Ereignisse hebt das Protokoll auf (100 bis 100000). Ältere werden gelöscht.",
        `<input type="number" name="logRetention" min="100" max="100000" step="100" value="${esc(s.logRetention || 5000)}">`)}
      ${adm(`<div class="row" style="justify-content:flex-end;margin-top:8px"><button class="btn primary" type="submit">Speichern</button></div>`)}
    </fieldset></form>

    ${adm(`<div class="section-title">Sicherung</div>
    ${setting("download", "Sicherung herunterladen",
      "Eine verschlüsselte Datei mit allen Einstellungen, Benutzern, Geräten, Secrets und Zertifikaten. Ohne das Passwort ist sie wertlos – gut merken.",
      `<button class="btn" data-action="backup">Herunterladen</button>`)}
    ${setting("upload", "Sicherung wiederherstellen",
      "Ersetzt alles durch den Stand der Sicherung. Danach müssen sich alle neu anmelden.",
      `<button class="btn" data-action="restore">Wiederherstellen</button>`)}`)}

    <div class="section-title">Konto</div>
    ${setting("users", esc(state.user), `Angemeldet${isAdmin() ? " als Administrator" : " – nur lesen"}`, `<button class="btn" data-action="changePw">Passwort ändern</button>`)}
    ${setting("devices", "Überall abmelden", "Beendet alle Anmeldungen dieses Kontos – auf allen Rechnern und in allen Browsern.", `<button class="btn" data-action="logoutAll">Überall abmelden</button>`)}
    ${setting("logout", "Abmelden", "Von der Verwaltungsoberfläche abmelden.", `<button class="btn" data-action="logout">Abmelden</button>`)}
    <div class="section-title">Info</div>
    ${setting("info", "EasyRadius", `Version ${esc(state.version)} · Weboberfläche ${esc(location.origin)}${s.httpsAddr && !state.secure ? ` · HTTPS auf Port ${esc(state.httpsPort)}` : ""}`, "")}
    ${setting("wifi", "Unterstützte Verfahren", "WPA2/WPA3-Enterprise (PEAP, EAP-TTLS, EAP-TLS), PAP, MS-CHAPv2, MAC-Authentifizierung, VLAN-Zuweisung, Accounting, Sitzung trennen (RFC 5176)", "")}`;
  const sf = $("#sform");
  sf.onsubmit = async (e) => {
    e.preventDefault();
    const fd = new FormData(sf);
    const num = (k) => parseInt(fd.get(k), 10) || 0;
    const body = {
      serverName: fd.get("serverName"), authPort: num("authPort"), acctPort: num("acctPort"),
      httpsOnly: sf.httpsOnly.checked, logRetention: num("logRetention"),
      lockout: { enabled: fd.has("lockEnabled"), attempts: num("lockAttempts"), minutes: num("lockMinutes") },
      eap: { ttls: fd.has("eapTtls"), tls: fd.has("eapTls") },
    };
    try {
      await api("PUT", "/api/settings", body);
      state.server = body.serverName; $(".msg", sf).innerHTML = ""; toast("Einstellungen gespeichert");
    } catch (err) {
      $(".msg", sf).innerHTML = `<div class="error-box">${esc(err.message)}</div>`;
      $(".msg", sf).scrollIntoView({ block: "nearest" });
    }
  };
};

ACTIONS.backup = () => openModal("Sicherung herunterladen", `
  <p class="muted">Die Datei enthält alle Secrets und Schlüssel und wird mit diesem Passwort verschlüsselt. Ohne das Passwort lässt sie sich nicht wiederherstellen.</p>
  <label class="f"><span>Passwort für die Sicherung</span><input type="password" name="password" autocomplete="new-password"><small>Mindestens 8 Zeichen.</small></label>
  <label class="f"><span>Passwort wiederholen</span><input type="password" name="password2" autocomplete="new-password"></label>`,
{
  submit: "Herunterladen",
  onSubmit: async (fd) => {
    const pw = String(fd.get("password") || "");
    if (pw.length < 8) throw new Error("Das Passwort braucht mindestens 8 Zeichen.");
    if (pw !== fd.get("password2")) throw new Error("Die Passwörter stimmen nicht überein.");
    const d = new Date();
    const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    await download("/api/backup", { password: pw }, `EasyRadius-Sicherung-${day}.erbackup`);
    toast("Sicherung heruntergeladen");
  },
});

ACTIONS.restore = () => openModal("Sicherung wiederherstellen", `
  <div class="callout warn"><h3>${icon("alert")} Alles wird ersetzt</h3>
    Einstellungen, Benutzer, Gruppen, Geräte, MAC-Adressen, Konten und Zertifikate werden durch den Stand der Sicherung überschrieben. Danach müssen sich alle neu anmelden.</div>
  <label class="f"><span>Sicherungsdatei</span><input type="file" name="file" accept=".erbackup"></label>
  <label class="f"><span>Passwort der Sicherung</span><input type="password" name="password" autocomplete="off"></label>`,
{
  submit: "Wiederherstellen",
  danger: true,
  onSubmit: async (fd) => {
    const f = fd.get("file");
    if (!f || !f.size) throw new Error("Bitte die Sicherungsdatei auswählen.");
    if (!fd.get("password")) throw new Error("Bitte das Passwort der Sicherung eingeben.");
    await api("POST", "/api/restore", fd);
    modal.close();
    state.user = ""; location.hash = "";
    try { applyState(await api("GET", "/api/state")); } catch { /* Anmeldung kommt trotzdem */ }
    render();
    toast("Sicherung wiederhergestellt – bitte neu anmelden");
  },
});
ACTIONS.changePw = () => openModal("Passwort ändern", `
  <label class="f"><span>Aktuelles Passwort</span><input type="password" name="current" autocomplete="current-password"></label>
  <label class="f"><span>Neues Passwort</span><input type="password" name="new" autocomplete="new-password"><small>Mindestens 8 Zeichen.</small></label>
  <label class="f"><span>Neues Passwort wiederholen</span><input type="password" name="new2" autocomplete="new-password"></label>`,
{
  submit: "Ändern",
  onSubmit: async (fd) => {
    const d = Object.fromEntries(fd);
    if (d.new !== d.new2) throw new Error("Die neuen Passwörter stimmen nicht überein.");
    await api("POST", "/api/admin/password", { current: d.current, new: d.new });
    toast("Passwort geändert");
  },
});

ACTIONS.reloadView = () => render();

/* ---------- Sitzungen, Accounting, Sperren ---------- */
VIEWS.sitzungen = async (main) => {
  const month = state.acctMonth ? "?month=" + encodeURIComponent(state.acctMonth) : "";
  const [online, acct, locks] = await Promise.all([api("GET", "/api/online"), api("GET", "/api/accounting" + month), api("GET", "/api/lockouts")]);
  if (state.route !== "sitzungen") return;
  state.online = online || [];
  state.locks = locks || [];
  main.innerHTML = header("Sitzungen", "Wer gerade verbunden ist, wer wann online war und wer gesperrt ist",
    `<button class="btn" data-action="reloadView">${icon("refresh")} Aktualisieren</button>`) + `
    <div class="section-title" style="margin-top:0">Jetzt verbunden</div>
    <div id="onlinebox"></div>
    <div class="section-title">Gesperrt nach Fehlversuchen</div>
    <div id="lockbox"></div>
    <div class="section-title">Abgeschlossene Sitzungen</div>
    <div id="acctbox"></div>`;
  drawOnline();
  drawLocks();
  drawAcct(acct);
};

function drawOnline() {
  const list = state.online;
  $("#onlinebox").innerHTML = list.length ? `<div class="card flush"><div class="table-wrap"><table>
    <thead><tr><th>Benutzer</th><th>Endgerät</th><th>Über</th><th>Seit</th><th>Empfangen / Gesendet</th><th></th></tr></thead><tbody>
    ${list.map((o) => `<tr>
      <td><b>${esc(o.user || "?")}</b></td>
      <td><div class="who"><span class="mono">${esc(o.station || "–")}</span>${o.ip ? `<small>${esc(o.ip)}</small>` : ""}</div></td>
      <td><div class="who">${esc(o.client || "–")}${o.nasAddr ? `<small class="mono">${esc(o.nasAddr)}</small>` : ""}</div></td>
      <td title="${esc(fmtDate(o.started))}">${fmtRel(o.started)}</td>
      <td class="muted">${fmtBytes(o.inBytes || 0)} / ${fmtBytes(o.outBytes || 0)}</td>
      <td class="actions">${adm(`<button class="btn sm" data-action="disconnect" data-key="${esc(o.key)}">${icon("power")} Trennen</button>`)}</td>
    </tr>`).join("")}</tbody></table></div></div>
    <p class="muted" style="font-size:12px;margin-top:8px">„Trennen“ schickt dem Access Point oder Switch eine Disconnect-Anfrage (RFC 5176) an den CoA-Port des Geräts (Standard 3799). Das Gerät muss das unterstützen und eingeschaltet haben.</p>`
    : `<div class="callout">Keine aktiven Sitzungen bekannt. Damit Sitzungen erscheinen, am Access Point RADIUS-Accounting einschalten.</div>`;
}

function drawLocks() {
  const list = state.locks;
  const until = (s) => { const d = new Date(s); return d.toDateString() === new Date().toDateString() ? fmtTime(s) + " Uhr" : fmtDate(s); };
  $("#lockbox").innerHTML = list.length ? `<div class="card flush"><div class="table-wrap"><table>
    <thead><tr><th>Gesperrt</th><th>Art</th><th>Bis</th><th class="actions">${adm(`<button class="btn sm" data-action="unlockAll">${icon("unlock")} Alle entsperren</button>`)}</th></tr></thead><tbody>
    ${list.map((l) => `<tr>
      <td class="${l.kind === "station" ? "mono" : ""}">${esc(l.name)}</td>
      <td>${l.kind === "station" ? "Endgerät" : "Benutzer"}</td>
      <td>${esc(until(l.until))}</td>
      <td class="actions">${adm(`<button class="btn sm" data-action="unlock" data-key="${esc(l.key)}">${icon("unlock")} Entsperren</button>`)}</td>
    </tr>`).join("")}</tbody></table></div></div>`
    : `<div class="callout">Derzeit ist niemand gesperrt.</div>`;
}

function drawAcct(a) {
  a = a || {};
  const box = $("#acctbox");
  if (!box) return;
  const months = (a.months || []).slice();
  if (a.month && !months.includes(a.month)) months.unshift(a.month);
  if (!months.length) {
    box.innerHTML = `<div class="callout">Noch keine abgeschlossenen Sitzungen aufgezeichnet. Dafür am Access Point RADIUS-Accounting einschalten.</div>`;
    return;
  }
  const recs = (a.records || []).slice().sort((x, y) => new Date(y.stop || y.start) - new Date(x.stop || x.start));
  const shown = recs.slice(0, 500);
  const secs = recs.reduce((n, r) => n + (r.seconds || 0), 0);
  const bytes = recs.reduce((n, r) => n + (r.inBytes || 0) + (r.outBytes || 0), 0);
  box.innerHTML = `
    <div class="row" style="margin-bottom:8px">
      <select id="acctmonth" class="auto">${months.map((m) => `<option value="${esc(m)}" ${m === a.month ? "selected" : ""}>${esc(fmtMonth(m))}</option>`).join("")}</select>
      <a class="btn" href="/api/accounting.csv?month=${encodeURIComponent(a.month || months[0])}" download>${icon("download")} CSV-Export</a>
      <span class="muted">${recs.length} Sitzung${recs.length === 1 ? "" : "en"} · ${fmtDur(secs)} · ${fmtBytes(bytes)}</span>
    </div>
    ${recs.length ? `<div class="card flush"><div class="table-wrap"><table>
      <thead><tr><th>Benutzer</th><th>Endgerät</th><th>Über</th><th>Beginn</th><th>Dauer</th><th>Empfangen / Gesendet</th><th>Grund</th></tr></thead><tbody>
      ${shown.map((r) => `<tr>
        <td>${esc(r.user || "?")}</td>
        <td><div class="who"><span class="mono">${esc(r.station || "–")}</span>${r.ip ? `<small>${esc(r.ip)}</small>` : ""}</div></td>
        <td>${esc(r.client || "–")}</td>
        <td class="muted">${fmtDate(r.start)}</td>
        <td>${fmtDur(r.seconds)}</td>
        <td class="muted">${fmtBytes(r.inBytes || 0)} / ${fmtBytes(r.outBytes || 0)}</td>
        <td class="muted">${esc(r.cause || "")}</td>
      </tr>`).join("")}</tbody></table></div></div>
      ${recs.length > shown.length ? `<p class="muted" style="font-size:12px;margin-top:8px">Angezeigt werden die neuesten ${shown.length} – alle stehen im CSV-Export.</p>` : ""}`
    : `<div class="callout">In diesem Monat keine abgeschlossenen Sitzungen.</div>`}`;
  $("#acctmonth").onchange = async (e) => {
    state.acctMonth = e.target.value;
    try { drawAcct(await api("GET", "/api/accounting?month=" + encodeURIComponent(state.acctMonth))); }
    catch (err) { toast(err.message, true); }
  };
}

ACTIONS.disconnect = (el) => {
  const key = el.dataset.key;
  const o = state.online.find((x) => x.key === key) || {};
  confirmDialog("Sitzung trennen", `Soll die Sitzung von <b>${esc(o.user || "?")}</b>${o.station ? ` (<span class="mono">${esc(o.station)}</span>)` : ""} getrennt werden?
    Das Endgerät verbindet sich danach meist sofort neu – zum dauerhaften Aussperren den Benutzer deaktivieren.`, "Trennen", async () => {
    await api("POST", "/api/online/disconnect", { key });
    toast("Sitzung getrennt");
    VIEWS.sitzungen($(".main"));
  });
};
async function unlock(key) {
  await api("POST", "/api/lockouts/unlock", { key });
  toast(key ? "Sperre aufgehoben" : "Alle Sperren aufgehoben");
  if (state.route === "sitzungen") VIEWS.sitzungen($(".main"));
}
ACTIONS.unlock = (el) => unlock(el.dataset.key);
ACTIONS.unlockAll = () => unlock("");

/* ---------- Konten und API-Tokens ---------- */
let adminsCache = [], tokensCache = [];
const roleBadge = (r) => (r === "viewer" ? `<span class="badge">Nur lesen</span>` : `<span class="badge info">Administrator</span>`);
const todayISO = () => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`; };

VIEWS.konten = async (main) => {
  if (!isAdmin()) {
    main.innerHTML = header("Konten", "") + `<div class="callout">Konten und API-Tokens können nur Administratoren verwalten.</div>`;
    return;
  }
  [adminsCache, tokensCache] = await Promise.all([api("GET", "/api/admins"), api("GET", "/api/tokens")]);
  if (state.route !== "konten") return;
  const host = `https://${location.hostname}:${state.httpsPort}`;
  const today = todayISO();
  main.innerHTML = header("Konten", "Wer die Weboberfläche benutzen darf – und Zugänge für Skripte",
    `<button class="btn primary" data-action="newAdmin">${icon("plus")} Konto hinzufügen</button>`) + `
    <div class="card flush"><div class="table-wrap"><table>
      <thead><tr><th>Benutzername</th><th>Rolle</th><th>Letzte Anmeldung</th><th>Angelegt</th><th></th></tr></thead><tbody>
      ${adminsCache.map((a) => `<tr>
        <td>${esc(a.username)} ${a.self ? `<span class="badge ok" title="Mit diesem Konto bist du angemeldet">Du</span>` : ""}</td>
        <td>${roleBadge(a.role)}</td>
        <td class="muted">${fmtRel(a.lastLogin)}</td>
        <td class="muted">${fmtDay(a.createdAt)}</td>
        <td class="actions"><button class="btn ghost sm" data-action="editAdmin" data-id="${esc(a.id)}" title="Bearbeiten">${icon("edit")}</button>
          ${a.self ? "" : `<button class="btn ghost sm" data-action="deleteAdmin" data-id="${esc(a.id)}" title="Löschen">${icon("trash")}</button>`}</td>
      </tr>`).join("")}</tbody></table></div></div>
    <p class="muted" style="font-size:12px;margin-top:8px"><b>Administrator</b> darf alles. <b>Nur lesen</b> sieht Benutzer, Geräte, Sitzungen und Protokoll, kann aber nichts ändern und keine Secrets abrufen.</p>

    <div class="section-title row" style="justify-content:space-between"><span>API-Tokens</span>
      <button class="btn sm" data-action="newToken">${icon("plus")} Token erstellen</button></div>
    ${tokensCache.length ? `<div class="card flush"><div class="table-wrap"><table>
      <thead><tr><th>Name</th><th>Token</th><th>Rolle</th><th>Erstellt</th><th>Zuletzt benutzt</th><th>Läuft ab</th><th></th></tr></thead><tbody>
      ${tokensCache.map((t) => `<tr>
        <td>${esc(t.name)}</td>
        <td class="mono muted">${esc(t.prefix)}…</td>
        <td>${roleBadge(t.role)}</td>
        <td><div class="who">${fmtDay(t.createdAt)}${t.createdBy ? `<small>von ${esc(t.createdBy)}</small>` : ""}</div></td>
        <td class="muted">${fmtRel(t.lastUsed)}</td>
        <td>${!t.expiresAt ? `<span class="muted">nie</span>` : t.expiresAt < today ? `<span class="badge bad">abgelaufen</span>` : esc(fmtDay(t.expiresAt))}</td>
        <td class="actions"><button class="btn ghost sm" data-action="deleteToken" data-id="${esc(t.id)}" title="Löschen">${icon("trash")}</button></td>
      </tr>`).join("")}</tbody></table></div></div>`
    : `<div class="callout">Noch keine API-Tokens. Damit können Skripte die API ohne Passwort benutzen.</div>`}
    <div class="callout" style="margin-top:12px"><h3>${icon("info")} So benutzt du ein Token</h3>
      <p>Das Token im Header <span class="mono">Authorization</span> mitschicken, zum Beispiel:</p>
      <pre class="code">curl -H "Authorization: Bearer er_…" ${esc(host)}/api/users</pre>
      <p class="muted" style="margin:0;font-size:12px">Ein Token mit „Nur lesen“ darf nur abfragen. Bei einem Zertifikat der eigenen CA zusätzlich <span class="mono">--cacert ca.crt</span> angeben.</p></div>`;
};

function adminDialog(a = { role: "admin" }) {
  const isNew = !a.id;
  openModal(isNew ? "Konto hinzufügen" : "Konto bearbeiten", `
    <label class="f"><span>Benutzername</span><input type="text" name="username" value="${esc(a.username)}" autocomplete="off"></label>
    <label class="f"><span>Rolle</span><select name="role">
      <option value="admin" ${a.role !== "viewer" ? "selected" : ""}>Administrator</option>
      <option value="viewer" ${a.role === "viewer" ? "selected" : ""}>Nur lesen</option></select>
      <small>„Nur lesen“ sieht fast alles, kann aber nichts ändern.</small></label>
    <label class="f"><span>${isNew ? "Passwort" : "Neues Passwort"}</span>
      <div class="input-group"><input type="password" name="password" autocomplete="new-password" placeholder="${isNew ? "" : "leer lassen = unverändert"}">
      <button type="button" class="btn" data-action="reveal" title="Anzeigen">${icon("eye")}</button>
      <button type="button" class="btn" data-action="genpw" title="Zufällig erzeugen">${icon("dice")}</button></div>
      <small>8 bis 72 Zeichen.${isNew ? "" : " Neues Passwort oder andere Rolle: Das Konto wird überall abgemeldet."}</small></label>`,
  {
    submit: isNew ? "Anlegen" : "Speichern",
    onSubmit: async (fd) => {
      const username = String(fd.get("username") || "").trim();
      const role = fd.get("role"), password = String(fd.get("password") || "");
      if (!username) throw new Error("Bitte einen Benutzernamen eingeben.");
      if (isNew && !password) throw new Error("Bitte ein Passwort vergeben.");
      const roleChanged = !isNew && role !== a.role;
      // Rolle nur mitschicken, wenn sie sich ändert – sonst beendet der Server die Sitzungen des Kontos.
      await api(isNew ? "POST" : "PUT", isNew ? "/api/admins" : "/api/admins/" + a.id,
        { username, role: isNew || roleChanged ? role : "", password });
      toast(isNew ? "Konto angelegt" : "Gespeichert");
      if (a.self && (password || roleChanged)) { state.user = ""; location.hash = ""; render(); return; }
      if (a.self && username !== state.user) { state.user = username; app.innerHTML = ""; }
      render();
    },
  });
}
ACTIONS.newAdmin = () => adminDialog();
ACTIONS.editAdmin = (el) => adminDialog(adminsCache.find((a) => a.id === el.dataset.id));
ACTIONS.deleteAdmin = (el) => {
  const a = adminsCache.find((x) => x.id === el.dataset.id);
  if (!a) return;
  confirmDialog("Konto löschen", `Soll das Konto <b>${esc(a.username)}</b> gelöscht werden? Es wird sofort abgemeldet.`, "Löschen", async () => {
    await api("DELETE", "/api/admins/" + a.id);
    toast("Konto gelöscht");
    render();
  });
};

ACTIONS.newToken = () => openModal("API-Token erstellen", `
  <label class="f"><span>Name</span><input type="text" name="name" placeholder="z. B. Benutzer-Abgleich" autocomplete="off"><small>Wofür das Token gedacht ist.</small></label>
  <label class="f"><span>Rolle</span><select name="role"><option value="viewer">Nur lesen</option><option value="admin">Administrator</option></select></label>
  <label class="f"><span>Gültig bis <span class="muted">(optional)</span></span><input type="date" name="expiresAt"><small>Leer = unbegrenzt.</small></label>`,
{
  submit: "Erstellen",
  onSubmit: async (fd) => {
    const name = String(fd.get("name") || "").trim();
    if (!name) throw new Error("Bitte einen Namen eingeben.");
    const t = await api("POST", "/api/tokens", { name, role: fd.get("role"), expiresAt: fd.get("expiresAt") || "" });
    VIEWS.konten($(".main"));
    showToken(t);
    return true;
  },
});

function showToken(t) {
  openModal("Token erstellt", `
    <div class="ok-box">Das Token wird nur jetzt angezeigt. Kopiere es und bewahre es sicher auf – später lässt es sich nicht mehr anzeigen.</div>
    <div class="tokenbox mono">${esc(t.token)}</div>
    <div class="row" style="margin-top:12px"><button type="button" class="btn primary" data-action="copy" data-value="${esc(t.token)}">${icon("copy")} Kopieren</button>
      <span class="muted">${esc(t.name)} · ${t.role === "viewer" ? "Nur lesen" : "Administrator"}</span></div>`, {});
}

ACTIONS.deleteToken = (el) => {
  const t = tokensCache.find((x) => x.id === el.dataset.id);
  if (!t) return;
  confirmDialog("Token löschen", `Soll das Token <b>${esc(t.name)}</b> gelöscht werden? Skripte, die es benutzen, verlieren sofort den Zugriff.`, "Löschen", async () => {
    await api("DELETE", "/api/tokens/" + t.id);
    toast("Token gelöscht");
    render();
  });
};

/* Übersicht regelmäßig aktualisieren */
setInterval(() => { if (state.user && state.route === "uebersicht" && !modal.open && document.visibilityState === "visible") VIEWS.uebersicht($(".main")); }, 15000);

boot();
