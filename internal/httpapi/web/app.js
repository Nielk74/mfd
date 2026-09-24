const $ = (id) => document.getElementById(id);
let selected = null,
  current = null,
  pendingKey = null,
  renderedRunList = "",
  busy = false;
const money = (v) =>
  new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(
    Number(v),
  );
const clock = (v) => new Date(v).toISOString().slice(11, 19);
function node(tag, text, className) {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (className) n.className = className;
  return n;
}
async function request(url, options) {
  const response = await fetch(url, options);
  const data = await response.json();
  if (!response.ok)
    throw new Error(data.detail || data.error || `HTTP ${response.status}`);
  return data;
}
function row(parent, values) {
  const tr = node("tr");
  for (const value of values) tr.append(node("td", value));
  parent.append(tr);
}
function selectRun(id) {
  selected = id;
  current = null;
  $("result").hidden = true;
  $("export").hidden = true;
  $("run-title").textContent = `Replay ${id.slice(0, 8)}`;
  $("run-meta").textContent = "Loading selected replay…";
}
async function refresh() {
  if (busy) return;
  busy = true;
  try {
    const { runs } = await request("/api/v1/runs");
    $("run-count").textContent = runs.length;
    if (!selected && runs.length) selected = runs[0].id;
    const listKey = JSON.stringify([
      selected,
      runs.map((r) => [r.id, r.status]),
    ]);
    if (listKey !== renderedRunList) {
      const focusedRun = document.activeElement?.dataset?.runId;
      $("runs").replaceChildren();
      if (!runs.length)
        $("runs").append(
          node("p", "No runs yet. Start a fixture replay.", "muted"),
        );
      for (const run of runs) {
        const b = node("button");
        b.type = "button";
        b.dataset.runId = run.id;
        b.setAttribute("aria-pressed", String(run.id === selected));
        b.append(
          node("strong", `${run.status} · ${run.id.slice(0, 8)}`),
          node("small", new Date(run.created_at).toLocaleString()),
        );
        b.onclick = () => {
          selectRun(run.id);
          refresh();
        };
        $("runs").append(b);
        if (focusedRun === run.id) b.focus();
      }
      renderedRunList = listKey;
    }
    if (selected) {
      const requestedID = selected;
      const run = await request(`/api/v1/runs/${requestedID}`);
      if (
        selected === requestedID &&
        (!current || current.id !== run.id || current.status !== run.status)
      ) {
        current = run;
        render(run);
      }
    }
    $("status").textContent =
      "Lab connected. Fixture results are stored in PostgreSQL.";
  } catch (error) {
    $("status").textContent =
      `Lab unavailable: ${error.message}. Previously displayed results may be out of date.`;
  } finally {
    busy = false;
  }
}
function render(run) {
  $("run-title").textContent = `Replay ${run.id.slice(0, 8)}`;
  $("run-meta").textContent = `${run.status} · ${run.mode} · ${run.actor}`;
  $("export").hidden = false;
  $("export").href = `/api/v1/runs/${run.id}`;
  $("result").hidden = !run.result;
  if (run.error) $("run-meta").textContent += ` · ${run.error}`;
  if (!run.result) return;
  const data = run.result,
    latest = new Map();
  for (const s of data.snapshots) latest.set(s.sleeve, s);
  const totals = [...latest.values()].reduce(
    (a, s) => ({
      equity: a.equity + Number(s.equity),
      pnl: a.pnl + Number(s.unrealized_pnl),
    }),
    { equity: 0, pnl: 0 },
  );
  $("equity").textContent = money(totals.equity);
  $("pnl").textContent = money(totals.pnl);
  $("decisions-count").textContent = data.decisions.length;
  $("sleeves").replaceChildren();
  for (const s of latest.values())
    row($("sleeves"), [
      s.sleeve,
      money(s.cash),
      money(s.equity),
      money(s.liquidation_equity),
      money(s.unrealized_pnl),
    ]);
  const points = new Map();
  for (const s of data.snapshots) {
    const p = points.get(s.at) || { equity: 0, pnl: 0 };
    p.equity += Number(s.equity);
    p.pnl += Number(s.unrealized_pnl);
    points.set(s.at, p);
  }
  $("history").replaceChildren();
  for (const [at, p] of points)
    row($("history"), [clock(at) + " UTC", money(p.equity), money(p.pnl)]);
  $("hash").textContent = data.dataset_hash;
  $("policy").textContent = data.valuation_version;
  renderDecisions();
}
function renderDecisions() {
  $("decisions").replaceChildren();
  if (!current?.result) return;
  for (const d of current.result.decisions) {
    if ($("filter").value !== "all" && d.sleeve !== $("filter").value) continue;
    const detail = node("details"),
      summary = node("summary");
    summary.append(
      node("span", d.action, `badge ${d.action}`),
      node("strong", d.sleeve),
      node("time", clock(d.at) + " UTC"),
    );
    detail.append(summary);
    const body = node("div", undefined, "reason");
    body.append(node("p", d.reason), node("p", `${d.actor} · ${d.strategy}`));
    const ul = node("ul");
    for (const check of d.checks) ul.append(node("li", check));
    body.append(ul);
    body.append(
      node("p", "Captured input snapshot"),
      node(
        "pre",
        JSON.stringify(current.result.snapshots[d.snapshot_index], null, 2),
      ),
    );
    detail.append(body);
    $("decisions").append(detail);
  }
}
$("filter").onchange = renderDecisions;
$("replay").onclick = async () => {
  $("replay").disabled = true;
  // randomUUID requires HTTPS (or localhost). getRandomValues also works on LAN HTTP.
  pendingKey ??= Array.from(
    crypto.getRandomValues(new Uint8Array(16)),
    (byte) => byte.toString(16).padStart(2, "0"),
  ).join("");
  try {
    const data = await request("/api/v1/replays", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": pendingKey,
      },
      body: "{}",
    });
    selectRun(data.run_id);
    pendingKey = null;
    $("status").textContent = "Replay queued. Waiting for a worker.";
    await refresh();
  } catch (error) {
    $("status").textContent =
      `Could not confirm replay: ${error.message}. Click again to retry the same request.`;
  } finally {
    $("replay").disabled = false;
  }
};
async function health() {
  try {
    const response = await fetch("/readyz");
    const data = await response.json();
    $("health").textContent = Object.entries(data)
      .map(([name, ok]) => `${name}: ${ok ? "ready" : "unavailable"}`)
      .join(" · ");
  } catch {
    $("health").textContent = "Dependency status unavailable";
  }
  try {
    const { revision } = await request("/healthz");
    $("revision").textContent =
      `Build: ${revision === "development" ? revision : revision.slice(0, 8)}`;
  } catch {
    $("revision").textContent = "Build: unavailable";
  }
}
refresh();
health();
setInterval(refresh, 3000);
setInterval(health, 10000);
