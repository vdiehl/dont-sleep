"use strict";

const $ = (id) => document.getElementById(id);
const els = {
  card: $("statusCard"), badge: $("statusBadge"), text: $("statusText"), detail: $("statusDetail"),
  banner: $("adminBanner"),
  prevent: $("btnPrevent"), restorePrev: $("btnRestorePrev"), restoreDefault: $("btnRestoreDefault"),
  coreRows: $("coreRows"), advancedRows: $("advancedRows"),
  defaultsInfo: $("defaultsInfo"), previousInfo: $("previousInfo"),
  toast: $("toast"), rowTpl: $("rowTpl"),
};

let busy = false;

function fmtValue(unit, v) {
  if (v === null || v === undefined) return "n/a";
  if (unit === "percent") return `${v}%`;
  if (unit === "bool") return v ? "On" : "Off";
  // seconds
  if (v === 0) return "Never";
  if (v % 3600 === 0) { const h = v / 3600; return `${h} hour${h > 1 ? "s" : ""}`; }
  if (v % 60 === 0) return `${v / 60} min`;
  return `${v} sec`;
}

function snapSummary(snap) {
  if (!snap) return "Not saved yet.";
  const when = (snap.capturedAt || "").replace("T", " ");
  const parts = Object.keys(snap.settings || {}).join(", ");
  return `Saved ${when}\n${parts}`;
}

let toastTimer = null;
function toast(msg, kind = "ok") {
  els.toast.textContent = msg;
  els.toast.className = `toast ${kind}`;
  els.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { els.toast.hidden = true; }, 3800);
}

function valSpan(span, unit, v, preventValue) {
  span.textContent = fmtValue(unit, v);
  span.classList.remove("is-prevented-val", "is-awake-val", "is-na");
  if (v === null || v === undefined) span.classList.add("is-na");
  else if (v === preventValue) span.classList.add("is-prevented-val");
  else span.classList.add("is-awake-val");
}

function makeRow(s) {
  const node = els.rowTpl.content.firstElementChild.cloneNode(true);
  const cb = node.querySelector(".row-enable");
  cb.checked = s.enabled;
  cb.disabled = busy;
  cb.addEventListener("change", () => setEnabled(s.key, cb.checked));

  node.querySelector(".row-name").textContent = s.name;
  let note = s.note;
  if (!s.available) {
    if (s.hidden) note += s.needsAdmin ? "  ⚠ needs admin to enable" : "  (hidden — enabled when you Prevent)";
    else note += "  (not present on this PC)";
  }
  node.querySelector(".row-note").textContent = note;
  if (!s.available) node.classList.add("unavailable");

  valSpan(node.querySelector(".val-ac"), s.unit, s.ac, s.preventValue);
  valSpan(node.querySelector(".val-dc"), s.unit, s.dc, s.preventValue);
  return node;
}

function render(data) {
  if (data.error) {
    els.text.textContent = "Error";
    els.detail.textContent = data.error;
    els.card.className = "status-card can-sleep";
    return;
  }

  if (data.prevented) {
    els.card.className = "status-card is-prevented";
    els.text.textContent = "Sleep prevented";
    els.detail.textContent = "All enabled settings are at their keep-awake value.";
  } else {
    els.card.className = "status-card can-sleep";
    els.text.textContent = "PC can sleep";
    els.detail.textContent = "One or more enabled settings will let the PC power down.";
  }

  els.coreRows.innerHTML = "";
  els.advancedRows.innerHTML = "";
  data.settings.forEach((s) => {
    const target = s.category === "core" ? els.coreRows : els.advancedRows;
    target.appendChild(makeRow(s));
  });

  const needAdmin = data.settings.filter((s) => s.enabled && s.needsAdmin).map((s) => s.name);
  if (needAdmin.length) {
    els.banner.hidden = false;
    els.banner.textContent =
      `⚠ Enabled hidden setting(s) need administrator rights: ${needAdmin.join(", ")}. ` +
      `Re-run Don't Sleep as administrator to control them.`;
  } else {
    els.banner.hidden = true;
  }

  els.defaultsInfo.textContent = snapSummary(data.defaults);
  els.previousInfo.textContent = snapSummary(data.previous);

  els.prevent.disabled = busy || !!data.prevented;
  els.prevent.textContent = data.prevented ? "Sleep already prevented" : "Prevent sleep";
  els.restorePrev.disabled = busy || !data.hasPrevious;
  els.restoreDefault.disabled = busy || !data.hasDefaults;
}

async function call(url, opts) {
  const res = await fetch(url, opts);
  let data;
  try { data = await res.json(); } catch { data = { error: `HTTP ${res.status}` }; }
  if (!res.ok && !data.error) data.error = `HTTP ${res.status}`;
  return data;
}

async function refresh() {
  if (busy) return;
  const data = await call("/api/status");
  render(data);
}

async function setEnabled(key, value) {
  busy = true;
  const data = await call("/api/config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled: { [key]: value } }),
  });
  busy = false;
  render(data);
  if (data.error) toast(data.error, "err");
}

async function doAction(url, label) {
  busy = true;
  els.detail.textContent = `${label}…`;
  [els.prevent, els.restorePrev, els.restoreDefault].forEach((b) => (b.disabled = true));
  const data = await call(url, { method: "POST" });
  busy = false;
  render(data);
  if (data.error) toast(data.error, "err");
  else toast(`${label} ✓`, "ok");
}

els.prevent.addEventListener("click", () => doAction("/api/prevent", "Preventing sleep"));
els.restorePrev.addEventListener("click", () => doAction("/api/restore-previous", "Restoring previous"));
els.restoreDefault.addEventListener("click", () => doAction("/api/restore-defaults", "Restoring defaults"));

refresh().catch((e) => toast(String(e), "err"));
setInterval(refresh, 4000); // reflect changes made via the CLI
