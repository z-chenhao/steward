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
      await page.goto(input.URL + '/settings/apps/service');
      await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
      await page.reload();
      const save = page.locator('button[type=submit]').filter({hasText: 'Verify and save'});
      const back = page.locator('a.back-link');
      assert.equal(await page.locator('main h1').textContent(), input.Title);
      assert.equal(await back.evaluate(node => Boolean(node.compareDocumentPosition(document.querySelector('.service-configuration')) & Node.DOCUMENT_POSITION_FOLLOWING)), input.BackFirst);
      assert.equal(await page.locator('[name=interface_revision]').inputValue(), input.Revision);
      const styles = await save.evaluate(node => ({height: node.getBoundingClientRect().height, radius: getComputedStyle(node).borderRadius, font: getComputedStyle(node).fontFamily}));
      assert(styles.height >= (width < 760 ? 44 : 36));
      assert.equal(styles.radius, '8px');
      assert(styles.font.length > 0);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      assert.equal(await page.locator('form').filter({has: page.locator('#composio-key-ref')}).getAttribute('action'), '/apps/service/configure');
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({path: `${input.Evidence}/composition-${theme}-${width}.png`, fullPage: true});
      checks.push({theme, width, backFirst: input.BackFirst, revision: input.Revision, styles});
    }
  }
  await context.close();
  const native = await browser.newContext({javaScriptEnabled: false});
  assert.equal((await native.request.post(input.URL + '/session', {form: {token: input.Token}})).status(), 200);
  const nativePage = await native.newPage();
  await nativePage.goto(input.URL + '/settings/apps/service');
  assert.equal(await nativePage.locator('[name=interface_revision]').inputValue(), input.Revision);
  assert.equal(await nativePage.locator('main h1').textContent(), input.Title);
  assert.equal(await nativePage.locator('a.back-link').evaluate(node => Boolean(node.compareDocumentPosition(document.querySelector('.service-configuration')) & Node.DOCUMENT_POSITION_FOLLOWING)), input.BackFirst);
  assert.equal(await nativePage.locator('button[type=submit]').filter({hasText: 'Verify and save'}).isEnabled(), true);
  await native.close();
  await writeFile(`${input.Evidence}/checks.json`, JSON.stringify({checks, nativeFormReachable: true}, null, 2) + '\n');
} finally {
  await browser.close();
}
