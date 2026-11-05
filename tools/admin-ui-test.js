// Ручная проверка интерфейса админки в браузере (Playwright, Chromium). Не входит в CI.
// Запуск: ADMIN_URL=http://127.0.0.1:18770/LunexGame/admin/ ADMIN_PASSWORD=... node tools/admin-ui-test.js <каталог для скриншотов>
// Сервер должен быть запущен с LUNEXGAME_ADMIN_PASSWORD_HASH и ПУСТОЙ БД (сценарий публикует версии 1–3).
const { chromium } = require('playwright');
const OUT = process.argv[2] || '.';
const BASE = process.env.ADMIN_URL || 'http://127.0.0.1:18770/LunexGame/admin/';
const PW = process.env.ADMIN_PASSWORD || 'correct horse battery staple';
(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
  const page = await ctx.newPage();
  const problems = [];
  page.on('console', m => { if (['error', 'warning'].includes(m.type())) problems.push('console ' + m.type() + ': ' + m.text()); });
  page.on('pageerror', e => problems.push('pageerror: ' + e.message));
  await page.addInitScript(() => document.addEventListener('securitypolicyviolation', e => console.error('CSP: ' + e.violatedDirective + ' ' + e.blockedURI)));
  const step = (s) => console.log('• ' + s);

  await page.goto(BASE);
  await page.waitForSelector('#pw');
  await page.screenshot({ path: OUT + '/adm_1_login.png' });
  step('экран входа открыт');

  await page.fill('#pw', 'неверный пароль 12345'); await page.click('button[type=submit]');
  await page.waitForSelector('.msg.err'); step('неверный пароль: ' + (await page.textContent('.msg.err')));
  await page.fill('#pw', PW); await page.click('button[type=submit]');
  await page.waitForSelector('nav.tabs'); step('вход выполнен, вкладки: ' + (await page.$$eval('nav.tabs button', bs => bs.map(x => x.textContent).join(' | '))));

  // --- энергия: форма, проверка, предпросмотр, публикация v1 ---
  await page.waitForSelector('#f-max');
  await page.screenshot({ path: OUT + '/adm_2_energy.png', fullPage: true });
  step('форма «Энергия» значения по умолчанию: ' + (await page.inputValue('#f-max')) + '/' + (await page.inputValue('#f-perDealt')));
  await page.fill('#f-max', '0'); await page.click('text=Проверить');
  await page.waitForSelector('.msg.err'); step('проверка некорректных данных: ' + (await page.textContent('.msg.err')).slice(0, 90));
  await page.fill('#f-max', '100'); await page.click('text=Проверить'); await page.waitForSelector('.msg.ok');
  step('проверка корректных данных: ' + (await page.textContent('.msg.ok')));
  await page.click('text=Предпросмотр'); await page.waitForSelector('text=Демо-бой с черновиком');
  step('предпросмотр: ' + (await page.$$eval('table tr', rs => rs.slice(0, 3).map(r => r.textContent.replace(/\s+/g, ' ')).join(' / '))));
  await page.screenshot({ path: OUT + '/adm_3_preview.png', fullPage: true });
  await page.click('text=Сохранить черновик'); await page.waitForSelector('.msg.ok'); step('черновик сохранён');
  await page.click('text=Опубликовать…'); await page.waitForSelector('dialog[open]');
  await page.screenshot({ path: OUT + '/adm_4_dialog.png' });
  await page.fill('dialog input', 'первая публикация'); await page.click('dialog button.primary');
  await page.waitForSelector('.chip.ok'); step('после публикации: ' + (await page.textContent('.chip.ok')));

  // --- v2 и возврат к v1 ---
  await page.fill('#f-max', '150'); await page.click('text=Опубликовать…'); await page.waitForSelector('dialog[open]');
  await page.fill('dialog input', 'увеличили запас'); await page.click('dialog button.primary');
  await page.waitForFunction(() => document.querySelector('.chip.ok')?.textContent.includes('версия 2')); step('опубликована версия 2');
  await page.click('.hist li:has-text("v1") >> text=Вернуть'); await page.waitForSelector('dialog[open]');
  await page.screenshot({ path: OUT + '/adm_5_rollback.png' });
  await page.fill('dialog input', 'вернули'); await page.click('dialog button.danger');
  await page.waitForFunction(() => document.querySelector('.chip.ok')?.textContent.includes('версия 3')); step('возврат к v1 создал версию 3');
  step('значение в форме после возврата: max=' + (await page.inputValue('#f-max')));
  await page.click('.hist li:has-text("v2") >> text=Посмотреть'); await page.waitForSelector('pre.view');
  step('просмотр версии 2: ' + (await page.textContent('pre.view')).replace(/\s+/g, ' ').slice(0, 60));
  await page.screenshot({ path: OUT + '/adm_6_history.png', fullPage: true });

  // --- герои: JSON-редактор ---
  await page.click('nav.tabs button:has-text("Герои")'); await page.waitForSelector('textarea');
  await page.click('text=Проверить'); await page.waitForSelector('.msg.ok'); step('герои: пример проходит проверку');
  await page.fill('textarea', '{"schema":1,"heroes":[{"id":"BAD ID"}]}'); await page.click('text=Проверить');
  await page.waitForSelector('.msg.err li'); step('герои: ошибок найдено ' + (await page.$$eval('.msg.err li', l => l.length)));
  await page.fill('textarea', '{ сломанный json'); await page.click('text=Проверить');
  step('герои: битый JSON -> ' + (await page.textContent('.msg.err')).slice(0, 60));
  await page.click('text=Подставить пример'); await page.click('text=Предпросмотр'); await page.waitForSelector('text=Состав');
  await page.screenshot({ path: OUT + '/adm_7_heroes.png', fullPage: true });

  // --- журнал и выход ---
  await page.click('nav.tabs button:has-text("Журнал")'); await page.waitForSelector('table tbody tr');
  step('журнал: строк ' + (await page.$$eval('table tbody tr', r => r.length)) + ', действия: ' + (await page.$$eval('table tbody tr td:nth-child(2)', c => [...new Set(c.map(x => x.textContent))].join(','))));
  await page.screenshot({ path: OUT + '/adm_8_audit.png' });
  await page.click('text=Выйти'); await page.waitForSelector('#pw'); step('выход выполнен');
  await page.goto(BASE + 'api/kinds'); step('после выхода API отвечает: ' + (await page.textContent('body')).slice(0, 40));

  // --- десктоп ---
  const d = await b.newContext({ viewport: { width: 1280, height: 800 } }); const p2 = await d.newPage();
  await p2.goto(BASE); await p2.fill('#pw', PW); await p2.click('button[type=submit]'); await p2.waitForSelector('#f-max');
  await p2.screenshot({ path: OUT + '/adm_9_desktop.png' });

  console.log(problems.length ? 'ПРОБЛЕМЫ:\n' + problems.join('\n') : 'ошибок консоли и нарушений CSP нет');
  await b.close();
})().catch(e => { console.error('СБОЙ СЦЕНАРИЯ:', e.message.split('\n')[0]); process.exit(1); });
