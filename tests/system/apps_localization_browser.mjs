import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
const [url, broker, evidence, rawOptions] = process.argv.slice(2);
const options = JSON.parse(rawOptions);
await mkdir(evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
try {
  const context = await browser.newContext({viewport: {width: 1400, height: 980}});
  await context.route('https://app.composio.dev/link/**', route => route.fulfill({contentType: 'text/html', body: '<h1>Owned OAuth test peer</h1>'}));
  assert.equal((await context.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
  assert.equal((await context.request.post(broker + '/test/configure-project')).status(), 204);
  await context.addCookies([{name: 'steward-language', value: 'zh-CN', url}]);
  const page = await context.newPage();
  await page.goto(url + '/settings/apps');
  await page.locator('[data-apps-ready]').waitFor();
  assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-localized'), 'true');
  assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-revision'), options.interfaceRevision);
  await page.locator('[data-app-open=gmail]').first().click();
  const detail = page.locator('[data-app-dialog][open]');
  await detail.waitFor();
  const popupEvent = page.waitForEvent('popup');
  await detail.locator('[data-app-first] button').click();
  const popup = await popupEvent;
  await popup.waitForURL('https://app.composio.dev/link/**');
  await page.waitForFunction(() => document.querySelector('[data-app-feedback]')?.textContent === '请在授权标签页完成连接。');
  await popup.close();
  await page.screenshot({path: evidence + '/owned-pending-zh-1400.png', fullPage: true});
  assert.equal((await context.request.post(broker + '/test/activate?id=ca_1')).status(), 204);
  await page.waitForFunction(() => document.querySelector('[data-app-dialog] [data-app-account][data-app-state=active]'));
  await page.waitForFunction(() => document.querySelector('[data-app-feedback]')?.textContent === 'Workspace');
  assert.equal(await detail.locator('[data-app-detail-feedback]').textContent(), 'Workspace');
  await page.screenshot({path: evidence + '/owned-connected-zh-1400.png', fullPage: true});
  const checks = [];
  for (const language of ['en', 'zh-CN']) {
    await context.addCookies([{name: 'steward-language', value: language, url}]);
    const title = language === 'zh-CN' ? 'Workspace' : 'Connection workspace';
    for (const theme of ['light', 'dark']) {
      for (const width of [1400, 390]) {
        await page.setViewportSize({width, height: 980});
        await page.goto(url + '/settings/apps/service');
        await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
        await page.reload();
        assert.equal(await page.locator('html').getAttribute('lang'), language);
        assert.equal(await page.locator('main h1').textContent(), title);
        assert.equal(await page.title(), 'Steward · ' + title);
        assert.equal(await page.locator('[name=interface_revision]').inputValue(), options.interfaceRevision);
        const save = page.locator('form[action="/apps/service/configure"] button[type=submit]');
        const styles = await save.evaluate(node => ({height: node.getBoundingClientRect().height, radius: getComputedStyle(node).borderRadius, font: getComputedStyle(node).fontFamily}));
        assert(styles.height >= (width < 760 ? 44 : 36));
        assert.equal(styles.radius, '8px');
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
        await page.evaluate(() => document.fonts.ready);
        await page.screenshot({path: `${evidence}/localized-${language}-${theme}-${width}.png`, fullPage: true});
        checks.push({language, theme, width, title, styles});
      }
    }
  }
  const native = await browser.newContext({javaScriptEnabled: false});
  assert.equal((await native.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
  await native.addCookies([{name: 'steward-language', value: 'zh-CN', url}]);
  const nativePage = await native.newPage();
  await nativePage.goto(url + '/settings/apps/service');
  assert.equal(await nativePage.locator('main h1').textContent(), 'Workspace');
  assert.equal(await nativePage.title(), 'Steward · Workspace');
  assert.equal(await nativePage.locator('form[action="/apps/service/configure"] button[type=submit]').isEnabled(), true);
  assert.equal((await context.request.get(broker + '/test/count')).status(), 200);
  assert.equal((await (await context.request.get(broker + '/test/count')).json()).created, 1);
  await writeFile(evidence + '/checks.json', JSON.stringify({checks, actualV2OwnedFeedback: true, newChineseText: true, dictionaryCollisionPreserved: 'Workspace', nativeForm: true, created: 1, interfaceRevision: options.interfaceRevision, backendScope: 'pre-account opt-in/native account boundary'}, null, 2) + '\n');
  await native.close();
  await context.close();
} finally {
  await browser.close();
}
