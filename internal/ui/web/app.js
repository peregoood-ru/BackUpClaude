'use strict';
/* The page only shows what the application's commands answer. Every text comes
   from the catalogs of the application (api/strings), every fact from the
   --json answer of a command. */

const state = {
  strings: {},
  meta: {},
  tab: 'status',
  job: null,
  polling: false,
  render: 0,
  status: null,
  restore: { mode: 'session', query: '', from: 'local', selected: null, sessions: null, projects: null, settings: null },
  snapshots: { filter: 'all', limit: 100 },
};

/* ---------- helpers ---------- */

function t(key, ...args) {
  const text = state.strings[key];
  if (text === undefined) return key;
  let i = 0;
  return text.replace(/%(?:\.(\d+))?[sdvf]/g, (match, digits) => {
    const value = args[i++];
    if (match.endsWith('f')) return Number(value).toFixed(digits ? Number(digits) : 6);
    return String(value);
  });
}

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [key, value] of Object.entries(attrs)) {
      if (value === false || value === null || value === undefined) continue;
      if (key === 'class') el.className = value;
      else if (key.startsWith('on')) el.addEventListener(key.slice(2), value);
      else if (key === 'value') el.value = value;
      else el.setAttribute(key, value === true ? '' : value);
    }
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

const ICONS = {
  ok: ['M5 12.5l4.5 4.5L19 7.5'],
  warn: ['M12 6.5v7', 'M12 17.5h.01'],
  bad: ['M7 7l10 10', 'M17 7L7 17'],
  wait: ['M12 7v5.5l3.5 2'],
};

function icon(name) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of ICONS[name]) {
    const path = document.createElementNS(ns, 'path');
    path.setAttribute('d', d);
    path.setAttribute('fill', 'none');
    path.setAttribute('stroke', 'currentColor');
    path.setAttribute('stroke-width', '2.6');
    path.setAttribute('stroke-linecap', 'round');
    path.setAttribute('stroke-linejoin', 'round');
    svg.append(path);
  }
  return svg;
}

async function api(path, options = {}) {
  const init = { method: options.method || 'GET', headers: { 'X-BackUpClaude': '1' }, credentials: 'same-origin' };
  if (options.body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(options.body);
  }
  try {
    const response = await fetch(path, init);
    return await response.json();
  } catch (e) {
    return { ok: false, error: { code: 'network', message: t('ui.error.network'), hint: t('ui.error.network_hint') } };
  }
}

function fmtNumber(value, digits) {
  return new Intl.NumberFormat(state.meta.language || undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value);
}

function fmtSize(bytes) {
  const n = Number(bytes) || 0;
  if (n >= 1e12) return t('ui.size.tb', fmtNumber(n / 1e12, 2));
  if (n >= 1e9) return t('ui.size.gb', fmtNumber(n / 1e9, 1));
  if (n >= 1e6) return t('ui.size.mb', fmtNumber(n / 1e6, 0));
  if (n >= 1e3) return t('ui.size.kb', fmtNumber(n / 1e3, 0));
  return t('ui.size.b', fmtNumber(n, 0));
}

function fmtAge(seconds) {
  const s = Number(seconds) || 0;
  if (s < 60) return t('age.now');
  if (s < 3600) return t('age.minutes', Math.floor(s / 60));
  if (s < 48 * 3600) return t('age.hours', Math.floor(s / 3600));
  return t('age.days', Math.floor(s / 86400));
}

function fmtDate(value) {
  if (!value) return '';
  const date = new Date(value);
  if (isNaN(date)) return String(value);
  return date.toLocaleString(state.meta.language || undefined, {
    day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
  });
}

function fmtClock(ms) {
  const total = Math.max(0, Math.floor(ms / 1000));
  const minutes = Math.floor(total / 60);
  return minutes + ':' + String(total % 60).padStart(2, '0');
}

function errorBox(error) {
  const e = error || { message: t('error.unexpected') };
  return h('div', { class: 'notice bad', role: 'alert' },
    h('div', {}, e.message || t('error.unexpected')),
    (e.details || []).map((line) => h('div', { class: 'muted' }, '- ' + line)),
    e.hint ? h('div', {}, e.hint) : null);
}

function copyButton(text) {
  const button = h('button', { class: 'btn small', type: 'button' }, t('ui.copy'));
  button.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(text);
      button.textContent = t('ui.copied');
    } catch (e) {
      button.textContent = t('ui.copy_failed');
    }
    setTimeout(() => { button.textContent = t('ui.copy'); }, 1800);
  });
  return button;
}

function mount(node) {
  const view = document.getElementById('view');
  view.replaceChildren(node);
}

/* ---------- shell ---------- */

const TABS = ['status', 'snapshots', 'restore', 'settings'];

function drawShell() {
  document.documentElement.lang = state.meta.language || 'en';
  document.getElementById('version').textContent = state.meta.version || '';
  const tabs = document.getElementById('tabs');
  tabs.replaceChildren(...TABS.map((name) => h('button', {
    class: 'tab', role: 'tab', type: 'button', 'aria-selected': String(state.tab === name),
    onclick: () => go(name),
  }, t('ui.tab.' + name))));
  const select = document.getElementById('lang');
  select.replaceChildren(...(state.meta.languages || []).map((code) =>
    h('option', { value: code, selected: code === state.meta.language }, t('ui.lang.' + code))));
  select.setAttribute('aria-label', t('ui.language'));
  const quit = document.getElementById('quit');
  quit.textContent = t('ui.quit');
  quit.title = t('ui.quit_hint');
}

function go(tab) {
  state.tab = tab;
  history.replaceState(null, '', '#' + tab);
  drawShell();
  draw();
}

function draw() {
  const ticket = ++state.render;
  const views = { status: viewStatus, snapshots: viewSnapshots, restore: viewRestore, settings: viewSettings };
  views[state.tab](ticket);
}

const stale = (ticket) => ticket !== state.render;

/* ---------- long operations ---------- */

async function refreshJob() {
  const env = await api('api/job');
  state.job = env.ok ? env.data.job : null;
  return state.job;
}

async function poll() {
  if (state.polling) return;
  state.polling = true;
  try {
    for (;;) {
      const job = await refreshJob();
      paintJobs();
      if (!job || job.state !== 'running') break;
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  } finally {
    state.polling = false;
  }
  if (state.job && state.job.state === 'done' && state.job.kind === 'backup' && state.tab === 'status') {
    loadStatus(state.render);
  }
}

function paintJobs() {
  for (const box of document.querySelectorAll('[data-job]')) {
    const kind = box.getAttribute('data-job');
    box.replaceChildren(...jobNodes(kind));
  }
  for (const button of document.querySelectorAll('[data-busy-disable]')) {
    button.disabled = !!(state.job && state.job.state === 'running');
  }
}

function jobBox(kind) {
  const box = h('div', { 'data-job': kind });
  box.replaceChildren(...jobNodes(kind));
  return box;
}

function running() {
  return !!(state.job && state.job.state === 'running');
}

async function cancelJob() {
  await api('api/job/cancel', { method: 'POST', body: {} });
  poll();
}

function jobNodes(kind) {
  const job = state.job;
  if (!job || job.kind !== kind) return [];
  if (job.state === 'running') {
    const elapsed = Date.now() - Date.parse(job.started);
    const label = kind === 'backup' ? t('ui.backup.running') : t('ui.restore.running');
    return [h('div', { class: 'notice' },
      h('div', { class: 'row between' },
        h('strong', {}, label),
        h('span', { class: 'muted' }, fmtClock(elapsed)),
        h('button', { class: 'btn small', type: 'button', onclick: cancelJob }, t('ui.cancel'))),
      h('div', { class: 'bar busy', role: 'progressbar', 'aria-label': label }, h('i')),
      h('p', { class: 'muted' }, t(kind === 'backup' ? 'ui.backup.running_hint' : 'ui.restore.running_hint')))];
  }
  if (job.state === 'cancelled') return [h('div', { class: 'notice warn' }, t('ui.job.cancelled'))];
  return kind === 'backup' ? backupResult(job) : restoreResult(job);
}

/* ---------- state ---------- */

function heroNode(env) {
  if (!env.ok) {
    return h('section', { class: 'card hero problem' },
      h('div', { class: 'mark' }, icon('bad')),
      h('div', { class: 'grow' },
        h('h1', {}, t('ui.hero.error')),
        h('div', { class: 'sub' }, errorBox(env.error))));
  }
  const report = env.data;
  const local = report.copies.find((c) => c.role === 'local') || {};
  const offsite = report.copies.find((c) => c.role === 'offsite') || {};
  let kind = report.verdict;
  let title = t('ui.hero.' + kind);
  let sub = '';
  let warning = '';
  if (kind === 'ok') {
    const parts = (local.profiles || []).filter((p) => p.last_snapshot)
      .map((p) => t('status.profile.last', t('status.profile.' + p.profile), fmtAge(p.age_seconds)));
    sub = parts.join('. ');
    if (!offsite.configured) {
      title = t('ui.hero.ok_local');
      warning = t('ui.hero.ok_local_sub');
    }
  } else {
    sub = t('ui.hero.' + kind + '_sub');
  }
  const todo = (report.issues || []).map((code) => h('li', {},
    h('strong', {}, t('status.issue.' + code)), ' ', h('span', { class: 'muted' }, t('ui.todo.' + code))));
  const saveNow = h('button', {
    class: 'btn primary', type: 'button', 'data-busy-disable': true, disabled: running(), onclick: startBackup,
  }, t('ui.backup.button'));
  const refresh = h('button', { class: 'btn', type: 'button', onclick: () => loadStatus(state.render) }, t('ui.refresh'));
  return h('section', { class: 'card hero ' + kind },
    h('div', { class: 'mark' }, icon(kind === 'ok' ? 'ok' : kind === 'attention' ? 'warn' : 'bad')),
    h('div', { class: 'grow' },
      h('h1', {}, title),
      sub ? h('p', { class: 'sub' }, sub) : null,
      warning ? h('div', { class: 'notice warn' }, warning) : null,
      todo.length ? h('ul', { class: 'list' }, todo) : null,
      h('div', { class: 'actions' }, saveNow, refresh),
      h('p', { class: 'muted' }, t('ui.status.time', fmtDate(report.time)))));
}

async function startBackup() {
  const env = await api('api/backup', { method: 'POST', body: { profile: 'all' } });
  if (!env.ok) {
    state.job = { kind: 'backup', state: 'failed', started: new Date().toISOString(), result: env };
    paintJobs();
    return;
  }
  state.job = { kind: 'backup', state: 'running', started: new Date().toISOString() };
  paintJobs();
  poll();
}

function backupResult(job) {
  const env = job.result || { ok: false };
  const profiles = (env.data && env.data.profiles) || [];
  const nodes = [];
  for (const p of profiles) {
    const good = p.status === 'ok' || p.status === 'locked_files';
    nodes.push(h('div', { class: 'notice ' + (good ? 'ok' : 'bad') },
      h('div', { class: 'row between' },
        h('strong', {}, t('status.profile.' + p.profile)),
        h('span', { class: 'badge ' + (good ? 'ok' : 'bad') }, t('backup.status.' + p.status))),
      good ? h('p', {}, t('ui.backup.totals', p.files, fmtSize(p.bytes), p.files_new + p.files_changed, fmtSize(p.data_added), Math.round(p.seconds))) : null,
      p.snapshot_id ? h('p', { class: 'muted' }, t('ui.backup.snapshot'), ' ', h('span', { class: 'mono' }, p.snapshot_id.slice(0, 8))) : null,
      p.error ? errorBox(p.error) : null));
  }
  if (!nodes.length) nodes.push(env.ok ? h('div', { class: 'notice ok' }, t('ui.backup.done')) : errorBox(env.error));
  return nodes;
}

function copyCard(copy) {
  const title = copy.role === 'local' ? t('ui.copy.local') : t('ui.copy.offsite');
  if (!copy.configured) {
    return h('section', { class: 'card' },
      h('h2', {}, title),
      h('div', { class: 'notice warn' }, t('status.offsite.none')));
  }
  const body = [h('p', { class: 'muted path' }, copy.location)];
  if (!copy.reachable) {
    body.push(copy.error ? errorBox(copy.error) : h('div', { class: 'notice bad' }, t('status.issue.repository_unreachable')));
  } else {
    body.push(h('ul', { class: 'list' }, (copy.profiles || []).map((p) => h('li', { class: 'row between' },
      h('span', {}, t('status.profile.' + p.profile)),
      p.last_snapshot
        ? h('span', { class: 'badge ' + (p.stale ? 'warn' : 'ok') }, t('ui.ago', fmtAge(p.age_seconds)) + (p.stale ? ' · ' + t('ui.stale') : ''))
        : h('span', { class: 'badge warn' }, t('ui.no_snapshots'))))));
    if (copy.size_bytes !== undefined) body.push(h('p', { class: 'muted' }, t('status.size', fmtSize(copy.size_bytes))));
  }
  return h('section', { class: 'card' }, h('h2', {}, title), body);
}

function disksCard(report) {
  const rows = (report.disks || []).map((d) => {
    const used = d.total ? Math.round(((d.total - d.free) / d.total) * 100) : 0;
    const bar = h('div', { class: 'bar' + (d.low ? ' low' : ''), role: 'img', 'aria-label': used + '%' }, h('i'));
    bar.firstChild.style.width = used + '%';
    return h('li', {},
      h('div', { class: 'row between' },
        h('strong', {}, d.disk),
        d.low ? h('span', { class: 'badge bad' }, t('ui.disk.low')) : null),
      bar,
      h('p', { class: 'muted' }, t('ui.disk.free', fmtSize(d.free), fmtSize(d.total)) + ' · ' +
        d.roles.map((r) => t('status.disk.role.' + r)).join(', ')));
  });
  return h('section', { class: 'card' }, h('h2', {}, t('status.disks.title')),
    rows.length ? h('ul', { class: 'list' }, rows) : h('p', { class: 'muted' }, t('status.disks.unknown')));
}

function checksCard(report) {
  const items = [];
  const check = report.check;
  if (check.state === 'never' || !check.time) items.push([t('ui.check.title'), 'warn', t('status.check.never')]);
  else {
    const when = fmtDate(check.time);
    const text = check.state === 'errors' ? t('status.check.errors', when, check.errors)
      : check.state === 'failed' ? t('status.check.failed', when) : t('status.check.ok', when);
    items.push([t('ui.check.title'), check.state === 'ok' && !check.overdue ? 'ok' : check.state === 'ok' ? 'warn' : 'bad',
      text + (check.overdue ? ' ' + t('status.check.overdue') : '')]);
  }
  const test = report.restore_test;
  if (test.state === 'never' || !test.time) items.push([t('ui.restore_test.title'), 'warn', t('status.restore_test.never')]);
  else {
    const when = fmtDate(test.time);
    const text = test.state === 'failed' ? t('status.restore_test.failed', when, test.message || '')
      : t('status.restore_test.ok', when, test.files, test.matched);
    items.push([t('ui.restore_test.title'), test.state === 'ok' && !test.overdue ? 'ok' : test.state === 'ok' ? 'warn' : 'bad',
      text + (test.overdue ? ' ' + t('status.restore_test.overdue') : '')]);
  }
  if (report.sessions) {
    items.push([t('ui.sessions.title'), 'ok', t('status.sessions', report.sessions.files, fmtAge((Date.now() - Date.parse(report.sessions.time)) / 1000))]);
  }
  if (!(report.failing_jobs || []).length) items.push([t('ui.jobs.title'), 'ok', t('status.jobs.fine')]);
  for (const f of report.failing_jobs || []) {
    items.push([t('ui.jobs.title'), 'bad', t('status.jobs.failing', f.job, fmtAge((Date.now() - Date.parse(f.time)) / 1000), f.message)]);
  }
  for (const a of report.alerts || []) {
    items.push([t('ui.alert.title'), 'bad', t('status.alert', fmtAge((Date.now() - Date.parse(a.time)) / 1000), a.text)]);
  }
  if (!(report.notify_channels || []).length) items.push([t('ui.notify.title'), 'warn', t('status.notify.off')]);
  else items.push([t('ui.notify.title'), 'ok', t('status.notify.on', (report.notify_channels || []).map((c) => t('status.channel.' + c)).join(', '))]);
  return h('section', { class: 'card' }, h('h2', {}, t('ui.checks.title')),
    h('ul', { class: 'list' }, items.map(([name, kind, text]) => h('li', {},
      h('div', { class: 'row' }, h('span', { class: 'badge ' + kind }, name)),
      h('p', {}, text)))));
}

function scheduleCard(box) {
  box.replaceChildren(h('h2', {}, t('ui.schedule.title')), h('div', { class: 'skeleton' }));
  api('api/schedule').then((env) => {
    if (!env.ok) {
      box.replaceChildren(h('h2', {}, t('ui.schedule.title')), errorBox(env.error));
      return;
    }
    const jobs = env.data.jobs || [];
    box.replaceChildren(h('h2', {}, t('ui.schedule.title')),
      jobs.length ? h('ul', { class: 'list' }, jobs.map((j) => h('li', {},
        h('div', { class: 'row between' },
          h('strong', {}, t('ui.job.' + j.job)),
          j.installed && j.in_sync ? h('span', { class: 'badge ok' }, t('ui.schedule.ok'))
            : h('span', { class: 'badge bad' }, j.installed ? t('ui.schedule.differs') : t('ui.schedule.missing'))),
        j.last_result_words ? h('p', { class: 'muted' }, t('ui.schedule.last', j.last_result_words)) : null)))
        : h('div', { class: 'notice warn' }, t('ui.schedule.none')));
  });
}

async function loadStatus(ticket) {
  const root = document.getElementById('status-root');
  if (!root) return;
  const env = await api('api/status');
  if (stale(ticket)) return;
  state.status = env;
  const cards = [];
  if (env.ok) {
    const grid = h('div', { class: 'grid' });
    const local = env.data.copies.find((c) => c.role === 'local');
    const offsite = env.data.copies.find((c) => c.role === 'offsite');
    const sched = h('section', { class: 'card' });
    grid.append(copyCard(local), copyCard(offsite), disksCard(env.data), checksCard(env.data), sched);
    scheduleCard(sched);
    cards.push(grid);
  }
  root.replaceChildren(heroNode(env), jobBox('backup'), ...cards);
}

function viewStatus(ticket) {
  const root = h('div', { id: 'status-root', class: 'stack' },
    h('section', { class: 'card hero loading' },
      h('div', { class: 'mark' }, icon('wait')),
      h('div', { class: 'grow' },
        h('h1', {}, t('ui.status.checking')),
        h('p', { class: 'sub' }, t('ui.status.checking_sub')),
        h('div', { class: 'skeleton' }))),
    jobBox('backup'));
  mount(root);
  loadStatus(ticket);
}

/* ---------- snapshots ---------- */

async function viewSnapshots(ticket) {
  mount(h('section', { class: 'card' }, h('h2', {}, t('ui.snapshots.title')), h('div', { class: 'skeleton' }), h('div', { class: 'skeleton' })));
  const env = await api('api/snapshots');
  if (stale(ticket)) return;
  if (!env.ok) { mount(h('section', { class: 'card' }, h('h2', {}, t('ui.snapshots.title')), errorBox(env.error))); return; }
  const all = env.data.snapshots || [];
  const draw = () => {
    const rows = all.filter((s) => state.snapshots.filter === 'all' || s.profile === state.snapshots.filter);
    const shown = rows.slice(0, state.snapshots.limit);
    const seg = h('div', { class: 'seg', role: 'group' }, ['all', 'claude', 'projects'].map((name) => h('button', {
      type: 'button', 'aria-pressed': String(state.snapshots.filter === name),
      onclick: () => { state.snapshots.filter = name; draw(); },
    }, t('ui.snapshots.filter.' + name))));
    const table = h('div', { class: 'tablewrap' }, h('table', {},
      h('thead', {}, h('tr', {},
        h('th', {}, t('ui.snapshots.when')), h('th', {}, t('ui.snapshots.kind')),
        h('th', { class: 'num' }, t('ui.snapshots.files')), h('th', { class: 'num' }, t('ui.snapshots.size')),
        h('th', {}, t('ui.snapshots.id')))),
      h('tbody', {}, shown.map((s) => h('tr', {},
        h('td', {}, fmtDate(s.time)),
        h('td', {}, s.profile === 'claude' || s.profile === 'projects' ? t('status.profile.' + s.profile) : s.profile),
        h('td', { class: 'num' }, s.files ? s.files.toLocaleString(state.meta.language) : ''),
        h('td', { class: 'num' }, s.bytes ? fmtSize(s.bytes) : ''),
        h('td', { class: 'mono' }, s.id))))));
    const more = rows.length > shown.length
      ? h('button', { class: 'btn', type: 'button', onclick: () => { state.snapshots.limit += 200; draw(); } }, t('ui.show_more', rows.length - shown.length))
      : null;
    mount(h('section', { class: 'card' },
      h('div', { class: 'row between' }, h('h2', {}, t('ui.snapshots.title')), seg),
      h('p', { class: 'muted' }, t('ui.snapshots.count', rows.length)),
      rows.length ? table : h('div', { class: 'notice warn' }, t('ui.snapshots.none')),
      more ? h('div', { class: 'notice' }, more) : null));
  };
  draw();
}

/* ---------- restore ---------- */

async function viewRestore(ticket) {
  const r = state.restore;
  mount(h('section', { class: 'card' }, h('h2', {}, t('ui.restore.title')), h('p', { class: 'muted' }, t('ui.restore.loading')), h('div', { class: 'skeleton' })));
  if (!r.settings) {
    const env = await api('api/settings');
    if (stale(ticket)) return;
    r.settings = env.ok ? env.data : { offsite: '' };
  }
  const wanted = r.mode === 'session' ? 'sessions' : 'projects';
  if (!r[wanted]) {
    const env = await api('api/' + wanted);
    if (stale(ticket)) return;
    if (!env.ok) { mount(h('section', { class: 'card' }, h('h2', {}, t('ui.restore.title')), errorBox(env.error))); return; }
    r[wanted] = env.data[wanted] || [];
  }
  drawRestore();
}

function matches(item, query) {
  if (!query) return true;
  const hay = [item.id, item.title, item.cwd, item.path].filter(Boolean).join(' ').toLowerCase();
  return query.toLowerCase().split(/\s+/).every((word) => hay.includes(word));
}

function drawRestore() {
  const r = state.restore;
  const isSession = r.mode === 'session';
  const items = (isSession ? r.sessions : r.projects) || [];
  const rows = items.filter((item) => matches(item, r.query)).slice(0, 200);

  const mode = h('div', { class: 'seg', role: 'group' }, ['session', 'project'].map((name) => h('button', {
    type: 'button', 'aria-pressed': String(r.mode === name),
    onclick: () => { r.mode = name; r.selected = null; r.query = ''; viewRestore(++state.render); },
  }, t('ui.restore.mode.' + name))));
  const offsiteReady = !!(r.settings && r.settings.offsite);
  const from = h('label', { class: 'field' }, h('span', {}, t('ui.restore.from')),
    h('select', { onchange: (e) => { r.from = e.target.value; } },
      h('option', { value: 'local', selected: r.from === 'local' }, t('ui.restore.from.local')),
      h('option', { value: 'offsite', disabled: !offsiteReady, selected: r.from === 'offsite' },
        offsiteReady ? t('ui.restore.from.offsite') : t('ui.restore.from.offsite_none'))));
  const search = h('label', { class: 'field grow' }, h('span', {}, t('ui.restore.search')),
    h('input', {
      type: 'search', value: r.query, placeholder: t('ui.restore.search_hint'),
      oninput: (e) => { r.query = e.target.value; drawRestoreList(); },
    }));

  const list = h('ul', { class: 'pick scroll', id: 'pick' });
  const page = h('div', { class: 'stack' },
    h('section', { class: 'card' },
      h('div', { class: 'row between' }, h('h2', {}, t('ui.restore.title')), mode),
      h('p', { class: 'muted' }, t(isSession ? 'ui.restore.intro_session' : 'ui.restore.intro_project')),
      h('div', { class: 'row' }, search, from),
      list),
    h('div', { id: 'restore-detail' }));
  mount(page);
  drawRestoreList();
  drawRestoreDetail();
}

function itemKey(item) { return state.restore.mode === 'session' ? item.id : item.path; }

function drawRestoreList() {
  const r = state.restore;
  const list = document.getElementById('pick');
  if (!list) return;
  const isSession = r.mode === 'session';
  const items = (isSession ? r.sessions : r.projects) || [];
  const rows = items.filter((item) => matches(item, r.query));
  const shown = rows.slice(0, 200);
  if (!shown.length) {
    list.replaceChildren(h('li', {}, h('div', { class: 'notice' }, t(items.length ? 'ui.restore.nothing_found' : 'ui.restore.nothing'))));
    return;
  }
  list.replaceChildren(...shown.map((item) => {
    const selected = r.selected && itemKey(r.selected) === itemKey(item);
    let title, meta, badge;
    if (isSession) {
      title = item.title || item.cwd || item.id;
      meta = [t('ui.kind.' + item.kind), fmtDate(item.modified), fmtSize(item.size), item.id].join(' · ');
      const status = item.damaged ? 'damaged' : !item.on_disk ? 'snapshot_only' : !item.in_snapshot ? 'disk_only' : 'both';
      badge = h('span', { class: 'badge ' + (status === 'snapshot_only' ? 'warn' : status === 'damaged' || status === 'disk_only' ? 'bad' : 'ok') }, t('ui.session.' + status));
    } else {
      title = item.path;
      meta = t('ui.project.sessions', item.sessions || 0) + (item.last_seen ? ' · ' + fmtDate(item.last_seen) : '');
      badge = h('span', { class: 'badge ' + (item.saved ? 'ok' : 'warn') }, t(item.saved ? 'ui.project.saved' : 'ui.project.not_saved'));
    }
    return h('li', {}, h('button', {
      type: 'button', 'aria-pressed': String(!!selected), 'aria-label': title + '. ' + meta,
      onclick: () => { r.selected = item; state.job = state.job && state.job.kind === 'restore' && state.job.state === 'running' ? state.job : null; drawRestoreList(); drawRestoreDetail(true); },
    }, h('div', { class: 'row between' }, h('span', { class: 'title' }, title), badge), h('div', { class: 'meta' }, meta)));
  }));
  if (rows.length > shown.length) list.append(h('li', {}, h('div', { class: 'notice' }, t('ui.restore.more', rows.length - shown.length))));
}

function restoreRequest(step, extra = {}) {
  const r = state.restore;
  const isSession = r.mode === 'session';
  return Object.assign({
    mode: r.mode, name: isSession ? r.selected.id : r.selected.path, from: r.from, step,
  }, extra);
}

async function startRestore(step, extra) {
  const request = restoreRequest(step, extra);
  const env = await api('api/restore', { method: 'POST', body: request });
  if (!env.ok) {
    state.job = { kind: 'restore', step, state: 'failed', started: new Date().toISOString(), result: env, request };
    paintJobs();
    return;
  }
  state.job = { kind: 'restore', step, state: 'running', started: new Date().toISOString(), request };
  paintJobs();
  poll();
}

function drawRestoreDetail(scroll) {
  const r = state.restore;
  const box = document.getElementById('restore-detail');
  if (!box) return;
  if (!r.selected) { box.replaceChildren(); return; }
  const item = r.selected;
  const isSession = r.mode === 'session';
  const unsaved = !isSession && !item.saved;
  const card = h('section', { class: 'card', id: 'restore-card' },
    h('h2', {}, isSession ? (item.title || item.cwd || item.id) : item.path),
    isSession ? h('dl', { class: 'kv' },
      h('dt', {}, t('ui.snapshots.id')), h('dd', { class: 'mono' }, item.id),
      item.cwd ? h('dt', {}, t('ui.restore.folder')) : null, item.cwd ? h('dd', { class: 'path' }, item.cwd) : null,
      h('dt', {}, t('ui.restore.changed')), h('dd', {}, fmtDate(item.modified))) : null,
    unsaved ? h('div', { class: 'notice warn' }, t('ui.restore.not_saved')) : null,
    h('div', { class: 'stack' },
      h('div', {},
        h('button', { class: 'btn primary', type: 'button', 'data-busy-disable': true, disabled: running() || unsaved, onclick: () => startRestore('copy') }, t('ui.restore.copy')),
        h('p', { class: 'muted' }, t('ui.restore.copy_hint'))),
      h('div', {},
        h('button', { class: 'btn', type: 'button', 'data-busy-disable': true, disabled: running() || unsaved, onclick: () => startRestore('preview') }, t('ui.restore.place')),
        h('p', { class: 'muted' }, t('ui.restore.place_hint')))),
    jobBox('restore'));
  box.replaceChildren(card);
  if (scroll) card.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
}

function resumeNodes(report) {
  const nodes = [];
  for (const hint of report.resume || []) {
    if (hint.desktop) { nodes.push(h('p', {}, t('restore.resume_desktop'))); continue; }
    nodes.push(h('div', {},
      h('p', {}, hint.cwd ? t('ui.restore.resume_in', hint.cwd) : t('ui.restore.resume_here')),
      h('div', { class: 'copyrow' }, h('code', {}, hint.command), copyButton(hint.command))));
  }
  return nodes;
}

function restoreResult(job) {
  const env = job.result || { ok: false };
  if (job.state === 'failed' || !env.ok) {
    const nodes = [errorBox(env.error)];
    if (env.data && env.data.apply && env.data.apply.running && env.data.apply.running.length) nodes.push(h('p', { class: 'muted' }, t('ui.restore.close_claude')));
    return nodes;
  }
  const report = env.data;
  const apply = report.apply;
  const fetched = report.restored;
  if (job.step === 'copy') {
    return [h('div', { class: 'notice ok' },
      h('strong', {}, t('ui.restore.copied')),
      fetched ? h('p', {}, t('ui.restore.copied_to'), ' ', h('span', { class: 'path mono' }, fetched.target)) : null,
      fetched ? h('p', { class: 'muted' }, t('ui.restore.files', fetched.files, fmtSize(fetched.bytes)) + (fetched.verified ? ' · ' + t('ui.restore.verified') : '')) : null,
      resumeNodes(report))];
  }
  if (job.step === 'preview' && apply) {
    const nodes = [h('div', { class: 'notice' },
      h('strong', {}, t('ui.restore.preview')),
      h('p', {}, t('ui.restore.preview_count', apply.planned)),
      apply.existing ? h('p', { class: 'muted' }, t('ui.restore.preview_existing', apply.existing, apply.differ)) : null,
      skippedNodes(apply))];
    if (apply.planned > 0 && job.plan) {
      nodes.push(h('div', { class: 'notice warn' },
        h('p', {}, t('ui.restore.confirm_note')),
        h('button', { class: 'btn danger', type: 'button', 'data-busy-disable': true, disabled: running(), onclick: () => confirmPlace(job, apply) }, t('ui.restore.place_go'))));
    } else {
      nodes.push(h('div', { class: 'notice ok' }, t('ui.restore.nothing_to_place')));
    }
    return nodes;
  }
  if (job.step === 'apply' && apply) {
    return [h('div', { class: 'notice ok' },
      h('strong', {}, t('ui.restore.placed')),
      h('p', {}, t('ui.restore.placed_count', apply.copied, fmtSize(apply.bytes))),
      apply.existing ? h('p', { class: 'muted' }, t('ui.restore.preview_existing', apply.existing, apply.differ)) : null,
      skippedNodes(apply), resumeNodes(report))];
  }
  return [h('div', { class: 'notice ok' }, t('ui.restore.copied'))];
}

function skippedNodes(apply) {
  return Object.entries(apply.skipped || {}).map(([reason, count]) => h('p', { class: 'muted' }, t('restore.apply.skipped.' + reason, count)));
}

function confirmPlace(job, apply) {
  const overlay = document.getElementById('overlay');
  const close = () => overlay.replaceChildren();
  const go = h('button', { class: 'btn danger', type: 'button' }, t('ui.restore.confirm_yes'));
  go.addEventListener('click', () => {
    close();
    startRestore('apply', { plan: job.plan, confirm: true });
  });
  const cancel = h('button', { class: 'btn', type: 'button', onclick: close }, t('ui.cancel'));
  const name = state.restore.mode === 'session'
    ? (state.restore.selected.title || state.restore.selected.cwd || state.restore.selected.id)
    : state.restore.selected.path;
  overlay.replaceChildren(h('div', { class: 'modal-back', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'confirm-title' },
    h('div', { class: 'modal' },
      h('h2', { id: 'confirm-title' }, t('ui.restore.confirm_title')),
      h('p', {}, name),
      h('p', {}, t('ui.restore.preview_count', apply.planned)),
      h('p', { class: 'muted' }, t('ui.restore.confirm_text')),
      h('div', { class: 'actions' }, cancel, go))));
  cancel.focus();
}

/* ---------- settings ---------- */

async function viewSettings(ticket) {
  mount(h('section', { class: 'card' }, h('h2', {}, t('ui.settings.title')), h('div', { class: 'skeleton' })));
  const env = await api('api/settings');
  if (stale(ticket)) return;
  if (!env.ok) { mount(h('section', { class: 'card' }, h('h2', {}, t('ui.settings.title')), errorBox(env.error))); return; }
  drawSettings(env.data);
}

function listEditor(title, hint, items, onChange) {
  const values = items.slice();
  const holder = h('div');
  const paint = () => {
    const input = h('input', { type: 'text', 'aria-label': title, placeholder: t('ui.settings.add_placeholder') });
    const add = () => {
      const value = input.value.trim();
      if (!value) return;
      values.push(value);
      onChange(values);
      paint();
    };
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
    holder.replaceChildren(
      values.length ? h('ul', { class: 'list' }, values.map((value, index) => h('li', { class: 'row between' },
        h('span', { class: 'path mono grow' }, value),
        h('button', { class: 'btn small', type: 'button', onclick: () => { values.splice(index, 1); onChange(values); paint(); } }, t('ui.remove')))))
        : h('p', { class: 'muted' }, t('ui.settings.empty')),
      h('div', { class: 'row' }, input, h('button', { class: 'btn small', type: 'button', onclick: add }, t('ui.add'))));
  };
  paint();
  return h('div', {}, h('h3', {}, title), h('p', { class: 'muted' }, hint), holder);
}

function drawSettings(settings) {
  const draft = {
    language: settings.language,
    retention: Object.assign({}, settings.retention),
    deletion_drop_percent: settings.deletion_drop_percent,
    extra: settings.extra.slice(),
    exclude: settings.exclude.slice(),
    tray: settings.tray,
  };
  const message = h('div', { 'aria-live': 'polite' });

  const number = (label, obj, key, min) => h('label', { class: 'field' }, h('span', {}, label),
    h('input', { type: 'number', min: String(min), value: String(obj[key]), oninput: (e) => { obj[key] = e.target.value === '' ? 0 : Number(e.target.value); } }));

  const language = h('label', { class: 'field' }, h('span', {}, t('ui.settings.language')),
    h('select', { onchange: (e) => { draft.language = e.target.value; } },
      h('option', { value: '', selected: draft.language === '' }, t('ui.settings.language_auto')),
      (state.meta.languages || []).map((code) => h('option', { value: code, selected: draft.language === code }, t('ui.lang.' + code)))));

  const tray = h('label', { class: 'row' },
    h('input', { type: 'checkbox', checked: draft.tray, disabled: !settings.tray_supported, onchange: (e) => { draft.tray = e.target.checked; } }),
    h('span', {}, t('ui.settings.tray')));

  const save = h('button', { class: 'btn primary', type: 'button' }, t('ui.settings.save'));
  save.addEventListener('click', async () => {
    save.disabled = true;
    const body = {
      language: draft.language, retention: draft.retention, deletion_drop_percent: draft.deletion_drop_percent,
      extra: draft.extra, exclude: draft.exclude,
    };
    if (settings.tray_supported) body.tray = draft.tray;
    const env = await api('api/settings', { method: 'PUT', body });
    save.disabled = false;
    if (!env.ok) { message.replaceChildren(errorBox(env.error)); return; }
    if (draft.language && draft.language !== state.meta.language) {
      state.meta.language = draft.language;
      await loadStrings();
      drawShell();
    }
    drawSettings(env.data);
    document.getElementById('settings-message').replaceChildren(h('div', { class: 'notice ok' }, t('ui.settings.saved')));
  });

  const readonly = h('dl', { class: 'kv' },
    h('dt', {}, t('ui.settings.local')), h('dd', { class: 'path mono' }, settings.local),
    h('dt', {}, t('ui.settings.offsite')), h('dd', { class: 'path mono' }, settings.offsite || t('ui.settings.offsite_none')),
    h('dt', {}, t('ui.settings.data')), h('dd', { class: 'path mono' }, settings.data_folder),
    h('dt', {}, t('ui.settings.schedule')), h('dd', {}, t('ui.settings.schedule_text',
      settings.schedule.claude_every_hours, settings.schedule.projects_every_hours, Math.round(settings.schedule.check_every_hours / 24))),
    h('dt', {}, t('ui.settings.channels')), h('dd', {}, settings.channels.length ? settings.channels.map((c) => t('status.channel.' + c)).join(', ') : t('ui.settings.channels_none')),
    h('dt', {}, t('ui.settings.password')), h('dd', {}, settings.password_stored ? t('ui.settings.password_stored') : t('ui.settings.password_missing')));

  mount(h('div', { class: 'stack' },
    h('section', { class: 'card' }, h('h2', {}, t('ui.settings.title')), readonly,
      h('div', { class: 'notice' }, t('ui.settings.password_hint'))),
    h('section', { class: 'card' }, h('h2', {}, t('ui.settings.change')),
      h('div', { class: 'stack' },
        language,
        h('div', {}, h('h3', {}, t('ui.settings.retention')), h('p', { class: 'muted' }, t('ui.settings.retention_hint')),
          h('div', { class: 'row' },
            number(t('ui.settings.hourly'), draft.retention, 'hourly', 0), number(t('ui.settings.daily'), draft.retention, 'daily', 0),
            number(t('ui.settings.weekly'), draft.retention, 'weekly', 0), number(t('ui.settings.monthly'), draft.retention, 'monthly', 0))),
        h('div', {}, h('h3', {}, t('ui.settings.drop')), h('p', { class: 'muted' }, t('ui.settings.drop_hint')),
          number(t('ui.settings.drop_percent'), draft, 'deletion_drop_percent', 1)),
        listEditor(t('ui.settings.extra'), t('ui.settings.extra_hint'), draft.extra, (v) => { draft.extra = v; }),
        listEditor(t('ui.settings.exclude'), t('ui.settings.exclude_hint'), draft.exclude, (v) => { draft.exclude = v; }),
        h('div', {}, h('h3', {}, t('ui.settings.tray_title')), h('p', { class: 'muted' }, settings.tray_supported ? t('ui.settings.tray_hint') : t('ui.settings.tray_unsupported')), tray),
        h('div', { class: 'row' }, save), message, h('div', { id: 'settings-message' })))));
}

/* ---------- start ---------- */

async function loadStrings() {
  const env = await api('api/strings');
  if (env.ok) {
    state.strings = env.data.strings;
    state.meta.language = env.data.language;
  }
}

function closedScreen() {
  document.getElementById('overlay').replaceChildren(h('div', { class: 'closed' }, h('div', {},
    h('h1', {}, t('ui.closed.title')), h('p', { class: 'muted' }, t('ui.closed.text')))));
}

function keepAlive() {
  const stream = new EventSource('api/events');
  let failures = 0;
  stream.addEventListener('hello', () => { failures = 0; });
  stream.addEventListener('bye', () => { stream.close(); closedScreen(); });
  stream.onerror = () => {
    failures++;
    if (failures >= 4) { stream.close(); closedScreen(); }
  };
}

async function start() {
  const meta = await api('api/meta');
  if (meta.ok) state.meta = meta.data;
  await loadStrings();
  const wanted = location.hash.replace('#', '');
  if (TABS.includes(wanted)) state.tab = wanted;
  document.title = t('ui.title');
  drawShell();
  document.getElementById('lang').addEventListener('change', async (e) => {
    await api('api/lang', { method: 'POST', body: { language: e.target.value } });
    await loadStrings();
    document.title = t('ui.title');
    drawShell();
    draw();
  });
  document.getElementById('quit').addEventListener('click', async () => {
    if (running() && !confirm(t('ui.quit_busy'))) return;
    await api('api/quit', { method: 'POST', body: {} });
    closedScreen();
  });
  keepAlive();
  await refreshJob();
  draw();
  if (running()) poll();
}

start();
