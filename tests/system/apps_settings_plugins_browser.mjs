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
  const posts = [];
  page.on('request', req => {if(req.method() === 'POST' && new URL(req.url()).pathname === '/apps/service/configure') posts.push(Object.fromEntries(new URLSearchParams(req.postData())));});
  for (const theme of ['light', 'dark']) {
    for (const width of [1400, 390]) {
      await page.setViewportSize({width, height: 980});
      await page.goto(input.URL + '/settings/apps/service');
      await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
      await page.reload();
      await page.locator('.service-advanced').evaluate(node => {node.open = true;});
      assert.deepEqual(await page.locator('[name=app_slug]').evaluateAll(nodes => nodes.map(node => node.value)), input.Order);
      assert.equal(await page.locator('#composio-key-ref').inputValue(), 'env:COMPOSIO_API_KEY');
      const save = page.locator('button[type=submit]').filter({hasText: input.SaveLabel || 'Verify and save'});
      const styles = await save.evaluate(node => ({height: node.getBoundingClientRect().height, radius: getComputedStyle(node).borderRadius, font: getComputedStyle(node).fontFamily}));
      assert(styles.height >= (width < 760 ? 44 : 36));
      assert(styles.font.length > 0);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({path: `${input.Evidence}/settings-${theme}-${width}.png`, fullPage: width > 760});
      await page.locator('button[name=action][value=add-override]').click();
      const expected = input.BlankFirst ? ['', ...input.Order] : [...input.Order, ''];
      assert.deepEqual(await page.locator('[name=app_slug]').evaluateAll(nodes => nodes.map(node => node.value)), expected);
      assert((await page.locator('body').textContent()).includes('Unsaved changes.'));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      if (input.RemoveDraft) {
        await page.locator('button[name=action][value="remove-override-' + (input.BlankFirst ? 0 : input.Order.length) + '"]').click();
        assert.deepEqual(await page.locator('[name=app_slug]').evaluateAll(nodes => nodes.map(node => node.value)), input.Order);
      }
      checks.push({theme, width, order: input.Order, draft: expected, styles});
    }
  }
  if (input.Save) {
    await page.goto(input.URL + '/settings/apps/service');
    const response = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/apps/service/configure');
    await page.locator('button[type=submit]').filter({hasText: input.SaveLabel}).click();
    assert.equal((await response).status(), 200);
    await page.getByText('App connection service saved.', {exact: true}).waitFor();
    assert.equal(posts.filter(p => !p.action).length, 1, 'explicit save repeated');
  }
  if (input.InterfaceRevision) for (const post of posts) assert.equal(post.interface_revision, input.InterfaceRevision);
  const native = await browser.newContext({javaScriptEnabled: false, storageState: await context.storageState()});
  const nativePage = await native.newPage();
  await nativePage.goto(input.URL + '/settings/apps/service');
  assert.equal(await nativePage.locator('button[type=submit]').filter({hasText: input.SaveLabel || 'Verify and save'}).count(), 1);
  await native.close();
  await writeFile(`${input.Evidence}/checks.json`, JSON.stringify({checks, actualSave: !!input.Save, originalRevision: input.InterfaceRevision, actualRemoveDraft: !!input.RemoveDraft, nativeControls: true}, null, 2) + '\n');
  await context.close();
} finally {
  await browser.close();
}
