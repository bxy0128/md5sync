'use strict';

const $ = (id) => document.getElementById(id);

const ui = {
  rows: $('rows'),
  empty: $('empty'),
  bar: $('bar'),
  status: $('status'),
  json: $('json'),
  start: $('btn-start'),
  copy: $('btn-copy'),
  save: $('btn-save'),
  threads: $('threads'),
  auth: $('auth'),
  dropzone: $('dropzone'),
  toast: $('toast'),
};

const state = {
  paths: [],
  rows: [],
  running: false,
};

function fmtSize(n) {
  if (n === undefined || n === null) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = Number(n);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (i === 0 ? v : v.toFixed(v >= 100 ? 0 : 1)) + ' ' + units[i];
}

function fmtDur(sec) {
  const s = Math.max(0, Math.round(Number(sec) || 0));
  if (s < 60) return s + ' 秒';
  const m = Math.floor(s / 60);
  const r = s % 60;
  if (m < 60) return m + ' 分 ' + r + ' 秒';
  return Math.floor(m / 60) + ' 小时 ' + (m % 60) + ' 分';
}

function baseName(p) {
  const s = String(p || '').replace(/[?#].*$/, '');
  const parts = s.split(/[\\/]/);
  return parts[parts.length - 1] || s;
}

function errText(e) {
  if (!e) return '未知错误';
  if (typeof e === 'string') return e;
  return e.message || String(e);
}

function setStatus(text) {
  ui.status.textContent = text;
}

let toastTimer = null;
function toast(msg) {
  ui.toast.textContent = msg;
  ui.toast.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => ui.toast.classList.remove('show'), 2200);
}

/* ---------- 表格渲染 ---------- */

function buildTable() {
  ui.rows.innerHTML = '';
  state.rows.forEach((row, i) => {
    const tr = document.createElement('tr');
    const mk = (cls, text) => {
      const td = document.createElement('td');
      if (cls) td.className = cls;
      td.textContent = text || '';
      tr.appendChild(td);
      return td;
    };
    row.dom = {
      tr: tr,
      index: mk('col-index', String(i + 1)),
      name: mk('name-cell', ''),
      size: mk('size', ''),
      md5: mk('hash', ''),
      slice: mk('hash', ''),
      state: mk('state', ''),
    };
    row.dom.name.title = row.name;
    ui.rows.appendChild(tr);
    updateRow(row);
  });
  ui.empty.style.display = state.rows.length ? 'none' : 'grid';
}

function updateRow(row) {
  const d = row.dom;
  if (!d) return;
  d.name.textContent = row.name;
  d.name.title = row.name;
  d.size.textContent = row.result ? fmtSize(row.result.size) : '—';
  d.md5.textContent = row.result ? row.result.md5 : '';
  d.slice.textContent = row.result ? row.result.slice_md5 : '';
  d.state.className = 'state' + (row.kind ? ' ' + row.kind : '');
  d.state.textContent = row.status;
  if (row.kind === 'running') {
    const pct = Math.max(0, Math.min(100, row.pct || 0));
    const bar = document.createElement('div');
    bar.className = 'row-progress';
    const fill = document.createElement('i');
    fill.style.width = pct + '%';
    bar.appendChild(fill);
    d.state.appendChild(bar);
  }
}

/* ---------- 添加 / 清空 ---------- */

function rowsFromPaths(paths) {
  return paths.map((p) => ({
    name: baseName(p),
    status: '待计算',
    kind: '',
    pct: 0,
    result: null,
  }));
}

function addPaths(paths) {
  if (!paths || !paths.length) return;
  let added = 0;
  for (const raw of paths) {
    const p = String(raw || '').trim().replace(/^"(.*)"$/, '$1');
    if (!p) continue;
    if (state.paths.indexOf(p) >= 0) continue;
    state.paths.push(p);
    added++;
  }
  if (!added) {
    toast('没有新增内容');
    return;
  }
  state.rows = rowsFromPaths(state.paths);
  buildTable();
  setStatus('已添加 ' + state.paths.length + ' 项，点击「开始计算」');
  toast('新增 ' + added + ' 项');
}

function clearAll() {
  if (state.running) return;
  state.paths = [];
  state.rows = [];
  buildTable();
  ui.bar.style.width = '0%';
  ui.json.textContent = '（计算完成后会自动复制到剪贴板，粘贴到网页端秒传助手即可）';
  ui.copy.disabled = true;
  ui.save.disabled = true;
  setStatus('等待添加文件');
}

/* ---------- 开始计算 ---------- */

function clampThreads() {
  let n = parseInt(ui.threads.value, 10);
  if (!Number.isFinite(n)) n = 2;
  return Math.max(1, Math.min(8, n));
}

async function start() {
  if (state.running) return;
  if (!state.paths.length) {
    toast('请先添加文件');
    return;
  }

  state.running = true;
  ui.start.disabled = true;
  ui.copy.disabled = true;
  ui.save.disabled = true;
  ui.bar.style.width = '0%';
  setStatus('正在展开列表…');
  ui.json.textContent = '计算中…';

  try {
    const names = await window.go.main.App.Start(
      state.paths.slice(),
      clampThreads(),
      ui.auth.value.trim()
    );
    state.rows = (names || []).map((n) => ({
      name: n,
      status: '计算中',
      kind: 'running',
      pct: 0,
      result: null,
    }));
    buildTable();
    if (!state.rows.length) {
      throw new Error('没有可处理的文件');
    }
    setStatus('共 ' + state.rows.length + ' 个文件，计算中…');
  } catch (err) {
    state.running = false;
    ui.start.disabled = false;
    setStatus('无法开始：' + errText(err));
    toast(errText(err));
  }
}

/* ---------- 事件 ---------- */

function bindRuntimeEvents() {
  const rt = window.runtime;
  if (!rt) {
    setTimeout(bindRuntimeEvents, 50);
    return;
  }

  rt.EventsOn('progress', (d) => {
    if (!d) return;
    const percent = d.percent || [];
    state.rows.forEach((row, i) => {
      if (row.kind !== 'running') return;
      const p = typeof percent[i] === 'number' ? percent[i] : 0;
      if (p >= 0 && p !== row.pct) {
        row.pct = p;
        updateRow(row);
      }
    });
    const total = d.count || state.rows.length || 1;
    ui.bar.style.width = Math.min(100, (d.finished || 0) * 100 / total) + '%';
    setStatus(
      '已完成 ' + (d.finished || 0) + '/' + total +
      ' · 平均 ' + (d.speed || 0).toFixed(1) + ' MB/s' +
      ' · 已用 ' + fmtDur(d.elapsed)
    );
  });

  rt.EventsOn('file-done', (d) => {
    if (!d) return;
    const row = state.rows[d.index];
    if (!row) return;
    row.result = d.result;
    row.status = '完成';
    row.kind = 'ok';
    updateRow(row);
  });

  rt.EventsOn('file-fail', (d) => {
    if (!d) return;
    const row = state.rows[d.index];
    if (!row) return;
    row.status = '失败：' + (d.message || '');
    row.kind = 'fail';
    updateRow(row);
  });

  rt.EventsOn('done', (d) => {
    if (!d) return;
    state.running = false;
    ui.start.disabled = false;
    ui.bar.style.width = '100%';
    setStatus(
      '完成：成功 ' + (d.ok || 0) + ' 个' +
      (d.failed ? '，失败 ' + d.failed + ' 个' : '') +
      ' · 用时 ' + fmtDur(d.elapsed)
    );
    if (d.json) {
      ui.json.textContent = d.json;
      ui.copy.disabled = false;
      ui.save.disabled = false;
      toast('已复制到剪贴板');
    } else {
      ui.json.textContent = '（没有成功的结果）';
    }
  });
}

/* ---------- 交互绑定 ---------- */

function bindUI() {
  $('btn-files').addEventListener('click', async () => {
    try {
      addPaths(await window.go.main.App.PickFiles());
    } catch (e) {
      toast(errText(e));
    }
  });

  $('btn-folder').addEventListener('click', async () => {
    try {
      const files = await window.go.main.App.PickFolder();
      if (!files || !files.length) {
        toast('文件夹里没有文件');
        return;
      }
      addPaths(files);
    } catch (e) {
      toast(errText(e));
    }
  });

  $('btn-clip').addEventListener('click', async () => {
    try {
      const text = await window.go.main.App.Clipboard();
      if (!text) {
        toast('剪贴板是空的');
        return;
      }
      addPaths(text.split(/\r?\n/));
    } catch (e) {
      toast(errText(e));
    }
  });

  $('btn-clear').addEventListener('click', clearAll);
  ui.start.addEventListener('click', start);

  ui.copy.addEventListener('click', async () => {
    try {
      await window.go.main.App.CopyJSON();
      toast('已复制到剪贴板');
    } catch (e) {
      toast(errText(e));
    }
  });

  ui.save.addEventListener('click', async () => {
    try {
      const path = await window.go.main.App.SaveJSON();
      if (path) toast('已保存到 ' + path);
    } catch (e) {
      toast(errText(e));
    }
  });

  // 拖放：Wails 注入的 wails:file-drop 事件
  window.addEventListener('wails:file-drop', (e) => {
    const detail = (e && e.detail) || {};
    addPaths(detail.paths || detail.files || []);
  });

  // 拖放视觉反馈
  let depth = 0;
  window.addEventListener('dragenter', (e) => {
    e.preventDefault();
    depth++;
    document.body.classList.add('dragging');
  });
  window.addEventListener('dragover', (e) => e.preventDefault());
  window.addEventListener('dragleave', () => {
    depth = Math.max(0, depth - 1);
    if (depth === 0) document.body.classList.remove('dragging');
  });
  window.addEventListener('drop', (e) => {
    e.preventDefault();
    depth = 0;
    document.body.classList.remove('dragging');
  });

  window.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') start();
  });
}

bindUI();
bindRuntimeEvents();
buildTable();
