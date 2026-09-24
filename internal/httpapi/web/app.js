const $ = (id) => document.getElementById(id);
const ns = 'http://www.w3.org/2000/svg';
const money = (value) => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(Number(value));
const time = (value) => value ? new Date(value).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : '—';
const clock = (value) => new Date(value).toISOString().slice(11, 16) + ' UTC';
const short = (id) => id.slice(0, 8);
const node = (tag, content, className) => {
  const element = document.createElement(tag);
  if (content !== undefined) element.textContent = content;
  if (className) element.className = className;
  return element;
};
const svgNode = (tag, attributes = {}) => {
  const element = document.createElementNS(ns, tag);
  for (const [name, value] of Object.entries(attributes)) element.setAttribute(name, value);
  return element;
};
const randomKey = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('');
let runs = [], selected = null, current = null, comparison = null, pendingKey = null, busy = false;
let activeView = 'workspace', renderedList = '', lastOperations = null;
let operatorToken = '';

async function request(url, options) {
  const response = await fetch(url, options);
  const data = await response.json();
  if (!response.ok) throw new Error(data.detail || data.error || `HTTP ${response.status}`);
  return data;
}
function setStatus(message, bad = false) {
  $('status').textContent = message;
  $('status').classList.toggle('error', bad);
  $('connection').textContent = bad ? 'Lab unavailable' : 'Lab connected';
  document.querySelector('.live-dot').classList.toggle('offline', bad);
}
function showView(view) {
  activeView = view;
  for (const name of ['workspace', 'decisions', 'systems', 'broker']) {
    $(`${name}-view`).hidden = name !== view;
  }
  for (const button of document.querySelectorAll('.nav-button')) {
    const active = button.dataset.view === view;
    button.classList.toggle('active', active);
    if (active) button.setAttribute('aria-current', 'page');
    else button.removeAttribute('aria-current');
  }
  if (view === 'systems') refreshOperations();
  if (view === 'broker') refreshBrokerStatus();
}
for (const button of document.querySelectorAll('.nav-button')) button.onclick = () => showView(button.dataset.view);
$('hero-create').onclick = () => { showView('workspace'); $('composer').scrollIntoView({ behavior: 'smooth', block: 'start' }); $('experiment-name').focus(); };

const presets = {
  baseline: ['Baseline', 0, 0, 1],
  dip: ['Tech dip', -5, -10, 1],
  rally: ['Tech rally', 5, 10, 1],
  sensitive: ['Sensitive review', 0, 0, 0.5],
};
function selectPreset(name) {
  const [label, aapl, msft, threshold] = presets[name];
  $('experiment-name').value = label;
  $('aapl-shock').value = aapl;
  $('msft-shock').value = msft;
  $('review-threshold').value = threshold;
  for (const chip of document.querySelectorAll('[data-preset]')) chip.classList.toggle('selected', chip.dataset.preset === name);
}
for (const chip of document.querySelectorAll('[data-preset]')) chip.onclick = () => selectPreset(chip.dataset.preset);
for (const input of $('experiment-form').querySelectorAll('input')) input.addEventListener('input', () => {
  for (const chip of document.querySelectorAll('[data-preset]')) chip.classList.remove('selected');
});
function formExperiment() {
  const form = $('experiment-form');
  if (!form.reportValidity()) return null;
  const bps = (id) => Math.round(Number($(id).value) * 100);
  const shocks = {};
  if (bps('aapl-shock')) shocks.AAPL = bps('aapl-shock');
  if (bps('msft-shock')) shocks.MSFT = bps('msft-shock');
  return { name: $('experiment-name').value.trim(), final_shock_bps: shocks, review_threshold_bps: bps('review-threshold') };
}
async function enqueue(experiment) {
  const button = $('replay');
  button.disabled = true;
  pendingKey ??= randomKey();
  try {
    const data = await request('/api/v1/replays', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': pendingKey },
      body: JSON.stringify(experiment),
    });
    pendingKey = null;
    selectRun(data.run_id);
    setStatus('Experiment queued. Waiting for a worker.');
    await refreshRuns();
    $('selected-run').scrollIntoView({ behavior: 'smooth', block: 'start' });
  } catch (error) {
    setStatus(`Could not confirm the experiment: ${error.message}. Retry keeps the same request key.`, true);
  } finally { button.disabled = false; }
}
$('experiment-form').onsubmit = (event) => { event.preventDefault(); const experiment = formExperiment(); if (experiment) enqueue(experiment); };

function selectRun(id) {
  selected = id;
  current = null;
  comparison = null;
  $('result').hidden = true;
  $('export').hidden = true;
  $('copy-link').disabled = !id;
  $('download-csv').disabled = true;
  $('run-title').textContent = id ? `Loading ${short(id)}…` : 'Choose an experiment';
  $('run-meta').textContent = id ? 'Loading the durable record…' : 'Create or select a run to inspect its portfolio and decisions.';
  const url = new URL(location.href);
  if (id) url.searchParams.set('run', id); else url.searchParams.delete('run');
  history.replaceState(null, '', url);
  renderedList = '';
}
function renderRunList() {
  const query = $('run-search').value.trim().toLowerCase();
  const listKey = JSON.stringify([selected, query, runs.map((run) => [run.id, run.status, run.experiment?.name])]);
  if (renderedList === listKey) return;
  const focused = document.activeElement?.dataset?.runId;
  const container = $('runs');
  container.replaceChildren();
  const filtered = runs.filter((run) => `${run.experiment?.name || ''} ${run.id}`.toLowerCase().includes(query));
  if (!filtered.length) container.append(node('p', runs.length ? 'No matching experiments.' : 'No experiments yet. Run one to begin.', 'empty'));
  for (const run of filtered) {
    const button = node('button', undefined, 'run-item');
    button.type = 'button';
    button.dataset.runId = run.id;
    button.setAttribute('aria-pressed', String(run.id === selected));
    const head = node('span', undefined, 'run-item-head');
    head.append(node('strong', run.experiment?.name || 'Baseline'), node('span', run.status, `status-pill ${run.status}`));
    button.append(head, node('small', `${time(run.created_at)} · ${short(run.id)}`));
    button.onclick = () => { selectRun(run.id); renderRunList(); loadSelected(); };
    container.append(button);
    if (focused === run.id) button.focus();
  }
  renderedList = listKey;
}
$('run-search').oninput = renderRunList;

async function refreshRuns() {
  if (busy) return;
  busy = true;
  try {
    const data = await request('/api/v1/runs');
    runs = data.runs;
    $('run-count').textContent = runs.length;
    if (!selected && runs.length) selectRun(runs[0].id);
    renderRunList();
    await loadSelected();
    setStatus('Lab connected. Results are stored in PostgreSQL.');
  } catch (error) {
    setStatus(`Lab unavailable: ${error.message}. Displayed results may be old.`, true);
  } finally { busy = false; }
}
async function loadSelected() {
  if (!selected) return;
  const id = selected;
  const run = await request(`/api/v1/runs/${id}`);
  if (selected !== id) return;
  if (!current || current.status !== run.status || current.id !== run.id) {
    current = run;
    renderRun(run);
  }
}
function latestSnapshots(result) {
  const latest = new Map();
  for (const snapshot of result.snapshots) latest.set(snapshot.sleeve, snapshot);
  return latest;
}
function renderRun(run) {
  $('run-title').textContent = run.experiment?.name || `Replay ${short(run.id)}`;
  $('run-meta').textContent = `${run.status.toUpperCase()} · ${time(run.created_at)} · run ${short(run.id)}` + (run.error ? ` · ${run.error}` : '');
  $('export').href = `/api/v1/runs/${run.id}`;
  $('export').hidden = false;
  $('download-csv').disabled = !run.result;
  $('result').hidden = !run.result;
  $('equity').textContent = '—'; $('pnl').textContent = '—'; $('review-count').textContent = '—';
  if (!run.result) { $('decisions').replaceChildren(node('p', 'This experiment is still queued.', 'empty')); return; }
  const result = run.result;
  const latest = latestSnapshots(result);
  const totals = [...latest.values()].reduce((out, snapshot) => ({ equity: out.equity + Number(snapshot.equity), pnl: out.pnl + Number(snapshot.unrealized_pnl) }), { equity: 0, pnl: 0 });
  $('equity').textContent = money(totals.equity);
  $('pnl').textContent = money(totals.pnl);
  $('review-count').textContent = result.decisions.filter((decision) => decision.action === 'review').length;
  $('decision-total').textContent = result.decisions.length;
  const experiment = run.experiment || result.experiment || {};
  const shocks = experiment.final_shock_bps || {};
  $('scenario-fact').textContent = `Final shock: AAPL ${((shocks.AAPL || 0) / 100).toFixed(2)}% · MSFT ${((shocks.MSFT || 0) / 100).toFixed(2)}%`;
  $('threshold-fact').textContent = `Review threshold: ${((experiment.review_threshold_bps || 100) / 100).toFixed(2)}%`;
  $('run-clock').textContent = `Completed ${time(run.finished_at)}`;
  $('hash').textContent = result.dataset_hash;
  $('policy').textContent = result.valuation_version;
  renderChart(result);
  renderSleeves(latest);
  renderDecisions();
  updateCompareOptions();
}
function renderChart(result) {
  const points = new Map();
  for (const snapshot of result.snapshots) {
    const point = points.get(snapshot.at) || 0;
    points.set(snapshot.at, point + Number(snapshot.equity));
  }
  const entries = [...points];
  const values = entries.map(([, value]) => value);
  const min = Math.min(...values) - Math.max(4, (Math.max(...values) - Math.min(...values)) * .45);
  const max = Math.max(...values) + Math.max(4, (Math.max(...values) - Math.min(...values)) * .45);
  const width = 720, height = 250, left = 78, right = 26, top = 28, bottom = 48;
  const x = (index) => left + (index * (width - left - right)) / Math.max(1, entries.length - 1);
  const y = (value) => top + (max - value) * (height - top - bottom) / (max - min);
  const svg = svgNode('svg', { viewBox: `0 0 ${width} ${height}`, preserveAspectRatio: 'xMidYMid meet', 'aria-hidden': 'true' });
  for (let i = 0; i < 3; i++) {
    const value = min + (max - min) * i / 2;
    const yy = y(value);
    svg.append(svgNode('line', { x1: left, y1: yy, x2: width - right, y2: yy, class: 'grid-line' }));
    const label = svgNode('text', { x: 3, y: yy + 4, class: 'axis-label' }); label.textContent = money(value).replace('.00', ''); svg.append(label);
  }
  const line = entries.map(([, value], index) => `${x(index)},${y(value)}`).join(' ');
  const area = svgNode('polygon', { points: `${left},${height - bottom} ${line} ${x(entries.length - 1)},${height - bottom}`, class: 'chart-area' });
  svg.append(area, svgNode('polyline', { points: line, class: 'chart-line' }));
  entries.forEach(([at, value], index) => {
    svg.append(svgNode('circle', { cx: x(index), cy: y(value), r: 5, class: 'chart-point' }));
    const label = svgNode('text', { x: x(index), y: height - 14, 'text-anchor': 'middle', class: 'axis-label' }); label.textContent = clock(at); svg.append(label);
  });
  $('equity-chart').replaceChildren(svg);
  $('chart-legend').textContent = entries.map(([at, value]) => `${clock(at)}  ${money(value)}`).join('  ·  ');
}
function renderSleeves(latest) {
  const container = $('sleeves'); container.replaceChildren();
  for (const snapshot of latest.values()) {
    const article = node('article', undefined, 'sleeve-card');
    const top = node('div', undefined, 'sleeve-top');
    top.append(node('span', snapshot.sleeve.toUpperCase(), 'small-tag'), node('strong', money(snapshot.equity)));
    article.append(top, node('p', `${money(snapshot.cash)} cash · ${money(snapshot.unrealized_pnl)} unrealized P&L`, 'muted'));
    const table = node('table');
    const header = node('thead'); const headerRow = node('tr');
    for (const heading of ['Position', 'Units', 'Cost', 'Bid / ask']) headerRow.append(node('th', heading));
    header.append(headerRow); table.append(header);
    const body = node('tbody');
    for (const position of snapshot.positions) {
      const quote = snapshot.quotes.find((item) => item.symbol === position.symbol);
      const row = node('tr');
      for (const value of [position.symbol, position.units, money(position.cost), quote ? `${money(quote.bid)} / ${money(quote.ask)}` : 'Missing mark']) row.append(node('td', value));
      body.append(row);
    }
    table.append(body);
    const wrap = node('div', undefined, 'table-scroll'); wrap.append(table); article.append(wrap);
    container.append(article);
  }
}
function updateCompareOptions() {
  const select = $('compare-select');
  const previous = select.value;
  select.replaceChildren(node('option', 'Choose a completed run'));
  select.firstChild.value = '';
  for (const run of runs) {
    if (run.id === selected || run.status !== 'completed') continue;
    const option = node('option', `${run.experiment?.name || 'Baseline'} · ${short(run.id)}`);
    option.value = run.id; select.append(option);
  }
  select.value = [...select.options].some((option) => option.value === previous) ? previous : '';
  if (!select.value) { comparison = null; $('comparison').replaceChildren(node('p', 'Select another run to see the final mark and decision difference.', 'empty')); }
}
$('compare-select').onchange = async () => {
  const id = $('compare-select').value;
  if (!id) { comparison = null; $('comparison').replaceChildren(node('p', 'Select another run to compare.', 'empty')); return; }
  try { comparison = await request(`/api/v1/runs/${id}`); renderComparison(); }
  catch (error) { $('comparison').textContent = `Comparison unavailable: ${error.message}`; }
};
function renderComparison() {
  if (!current?.result || !comparison?.result) return;
  const summary = (run) => {
    const values = [...latestSnapshots(run.result).values()];
    return { equity: values.reduce((sum, item) => sum + Number(item.equity), 0), reviews: run.result.decisions.filter((item) => item.action === 'review').length };
  };
  const left = summary(current), right = summary(comparison);
  const container = $('comparison'); container.replaceChildren();
  const grid = node('div', undefined, 'compare-grid');
  for (const [label, value] of [[current.experiment?.name || 'Selected', money(left.equity)], [comparison.experiment?.name || 'Other', money(right.equity)], ['Mark difference', money(left.equity - right.equity)], ['Review difference', String(left.reviews - right.reviews)]]) {
    const block = node('div'); block.append(node('span', label), node('strong', value)); grid.append(block);
  }
  container.append(grid, node('p', 'Difference is caused by artificial quotes or a review threshold. It is not an investment return.', 'footnote'));
}
function renderDecisions() {
  const container = $('decisions'); container.replaceChildren();
  if (!current?.result) { container.append(node('p', 'Select a completed experiment to inspect decisions.', 'empty')); return; }
  const sleeve = $('filter').value, action = $('action-filter').value, query = $('decision-search').value.trim().toLowerCase();
  const filtered = current.result.decisions.filter((d) => (sleeve === 'all' || d.sleeve === sleeve) && (action === 'all' || d.action === action) && `${d.reason} ${d.strategy} ${d.sleeve}`.toLowerCase().includes(query));
  if (!filtered.length) container.append(node('p', 'No decisions match these filters.', 'empty'));
  for (const decision of filtered) {
    const detail = node('details', undefined, 'decision');
    const summary = node('summary');
    summary.append(node('span', decision.action.toUpperCase(), `status-pill ${decision.action}`), node('strong', decision.sleeve), node('span', decision.strategy, 'muted'), node('time', clock(decision.at)));
    const body = node('div', undefined, 'decision-body');
    body.append(node('p', decision.reason), node('p', `Actor: ${decision.actor} · Result: no order submitted.`, 'muted'));
    const checks = node('ul'); for (const check of decision.checks) checks.append(node('li', check)); body.append(checks);
    const source = node('details', undefined, 'snapshot'); source.append(node('summary', 'Captured position and quote snapshot'), node('pre', JSON.stringify(current.result.snapshots[decision.snapshot_index], null, 2))); body.append(source);
    detail.append(summary, body); container.append(detail);
  }
}
for (const id of ['filter', 'action-filter', 'decision-search']) $(id).addEventListener(id === 'decision-search' ? 'input' : 'change', renderDecisions);

$('copy-link').onclick = async () => {
  try {
    if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(location.href);
    else { const input = node('input'); input.value = location.href; document.body.append(input); input.select(); if (!document.execCommand('copy')) throw new Error('Clipboard unavailable'); input.remove(); }
    setStatus('Link copied to clipboard.');
  } catch { setStatus('Clipboard unavailable in this browser.', true); }
};
const csvCell = (value) => {
  const text = String(value ?? '');
  return `"${((/^[=+@\t\r]/.test(text) || (/^-/.test(text) && !/^-\d+(\.\d+)?$/.test(text))) ? "'" : '')}${text.replaceAll('"', '""')}"`;
};
$('download-csv').onclick = () => {
  if (!current?.result) return;
  const rows = [['observation_utc', 'sleeve', 'cash_usd', 'market_value_usd', 'equity_usd', 'liquidation_equity_usd', 'unrealized_pnl_usd']];
  for (const s of current.result.snapshots) rows.push([s.at, s.sleeve, s.cash, s.market_value, s.equity, s.liquidation_equity, s.unrealized_pnl]);
  const blob = new Blob([rows.map((row) => row.map(csvCell).join(',')).join('\r\n') + '\r\n'], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob); const link = node('a'); link.href = url; link.download = `mfd-${short(current.id)}-valuations.csv`; document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  setStatus('Exact decimal valuation strings exported as CSV.');
};

function statusCard(name, ready) {
  const card = node('div', undefined, 'dependency-card');
  card.append(node('span', name.toUpperCase()), node('strong', ready ? 'Ready' : 'Unavailable', ready ? 'ready' : 'unavailable'));
  return card;
}
async function refreshOperations() {
  try {
    const ops = await request('/api/v1/operations'); lastOperations = ops;
    const deps = $('dependency-cards'); deps.replaceChildren();
    for (const name of ['postgres', 'nats', 'redis']) deps.append(statusCard(name, ops.dependencies[name]));
    const db = ops.database, queue = ops.queue;
    $('queue-count').textContent = db.outbox_pending + queue.pending + queue.ack_pending;
    $('db-completed').textContent = db.runs.completed || 0;
    $('db-queued').textContent = db.runs.queued || 0;
    $('db-failed').textContent = db.runs.failed || 0;
    $('db-outbox').textContent = db.outbox_pending;
    $('db-state').textContent = ops.dependencies.postgres ? 'READY' : 'UNAVAILABLE';
    $('db-detail').textContent = `Runs and outbox size: ${(db.storage_bytes / 1024).toFixed(1)} KiB`;
    $('stream-messages').textContent = queue.messages;
    $('queue-pending').textContent = queue.pending;
    $('queue-ack').textContent = queue.ack_pending;
    $('queue-redelivered').textContent = queue.redelivered;
    $('queue-state').textContent = queue.error ? 'DEGRADED' : 'READY';
    $('queue-detail').textContent = queue.error || `Stream ${queue.stream} · ${queue.consumers} consumer${queue.consumers === 1 ? '' : 's'}`;
    $('ops-updated').textContent = `Updated ${time(ops.generated_at)}`;
    const body = $('job-rows'); body.replaceChildren();
    if (!db.recent_jobs.length) { const row = node('tr'); const cell = node('td', 'No jobs yet.'); cell.colSpan = 6; row.append(cell); body.append(row); }
    for (const job of db.recent_jobs) {
      const row = node('tr');
      for (const value of [job.name, short(job.run_id), job.status, time(job.created_at), time(job.published_at), time(job.finished_at)]) row.append(node('td', value));
      row.onclick = () => { selectRun(job.run_id); showView('workspace'); renderRunList(); loadSelected(); };
      row.tabIndex = 0;
      row.onkeydown = (event) => { if (event.key === 'Enter') row.click(); };
      body.append(row);
    }
  } catch (error) {
    $('queue-state').textContent = 'UNAVAILABLE';
    $('db-state').textContent = 'UNAVAILABLE';
    $('ops-updated').textContent = `Operations unavailable: ${error.message}`;
  }
}
$('refresh-ops').onclick = refreshOperations;

async function refreshBrokerStatus() {
  try {
    const status = await request('/api/v1/etoro/status');
    const result = status.configured ? status.last_result.replaceAll('_', ' ') : 'Not configured';
    $('broker-badge').textContent = status.configured ? status.last_result.toUpperCase().replaceAll('_', ' ') : 'NOT CONFIGURED';
    $('broker-result').textContent = result;
    $('broker-result').classList.add('small-value');
    $('broker-http').textContent = status.http_status || '—';
    $('broker-attempt').textContent = time(status.last_attempt);
    $('broker-snapshots').textContent = status.snapshot_count;
    if (!status.configured) $('broker-message').textContent = 'The demo connector has no private credentials on this deployment.';
    else if (status.last_result === 'permission_denied') $('broker-message').textContent = 'eToro denied demo portfolio access. Check that the user key has Demo + Read permission.';
  } catch (error) { $('broker-badge').textContent = 'UNAVAILABLE'; $('broker-message').textContent = `Could not read broker status: ${error.message}`; }
}
async function loadBrokerHistory() {
  const data = await request('/api/v1/etoro/snapshots', { headers: { 'X-MFD-Operator-Token': operatorToken } });
  $('broker-history').hidden = false;
  $('broker-sync').disabled = false;
  $('broker-count').textContent = data.snapshots.length;
  const container = $('broker-records'); container.replaceChildren();
  if (!data.snapshots.length) { container.append(node('p', 'No authorized demo portfolio snapshot has been imported yet.', 'empty')); $('broker-chart').replaceChildren(); return; }
  const table = node('table'); const head = node('thead'); const headings = node('tr');
  for (const label of ['Fetched', 'Provider time', 'Total value', 'Available cash', 'Current P&L', 'Assets', 'Copies']) headings.append(node('th', label));
  head.append(headings); table.append(head);
  const body = node('tbody');
  for (const item of data.snapshots) {
    const row = node('tr'); const portfolio = item.portfolio;
    for (const value of [time(item.fetched_at), time(portfolio.provider_at), money(portfolio.total_value), money(portfolio.available_cash), money(portfolio.current_pnl), portfolio.instruments.length, portfolio.mirror_count]) row.append(node('td', value));
    body.append(row);
  }
  table.append(body); container.append(table);
  const latest = data.snapshots[0];
  const details = node('details', undefined, 'raw-details'); details.append(node('summary', `Latest provider asset IDs · source ${latest.source_sha256.slice(0, 12)}`));
  const assetTable = node('table'); const assetHead = node('thead'); const assetRow = node('tr');
  for (const label of ['Instrument ID', 'Currency', 'Net units', 'Exposure USD', 'P&L asset currency', 'Leverage']) assetRow.append(node('th', label));
  assetHead.append(assetRow); assetTable.append(assetHead);
  const assetBody = node('tbody');
  for (const instrument of latest.portfolio.instruments) {
    const row = node('tr');
    for (const value of [instrument.instrument_id, instrument.asset_currency, instrument.net_units, instrument.exposure_usd, instrument.pnl_asset_currency, instrument.average_leverage]) row.append(node('td', value));
    assetBody.append(row);
  }
  assetTable.append(assetBody); details.append(assetTable); container.append(details);
  const ordered = [...data.snapshots].reverse();
  const values = ordered.map((item) => Number(item.portfolio.total_value));
  const min = Math.min(...values) - 1, max = Math.max(...values) + 1;
  const svg = svgNode('svg', { viewBox: '0 0 720 230', 'aria-hidden': 'true' });
  for (let index = 0; index < 3; index++) svg.append(svgNode('line', { x1: 65, y1: 30 + index * 72, x2: 695, y2: 30 + index * 72, class: 'grid-line' }));
  const points = values.map((value, index) => `${65 + index * 630 / Math.max(1, values.length - 1)},${30 + (max - value) * 145 / (max - min)}`).join(' ');
  svg.append(svgNode('polyline', { points, class: 'chart-line' }));
  values.forEach((value, index) => svg.append(svgNode('circle', { cx: 65 + index * 630 / Math.max(1, values.length - 1), cy: 30 + (max - value) * 145 / (max - min), r: 5, class: 'chart-point' })));
  $('broker-chart').replaceChildren(svg);
}
$('broker-unlock').onclick = async () => {
  const value = $('operator-token').value.trim();
  if (!value) { $('broker-message').textContent = 'Paste the operator token first.'; return; }
  operatorToken = value; $('operator-token').value = '';
  try { await loadBrokerHistory(); $('broker-message').textContent = 'Private demo account view unlocked for this tab.'; }
  catch (error) { operatorToken = ''; $('broker-sync').disabled = true; $('broker-message').textContent = `Could not unlock account view: ${error.message}`; }
};
$('broker-sync').onclick = async () => {
  $('broker-sync').disabled = true;
  try {
    const status = await request('/api/v1/etoro/sync', { method: 'POST', headers: { 'X-MFD-Operator-Token': operatorToken } });
    await refreshBrokerStatus();
    await loadBrokerHistory();
    $('broker-message').textContent = status.last_result === 'ok' ? 'Demo account snapshot saved.' : `Demo read returned ${status.last_result.replaceAll('_', ' ')} (HTTP ${status.http_status || '—'}).`;
  } catch (error) { $('broker-message').textContent = `Sync failed: ${error.message}`; }
  finally { $('broker-sync').disabled = !operatorToken; }
};
async function health() {
  try {
    const response = await fetch('/readyz'); const data = await response.json();
    $('health').textContent = Object.entries(data).map(([name, ok]) => `${name}: ${ok ? 'ready' : 'unavailable'}`).join(' · ');
  } catch { $('health').textContent = 'Dependency status unavailable'; }
  try { const { revision } = await request('/healthz'); $('revision').textContent = `Build: ${revision === 'development' ? revision : revision.slice(0, 8)}`; }
  catch { $('revision').textContent = 'Build: unavailable'; }
}
const initialRun = new URL(location.href).searchParams.get('run');
if (/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(initialRun || '')) selected = initialRun;
refreshRuns(); refreshOperations(); refreshBrokerStatus(); health();
setInterval(refreshRuns, 3000); setInterval(refreshOperations, 10000); setInterval(refreshBrokerStatus, 30000); setInterval(health, 10000);
