import assert from 'node:assert/strict';
import {readFileSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {DatabaseSync} from 'node:sqlite';
import {chromium} from './node_modules/playwright-core/index.mjs';

const marker = process.argv[2];
assert.match(marker, /^[a-z-]+$/);
const url = 'http://127.0.0.1:' + process.env.PORT;
const profile = process.env.STEWARD_BROWSER_PROFILE;
const state = process.env.STEWARD_STATE_DIR;
const launch = () => chromium.launchPersistentContext(profile, {headless: true, viewport: {width: 1100, height: 800}});
let context = await launch();
const facts = {marker, port: Number(process.env.PORT), initialRows: [], persistedRows: []};
try {
  let page = context.pages()[0] || await context.newPage();
  await page.goto(url);
  facts.initialRows = await (await page.request.get(url + '/api/notes')).json();
  assert.deepEqual(facts.initialRows, [], 'fresh environment inherited another candidate database');
  assert.equal(await page.evaluate(() => localStorage.getItem('selected-note')), null, 'inherited another candidate browser storage');
  assert.equal(await page.evaluate(() => document.cookie), '', 'inherited another candidate cookie');
  await page.getByLabel('记录', {exact: true}).fill(marker);
  await page.getByRole('button', {name: '保存', exact: true}).click();
  await page.getByRole('listitem').filter({hasText: marker}).waitFor();
  assert.equal(await page.evaluate(() => localStorage.getItem('selected-note')), marker);
  assert.match(await page.evaluate(() => document.cookie), new RegExp('selected-note=' + marker));
  const before = Number(readFileSync(join(state, 'service.pid'), 'utf8'));
  process.kill(before, 'SIGTERM');
  // This is the actual recovery action. No database update, extra selection or
  // fixture reload repairs the state before the original interaction is checked.
  const deadline = Date.now() + 5000;
  let recovered;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url + '/healthz');
      const value = await response.json();
      if (response.ok && value.pid !== before) {recovered = value.pid; break;}
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  assert.ok(recovered, 'original foreground service did not restart');
  facts.recovery = {originalPID: before, recoveredPID: recovered};
  assert.equal(await page.getByLabel('记录', {exact: true}).inputValue(), marker);
  assert.equal(await page.getByRole('listitem').textContent(), marker);
  const database = new DatabaseSync(join(state, 'notes.sqlite'), {readOnly: true});
  try {
    database.exec('BEGIN');
    facts.persistedRows = database.prepare('SELECT body FROM notes ORDER BY id').all().map(row => row.body);
    database.exec('COMMIT');
  } finally {database.close();}
  assert.deepEqual(facts.persistedRows, [marker], 'post-recovery SQLite state changed or crossed candidates');
  assert.deepEqual((await (await page.request.get(url + '/api/notes')).json()).map(row => row.body), [marker]);
  await context.close();
  context = await launch();
  page = context.pages()[0] || await context.newPage();
  await page.goto(url);
  await page.getByRole('listitem').waitFor();
  assert.equal(await page.getByRole('listitem').textContent(), marker);
  assert.equal(await page.getByLabel('记录', {exact: true}).inputValue(), marker);
  assert.equal(await page.evaluate(() => localStorage.getItem('selected-note')), marker);
  assert.match(await page.evaluate(() => document.cookie), new RegExp('selected-note=' + marker));
  facts.browserProfileRetained = true;
  await page.screenshot({path: '.evidence/flow.png', fullPage: true});
  writeFileSync('.evidence/assertions.json', JSON.stringify(facts, null, 2) + '\n');
  console.log(JSON.stringify(facts));
} finally {await context.close();}
