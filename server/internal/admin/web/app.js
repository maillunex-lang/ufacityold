'use strict';
// Админка LunexGame. Без внешних библиотек и без inline-кода (строгая CSP). Весь текст вставляется через textContent.

const app = document.getElementById('app');
let csrf = '';
let kinds = [];
let tab = null; // имя вида контента, 'audit' или null
const state = {}; // кэш по видам

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return el;
}

async function api(method, path, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  if (method !== 'GET') opt.headers['X-CSRF-Token'] = csrf;
  let r;
  try { r = await fetch('api' + path, opt); } catch (e) { return { status: 0, data: { error: 'network' } }; }
  let data = null;
  try { data = await r.json(); } catch (e) { /* пусто */ }
  if (r.status === 401 && path !== '/login') { showLogin(); throw new Error('unauthorized'); }
  return { status: r.status, data: data || {} };
}

function fmtDate(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return isNaN(d) ? iso : d.toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' });
}

// ---------------- вход ----------------

function showLogin(message) {
  csrf = '';
  const input = h('input', { type: 'password', id: 'pw', autocomplete: 'current-password', required: true, 'aria-label': 'Пароль' });
  const err = h('div', { class: 'msgs', role: 'alert' }, message ? h('div', { class: 'msg err' }, message) : null);
  const btn = h('button', { class: 'primary', type: 'submit' }, 'Войти');
  const form = h('form', { class: 'card' },
    h('h1', null, 'Самрая — админка'),
    h('p', { class: 'muted' }, 'Доступ только для владельца.'),
    h('label', { for: 'pw' }, 'Пароль'), input, btn, err);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    btn.disabled = true;
    const r = await api('POST', '/login', { password: input.value });
    input.value = '';
    if (r.status === 200) { csrf = r.data.csrf; await start(); return; }
    btn.disabled = false;
    err.replaceChildren(h('div', { class: 'msg err' },
      r.status === 429 ? 'Слишком много попыток. Подождите минуту.' :
      r.status === 401 ? 'Неверный пароль.' : 'Ошибка входа (' + r.status + ').'));
  });
  app.replaceChildren(h('div', { class: 'login' }, form));
  input.focus();
}

// ---------------- каркас ----------------

async function start() {
  const me = await api('GET', '/me');
  if (me.status !== 200) return showLogin();
  csrf = me.data.csrf;
  const k = await api('GET', '/kinds');
  kinds = k.data.kinds || [];
  tab = kinds.length ? kinds[0].name : 'audit';
  renderShell();
}

function renderShell() {
  const tabs = h('nav', { class: 'tabs', role: 'tablist' },
    kinds.map((k) => h('button', { role: 'tab', class: tab === k.name ? 'active' : '', onclick: () => { tab = k.name; renderShell(); } }, k.title)),
    h('button', { role: 'tab', class: tab === 'audit' ? 'active' : '', onclick: () => { tab = 'audit'; renderShell(); } }, 'Журнал'));
  const out = h('button', { class: 'link', onclick: async () => { await api('POST', '/logout', {}).catch(() => {}); showLogin('Вы вышли.'); } }, 'Выйти');
  const main = h('main', null);
  app.replaceChildren(h('header', { class: 'bar' }, h('h1', null, 'Самрая · админка'), out), tabs, main);
  if (tab === 'audit') renderAudit(main); else renderKind(main, tab);
}

// ---------------- журнал ----------------

async function renderAudit(main) {
  const r = await api('GET', '/audit');
  const rows = r.data.rows || [];
  main.replaceChildren(h('section', { class: 'card' }, h('h2', null, 'Журнал действий'),
    h('div', { class: 'tablewrap' }, h('table', null,
      h('thead', null, h('tr', null, ['Время', 'Действие', 'Вид', 'Версия', 'Адрес', 'Заметка'].map((t) => h('th', null, t)))),
      h('tbody', null, rows.map((x) => h('tr', null, h('td', null, fmtDate(x.at)), h('td', null, x.action), h('td', null, x.kind),
        h('td', null, x.version || ''), h('td', null, x.ip), h('td', null, x.detail))))))));
}

// ---------------- контент ----------------

// Поля простых форм (остальные виды редактируются как JSON).
const forms = {
  energy: [
    { key: 'max', label: 'Запас энергии для главной способности', hint: 'Сколько энергии нужно накопить (больше нуля).', step: '1', min: '1' },
    { key: 'perDealt', label: 'За нанесённый обычной атакой урон', hint: 'Энергия за единицу урона.', step: '0.001', min: '0' },
    { key: 'perTaken', label: 'За полученный урон', hint: 'Энергия за единицу полученного урона.', step: '0.001', min: '0' },
    { key: 'perHealed', label: 'За фактически восстановленное здоровье', hint: 'Избыточное лечение энергию не даёт.', step: '0.001', min: '0' },
  ],
};

async function renderKind(main, name) {
  main.replaceChildren(h('p', { class: 'muted' }, 'Загрузка…'));
  const r = await api('GET', '/content/' + name);
  if (r.status !== 200) { main.replaceChildren(h('div', { class: 'msg err' }, 'Не удалось загрузить (' + r.status + ').')); return; }
  const c = r.data;
  const kind = c.kind;
  const published = c.published;
  const draft = c.draft;
  const pubVersion = published ? published.version : 0;

  // начальные данные редактора: черновик → опубликованное → пример
  let initial = draft ? draft.data : published ? published.data : kind.example;

  const msgs = h('div', { class: 'msgs', 'aria-live': 'polite' });
  const previewBox = h('div', null);
  const viewBox = h('div', null);
  const say = (cls, title, list) => msgs.replaceChildren(h('div', { class: 'msg ' + cls }, title, list && list.length ? h('ul', null, list.map((t) => h('li', null, t))) : null));

  // --- редактор ---
  let getData; let setData;
  const editorHost = h('div', null);
  const fields = forms[name];
  if (fields) {
    const inputs = {};
    const grid = h('div', { class: 'formgrid' });
    for (const f of fields) {
      const inp = h('input', { type: 'number', inputmode: 'decimal', step: f.step, min: f.min, id: 'f-' + f.key });
      inputs[f.key] = inp;
      grid.append(h('div', null, h('label', { for: 'f-' + f.key }, f.label), inp, h('div', { class: 'hint' }, f.hint)));
    }
    setData = (obj) => { for (const f of fields) inputs[f.key].value = obj && obj[f.key] != null ? obj[f.key] : ''; };
    getData = () => {
      const o = {};
      for (const f of fields) { const v = inputs[f.key].value; o[f.key] = v === '' ? null : Number(v); }
      return { ok: true, data: o };
    };
    editorHost.append(grid);
  } else {
    const ta = h('textarea', { spellcheck: 'false', 'aria-label': 'JSON данных' });
    setData = (obj) => { ta.value = JSON.stringify(obj, null, 2); };
    getData = () => { try { return { ok: true, data: JSON.parse(ta.value) }; } catch (e) { return { ok: false, error: 'Некорректный JSON: ' + e.message }; } };
    editorHost.append(ta,
      h('div', { class: 'actions' },
        h('button', { type: 'button', onclick: () => { const g = getData(); if (g.ok) setData(g.data); else say('err', g.error); } }, 'Форматировать'),
        h('button', { type: 'button', onclick: () => setData(kind.example) }, 'Подставить пример')));
  }
  setData(initial);

  const need = () => { const g = getData(); if (!g.ok) { say('err', g.error); return null; } return g.data; };
  const showErrors = (title, errs) => say('err', title, errs);

  // --- действия ---
  async function check() {
    const data = need(); if (data == null) return;
    const r2 = await api('POST', '/content/' + name + '/validate', { data });
    if (r2.data.valid) say('ok', 'Проверка пройдена: ошибок нет.'); else showErrors('Найдены ошибки:', r2.data.errors);
  }
  async function preview() {
    const data = need(); if (data == null) return;
    const r2 = await api('POST', '/content/' + name + '/preview', { data });
    previewBox.replaceChildren();
    if (r2.status === 422) { showErrors('Предпросмотр невозможен — исправьте ошибки:', r2.data.details || []); return; }
    if (r2.status !== 200) { say('err', 'Ошибка предпросмотра (' + r2.status + ').'); return; }
    msgs.replaceChildren();
    const secs = (r2.data.preview && r2.data.preview.sections) || [];
    previewBox.append(h('section', { class: 'card' }, h('h2', null, 'Предпросмотр'),
      secs.map((s) => h('div', null, h('h3', null, s.title),
        h('div', { class: 'tablewrap' }, h('table', null, h('tbody', null, (s.rows || []).map((row) => h('tr', null, h('th', null, row[0]), h('td', null, row[1])))))))),
      !secs.length ? h('p', { class: 'muted' }, JSON.stringify(r2.data.preview)) : null));
  }
  async function saveDraft() {
    const data = need(); if (data == null) return;
    const r2 = await api('PUT', '/content/' + name + '/draft', { data });
    if (r2.status !== 200) { say('err', 'Не удалось сохранить (' + r2.status + ').'); return; }
    if ((r2.data.errors || []).length) say('err', 'Черновик сохранён, но в нём есть ошибки (опубликовать нельзя):', r2.data.errors);
    else say('ok', 'Черновик сохранён.');
  }
  async function resetDraft() {
    await api('DELETE', '/content/' + name + '/draft');
    renderShell();
  }
  function confirmDialog(title, text, okLabel, onOk, danger) {
    const note = h('input', { type: 'text', maxlength: '300', placeholder: 'Заметка (необязательно)', 'aria-label': 'Заметка' });
    const dlg = h('dialog', null, h('h2', null, title), h('p', null, text), note,
      h('div', { class: 'actions' },
        h('button', { type: 'button', onclick: () => dlg.close() }, 'Отмена'),
        h('button', { type: 'button', class: danger ? 'danger' : 'primary', onclick: async () => { dlg.close(); await onOk(note.value); } }, okLabel)));
    dlg.addEventListener('close', () => dlg.remove());
    document.body.append(dlg);
    dlg.showModal();
  }
  function publish() {
    const data = need(); if (data == null) return;
    confirmDialog('Опубликовать новую версию?', 'Будет создана версия ' + (pubVersion + 1) + '. Действует на новые бои; прогресс игроков не меняется.', 'Опубликовать', async (note) => {
      const r2 = await api('POST', '/content/' + name + '/publish', { data, note, expectedVersion: pubVersion });
      if (r2.status === 200) { renderShell(); return; }
      if (r2.status === 409) say('err', 'Опубликованная версия изменилась после загрузки страницы. Обновите страницу и проверьте изменения.');
      else if (r2.status === 422) showErrors('Публикация отклонена:', r2.data.details || []);
      else say('err', 'Ошибка публикации (' + r2.status + ').');
    });
  }
  function rollback(v) {
    confirmDialog('Вернуть версию ' + v + '?', 'Будет создана НОВАЯ версия ' + (pubVersion + 1) + ' с данными версии ' + v + '. История сохранится; прогресс игроков не меняется.', 'Вернуть', async (note) => {
      const r2 = await api('POST', '/content/' + name + '/rollback', { toVersion: v, note, expectedVersion: pubVersion });
      if (r2.status === 200) { renderShell(); return; }
      if (r2.status === 409) say('err', 'Опубликованная версия изменилась. Обновите страницу.');
      else if (r2.status === 422) showErrors('Эти данные больше не проходят проверку:', r2.data.details || []);
      else say('err', 'Ошибка возврата (' + r2.status + ').');
    }, true);
  }
  async function viewVersion(v) {
    const r2 = await api('GET', '/content/' + name + '/versions/' + v);
    if (r2.status !== 200) { say('err', 'Версия не найдена.'); return; }
    viewBox.replaceChildren(h('section', { class: 'card' }, h('h3', null, 'Версия ' + v + ' · ' + fmtDate(r2.data.createdAt)),
      h('pre', { class: 'view' }, JSON.stringify(r2.data.data, null, 2)),
      h('div', { class: 'actions' }, h('button', { type: 'button', onclick: () => { setData(r2.data.data); say('ok', 'Данные версии ' + v + ' подставлены в редактор (это ещё не черновик).'); window.scrollTo(0, 0); } }, 'Взять в редактор'))));
  }

  // --- вёрстка ---
  const stale = draft && draft.baseVersion !== pubVersion;
  main.replaceChildren(
    h('section', { class: 'card' },
      h('h2', null, kind.title), h('p', { class: 'muted' }, kind.description),
      h('div', { class: 'chips' },
        published ? h('span', { class: 'chip ok' }, 'Опубликовано: версия ' + published.version + ' · ' + fmtDate(published.createdAt)) : h('span', { class: 'chip warn' }, 'Ещё ничего не опубликовано'),
        draft ? h('span', { class: 'chip gold' }, 'Есть черновик · ' + fmtDate(draft.updatedAt)) : null,
        stale ? h('span', { class: 'chip warn' }, 'Опубликованная версия изменилась после создания черновика') : null),
      draft && (c.draftErrors || []).length ? h('div', { class: 'msg err' }, 'В черновике есть ошибки:', h('ul', null, c.draftErrors.map((t) => h('li', null, t)))) : null,
      editorHost,
      h('div', { class: 'actions' },
        h('button', { type: 'button', onclick: check }, 'Проверить'),
        h('button', { type: 'button', onclick: preview }, 'Предпросмотр'),
        h('button', { type: 'button', onclick: saveDraft }, 'Сохранить черновик'),
        h('button', { type: 'button', class: 'primary', onclick: publish }, 'Опубликовать…'),
        draft ? h('button', { type: 'button', class: 'danger', onclick: resetDraft }, 'Сбросить черновик') : null),
      msgs),
    previewBox,
    h('section', { class: 'card' }, h('h2', null, 'История версий'),
      c.history && c.history.length ? h('ul', { class: 'hist' }, c.history.map((v) => h('li', null, h('div', { class: 'row' },
        h('strong', null, 'v' + v.version),
        h('span', { class: 'chip ' + (v.source === 'rollback' ? 'warn' : '') }, v.source === 'rollback' ? 'возврат из v' + v.rolledBackFrom : 'публикация'),
        h('span', { class: 'muted grow' }, fmtDate(v.createdAt) + (v.note ? ' · ' + v.note : '')),
        h('button', { class: 'link', type: 'button', onclick: () => viewVersion(v.version) }, 'Посмотреть'),
        v.version !== pubVersion ? h('button', { class: 'link', type: 'button', onclick: () => rollback(v.version) }, 'Вернуть') : h('span', { class: 'chip ok' }, 'текущая'))))) :
        h('p', { class: 'muted' }, 'Пока пусто.'),
      viewBox));
}

start().catch(() => { /* вход показан */ });
