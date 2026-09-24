#!/usr/bin/env python3
"""Build the two provisioned Grafana views from readable panel definitions."""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / 'deploy/grafana/dashboards'
DS = {'type': 'prometheus', 'uid': 'mfd-prometheus'}

def panel(pid, title, kind, x, y, w, h, queries, description='', unit='short', decimals=None):
    defaults = {'unit': unit}
    if decimals is not None:
        defaults['decimals'] = decimals
    result = {
        'id': pid, 'title': title, 'type': kind, 'description': description,
        'datasource': DS, 'gridPos': {'x': x, 'y': y, 'w': w, 'h': h},
        'targets': [{'refId': chr(65 + i), 'expr': expr, 'legendFormat': legend} for i, (expr, legend) in enumerate(queries)],
        'fieldConfig': {'defaults': defaults, 'overrides': []},
    }
    if kind == 'stat':
        for target in result['targets']:
            target['instant'] = True
            target['range'] = False
        result['options'] = {'reduceOptions': {'values': False, 'calcs': ['lastNotNull']}, 'orientation': 'auto', 'textMode': 'auto', 'colorMode': 'value', 'graphMode': 'area', 'justifyMode': 'auto'}
    elif kind == 'timeseries':
        result['options'] = {'tooltip': {'mode': 'multi'}, 'legend': {'displayMode': 'list', 'placement': 'bottom'}}
    return result

def row(pid, title, y):
    return {'id': pid, 'title': title, 'type': 'row', 'gridPos': {'x': 0, 'y': y, 'w': 24, 'h': 1}, 'collapsed': False, 'panels': []}

def note(pid, title, y, content):
    return {'id': pid, 'title': title, 'type': 'text', 'gridPos': {'x': 0, 'y': y, 'w': 24, 'h': 3}, 'options': {'mode': 'markdown', 'content': content}}

def dashboard(uid, title, tags, panels, links):
    return {'uid': uid, 'title': title, 'schemaVersion': 41, 'version': 3, 'refresh': '10s', 'time': {'from': 'now-1h', 'to': 'now'}, 'timezone': 'browser', 'tags': tags, 'links': [{'title': name, 'type': 'link', 'url': url, 'targetBlank': True} for name, url in links], 'panels': panels}

lab = [
    row(100, 'Current artificial experiment · latest completed run', 0),
    panel(1, 'Final marked equity', 'stat', 0, 1, 6, 5, [('mfd_fixture_latest_equity_usd{scope="total"}', 'Total')], 'USD midpoint value of fixed holdings at the last artificial observation. No realized strategy return.', 'currencyUSD', 2),
    panel(2, 'Unrealized P&L', 'stat', 6, 1, 6, 5, [('mfd_fixture_latest_unrealized_pnl_usd{scope="total"}', 'Total')], 'Artificial mark minus fixed position cost. Excludes trading, fees and cash flows.', 'currencyUSD', 2),
    panel(3, 'Review decisions', 'stat', 12, 1, 6, 5, [('mfd_fixture_latest_decisions{action="review"}', 'Review')], 'Decisions asking for review in the latest completed fixture experiment. No order is sent.', 'short', 0),
    panel(4, 'Enqueue → result', 'stat', 18, 1, 6, 5, [('mfd_latest_run_duration_seconds', 'Seconds')], 'Elapsed wall time from accepting the replay to storing its result.', 's', 2),
    row(101, 'How artificial marks and decisions changed between runs', 6),
    panel(5, 'Marked equity by sleeve', 'timeseries', 0, 7, 12, 8, [('mfd_fixture_latest_equity_usd', '{{scope}}')], 'Latest completed artificial run sampled over wall clock. Each step reflects a new scenario; this is not a backtest equity curve.', 'currencyUSD', 2),
    panel(6, 'Unrealized mark to cost by sleeve', 'timeseries', 12, 7, 12, 8, [('mfd_fixture_latest_unrealized_pnl_usd', '{{scope}}')], 'Fixed holdings marked with the latest scenario quotes; no trades or fees.', 'currencyUSD', 2),
    panel(7, 'Decision actions in latest run', 'timeseries', 0, 15, 12, 8, [('mfd_fixture_latest_decisions', '{{action}}')], 'HOLD and REVIEW are durable decision records, not orders.', 'short', 0),
    panel(8, 'Durable run counts', 'timeseries', 12, 15, 12, 8, [('mfd_runs', '{{status}}')], 'Lifetime count by status in PostgreSQL.', 'short', 0),
    note(102, 'Read the underlying evidence', 23, 'All portfolio values here are **artificial fixture results**. Open the [mfd lab](https://mfd.aklein.fr) for the selected run, quote snapshots, exact decimal values, decision checks and scenario comparison. Prometheus shows aggregate signals; the lab holds the individual records.'),
]
lab[1]['fieldConfig']['defaults']['color'] = {'mode': 'fixed', 'fixedColor': 'green'}
lab[2]['fieldConfig']['defaults']['thresholds'] = {'mode': 'absolute', 'steps': [{'color': 'red', 'value': None}, {'color': 'green', 'value': 0}]}
ops = [
    row(100, 'Availability and backlog', 0),
    panel(1, 'API', 'stat', 0, 1, 4, 5, [('up{job="mfd"}', 'Scrape')], '1 means Prometheus reached /metrics. Check individual dependencies below.', 'short', 0),
    panel(2, 'Ready deps', 'stat', 4, 1, 4, 5, [('sum(mfd_dependency_ready)', 'of 3')], 'PostgreSQL, NATS and Redis ready probes. Expected value is 3.', 'short', 0),
    panel(3, 'Queued', 'stat', 8, 1, 4, 5, [('mfd_runs{status="queued"}', 'Queued')], 'Accepted in PostgreSQL but not yet completed.', 'short', 0),
    panel(4, 'Outbox', 'stat', 12, 1, 4, 5, [('mfd_outbox_pending', 'Pending')], 'Durable jobs not yet published to NATS.', 'short', 0),
    panel(5, 'Queue age', 'stat', 16, 1, 4, 5, [('mfd_oldest_queued_seconds', 'Seconds')], 'Age of oldest queued run. Zero when empty.', 's', 0),
    panel(6, 'Outbox age', 'stat', 20, 1, 4, 5, [('mfd_outbox_oldest_seconds', 'Seconds')], 'Age of oldest unpublished outbox job. Zero when empty.', 's', 0),
    row(101, 'Work moving through the pipeline', 6),
    panel(7, 'Run state', 'timeseries', 0, 7, 12, 8, [('mfd_runs', '{{status}}')], 'Durable run totals, sampled every 15 seconds.', 'short', 0),
    panel(8, 'NATS consumer', 'timeseries', 12, 7, 12, 8, [('mfd_queue_pending_messages', 'Waiting'), ('mfd_queue_ack_pending_messages', 'Awaiting ack'), ('mfd_queue_redelivered_messages', 'Redelivered')], 'Messages in the durable replay consumer. The queue can be empty while runs remain stored.', 'short', 0),
    panel(9, 'Outbox and queued age', 'timeseries', 0, 15, 12, 8, [('mfd_outbox_oldest_seconds', 'Outbox'), ('mfd_oldest_queued_seconds', 'Run')], 'Aging identifies stuck work even when current queue depth looks small.', 's', 1),
    panel(10, 'Dependency probes', 'timeseries', 12, 15, 12, 8, [('mfd_dependency_ready', '{{dependency}}')], '1 means the dependency was ready when /metrics was scraped.', 'short', 0),
    row(102, 'Durability and results', 23),
    panel(11, 'Run and outbox storage', 'timeseries', 0, 24, 8, 7, [('mfd_database_storage_bytes', 'PostgreSQL')], 'PostgreSQL relation and index size for runs and outbox only, not a disk usage metric.', 'bytes', 0),
    panel(12, 'Latest completion time', 'timeseries', 8, 24, 8, 7, [('mfd_latest_run_duration_seconds', 'Seconds')], 'Elapsed time from enqueue to committed result for the most recently completed fixture run.', 's', 2),
    panel(13, 'Failed runs', 'timeseries', 16, 24, 8, 7, [('mfd_runs{status="failed"}', 'Failed')], 'Lifetime failed run count; inspect individual failures in the lab.', 'short', 0),
    note(103, 'Inspect jobs directly', 31, 'Use [Queues & database in the lab](https://mfd.aklein.fr) to inspect the current NATS consumer, PostgreSQL run counts, outbox state and recent jobs. These panels do not expose raw SQL or private broker data.'),
    row(104, 'eToro demo connector · read-only access', 34),
    panel(14, 'Demo read succeeded', 'stat', 0, 35, 8, 5, [('mfd_etoro_demo_read_ok', '1 = available')], '0 means no successful demo portfolio read. Check the connector state in the lab; a denied key is not a valuation.', 'short', 0),
    panel(15, 'Last provider HTTP status', 'stat', 8, 35, 8, 5, [('mfd_etoro_last_http_status', 'Status')], '403 means the official demo portfolio endpoint denied access; 0 means no request has run.', 'short', 0),
    panel(16, 'Encrypted account snapshots', 'stat', 16, 35, 8, 5, [('mfd_etoro_snapshots', 'Snapshots')], 'Successful provider responses are encrypted before storage. No account value is in Prometheus.', 'short', 0),
]
ROOT.mkdir(parents=True, exist_ok=True)
for filename, data in [
    ('lab.json', dashboard('mfd-lab', 'mfd / Portfolio & decisions', ['mfd','fixture','decisions'], lab, [('Operations', '/d/mfd-operations'), ('Lab', 'https://mfd.aklein.fr')])),
    ('operations.json', dashboard('mfd-operations', 'mfd / Queue & database operations', ['mfd','operations'], ops, [('Portfolio & decisions', '/d/mfd-lab'), ('Lab', 'https://mfd.aklein.fr')]))]:
    path = ROOT / filename
    content = json.dumps(data, indent=2) + '\n'
    if '--check' in sys.argv:
        if not path.exists() or path.read_text() != content:
            raise SystemExit(f'{path} is out of date; run python3 scripts/build_dashboards.py')
    else:
        path.write_text(content)
