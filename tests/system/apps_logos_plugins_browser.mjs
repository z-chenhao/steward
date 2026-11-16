import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
let raw = '';
for await (const part of process.stdin) raw += part;
const input = JSON.parse(raw);
await mkdir(input.Evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
try {
  const context = await browser.newContext();
  assert.equal((await context.request.post(input.URL + '/session', {form: {token: input.Token}})).status(), 200);
  const page = await context.newPage();
  const checks = [];
  for (const theme of ['light', 'dark']) {
    for (const width of [1400, 390]) {
      await page.setViewportSize({width, height: 980});
      await page.goto(input.URL + '/settings/apps?view=all');
      await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
      await page.reload();
      await page.locator('[data-apps-ready]').waitFor();
      const images = page.locator('.app-tile img');
      assert.equal(await images.count(), 65);
      await images.evaluateAll(nodes => nodes.forEach(node => node.loading = 'eager'));
      await page.waitForFunction(() => [...document.querySelectorAll('.app-tile img')].every(node => node.complete && node.naturalWidth > 0));
      const facts = await images.evaluateAll(nodes => nodes.map(node => ({url: node.src, width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height})));
      for (const fact of facts) {
        assert.equal(new URL(fact.url).searchParams.get('revision'), input.Revision);
        assert(fact.width > 0 && fact.width <= 36 && fact.height > 0 && fact.height <= 36);
      }
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await page.evaluate(() => document.fonts.ready);
      const old = await context.request.get(input.URL + input.OriginalURL);
      assert.equal(old.status(), 200);
      assert.equal(old.headers().etag, input.OriginalETag);
      const current = await context.request.get(input.URL + '/apps/logo/gmail?revision=' + encodeURIComponent(input.Revision));
      assert.equal(current.status(), 200);
      assert.equal(current.headers().etag, input.CurrentETag);
      await page.screenshot({path: `${input.Evidence}/logos-${theme}-${width}.png`, fullPage: width > 760});
      checks.push({theme, width, count: facts.length, revision: input.Revision, originalETag: input.OriginalETag, currentETag: input.CurrentETag});
    }
  }
  await writeFile(`${input.Evidence}/checks.json`, JSON.stringify({checks}, null, 2) + '\n');
  await context.close();
} finally {
  await browser.close();
}
