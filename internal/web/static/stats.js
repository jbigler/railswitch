// Memory bars in the detail's Services (D161): the selected worktree's
// `railswitch --json stats`, every 10 s while the page is visible. The
// event stream replaces #detail; the last figures are drawn again on it.
"use strict";

(() => {
  const detail = () => document.getElementById("detail");
  if (!detail() || !document.body.dataset.prefix) return;
  const every = 10000;
  let rows = null, slug = "", busy = false, timer = null;

  const size = (n) => {
    const u = ["B", "KiB", "MiB", "GiB", "TiB"];
    let i = 0;
    for (; n >= 1024 && i < u.length - 1; i++) n /= 1024;
    return (n >= 10 || i === 0 ? Math.round(n) : n.toFixed(1)) + " " + u[i];
  };

  function draw() {
    const d = detail();
    if (!d) return;
    d.querySelectorAll(".svc .mem").forEach((m) => m.remove());
    if (!rows || d.dataset.slug !== slug) return;
    for (const r of rows) {
      const row = [...d.querySelectorAll(".svc[data-svc]")].find((el) => el.dataset.svc === r.service);
      if (!row || !r.running || !(r.mem_limit > 0)) continue;
      const pct = Math.min(100, (100 * r.mem_used) / r.mem_limit);
      const bar = document.createElement("span");
      bar.className = "mem" + (pct >= 95 ? " crit" : pct >= 80 ? " warn" : "");
      bar.setAttribute("role", "meter");
      bar.setAttribute("aria-valuemin", "0");
      bar.setAttribute("aria-valuemax", "100");
      bar.setAttribute("aria-valuenow", String(Math.round(pct)));
      bar.title = `${r.service}: memory ${size(r.mem_used)} / ${size(r.mem_limit)} (${Math.round(pct)}%) · CPU ${r.cpu_percent.toFixed(1)}% · PIDs ${r.pids}${r.pids_limit ? " / " + r.pids_limit : ""}`;
      bar.setAttribute("aria-label", bar.title);
      const fill = document.createElement("span");
      fill.style.width = pct + "%"; // CSSOM, which the CSP allows (no style attribute in markup)
      bar.append(fill);
      (row.querySelector(".st") || row).append(bar);
    }
  }

  async function poll() {
    clearTimeout(timer);
    timer = setTimeout(poll, every);
    const s = detail()?.dataset.slug || "";
    if (!s || busy || document.visibilityState !== "visible") return;
    busy = true;
    try {
      const r = await fetch(`/p/${encodeURIComponent(document.body.dataset.prefix)}/${encodeURIComponent(s)}/stats`, { credentials: "same-origin" });
      if (r.ok) {
        rows = await r.json();
        slug = s;
      } else rows = null;
    } catch {
      rows = null;
    } finally {
      busy = false;
    }
    draw();
  }

  // #detail is replaced, not changed: watch its parent.
  new MutationObserver(() => {
    if ((detail()?.dataset.slug || "") !== slug) poll();
    else draw();
  }).observe(detail().parentElement, { childList: true });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") poll();
  });
  poll();
})();
