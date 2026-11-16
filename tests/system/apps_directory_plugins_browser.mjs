import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';

let raw = '';
for await (const chunk of process.stdin) raw += chunk;
const input = JSON.parse(raw);
await mkdir(input.Evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
try {
  const context = await browser.newContext({viewport: {width: 1400, height: 980}});
  const login = await context.request.post(input.URL + '/session', {form: {token: input.Token}});
  assert.equal(login.status(), 200);
  const page = await context.newPage();
  const checks = [];
  for (const theme of ['light', 'dark']) {
    for (const width of [1400, 390]) {
      await page.setViewportSize({width, height: 980});
      await page.goto(input.URL + '/settings/apps');
      await page.evaluate(theme => localStorage.setItem('steward-theme', theme), theme);
      await page.reload();
      await page.locator('[data-apps-ready]').waitFor();
      const featured = await page.locator('.apps-group').first().locator('[data-app-tile]').evaluateAll(nodes => nodes.map(node => node.dataset.appTile));
      assert.equal(featured[0], input.First);
      const gmail = page.locator('[data-app-tile=gmail]');
      assert.equal(await gmail.locator('.app-copy strong').textContent(), input.Name);
      assert((await gmail.textContent()).includes(input.Description));
      const slugs = await page.locator('[data-app-tile]').evaluateAll(nodes => nodes.map(node => node.dataset.appTile));
      assert.equal(new Set(slugs).size, slugs.length);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      const columns = await page.locator('.apps-grid').first().evaluate(node => getComputedStyle(node).gridTemplateColumns.split(' ').length);
      assert.equal(columns, width === 1400 ? 2 : 1);
      await page.locator('.app-tile img').evaluateAll(images => images.forEach(image => image.loading = 'eager'));
      await page.waitForFunction(() => [...document.querySelectorAll('.app-tile img')].every(image => image.complete && image.naturalWidth > 0));
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({path: `${input.Evidence}/home-${theme}-${width}.png`, fullPage: width > 760});

      await page.goto(input.URL + '/settings/apps?view=all');
      await page.locator('[data-apps-ready]').waitFor();
      const all = await page.locator('[data-app-tile]').evaluateAll(nodes => nodes.map(node => node.dataset.appTile));
      const expectedAll = input.QueryCount ? [...input.All].sort().reverse() : input.All;
      assert.deepEqual(all, expectedAll);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await page.locator('.app-tile img').evaluateAll(images => images.forEach(image => image.loading = 'eager'));
      await page.waitForFunction(() => [...document.querySelectorAll('.app-tile img')].every(image => image.complete && image.naturalWidth > 0));
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({path: `${input.Evidence}/all-${theme}-${width}.png`, fullPage: width > 760});
      checks.push({theme, width, featured, columns, count: slugs.length, all});
    }
  }
  await page.goto(input.URL + '/settings/apps?collection=communication');
  await page.locator('[data-apps-ready]').waitFor();
  assert.equal(await page.locator('[data-app-tile]').count(), 10);
  const names = await page.locator('.app-copy strong').allTextContents();
  const collectionSlugs = await page.locator('[data-app-tile]').evaluateAll(nodes => nodes.map(node => node.dataset.appTile));
  assert.deepEqual(collectionSlugs, input.Collection);
  if (input.QueryCount === 0) assert.deepEqual(names, [...names].sort((a, b) => a.toLowerCase() < b.toLowerCase() ? -1 : 1));
  else assert.deepEqual(names, input.Collection);
  await page.goto(input.URL + '/settings/apps?view=all');
  await page.locator('[data-apps-ready]').waitFor();
  assert.equal(await page.locator('[data-app-tile]').count(), 65);
  await page.locator('.app-tile img').evaluateAll(images => images.forEach(image => image.loading = 'eager'));
  await page.waitForFunction(() => [...document.querySelectorAll('.app-tile img')].every(image => image.complete && image.naturalWidth > 0));
  await page.goto(input.URL + '/settings/apps?search=uniquehint');
  await page.locator('[data-apps-ready]').waitFor();
  const selectedCount = input.QueryCount;
  assert.equal(await page.locator('[data-app-tile]').count(), selectedCount);
  if (selectedCount) assert.equal(await page.locator('[data-app-tile=gmail] .app-copy strong').textContent(), input.Name);
  await page.goto(input.URL + '/settings/apps?search=+GmAiL+');
  await page.locator('[data-apps-ready]').waitFor();
  assert.equal(await page.locator('[data-app-tile]').count(), 1);
  await page.goto(input.URL + '/settings/apps?app=gmail');
  assert((await page.locator('body').textContent()).includes(input.Name));
  assert((await page.locator('body').textContent()).includes(input.Description));
  await writeFile(`${input.Evidence}/checks.json`, JSON.stringify({checks, collection: names, allCount: 65, versionedQueryCount: selectedCount}, null, 2) + '\n');
  await context.close();
} finally {
  await browser.close();
}
