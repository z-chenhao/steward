import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
const [url, broker, evidence, rawOptions] = process.argv.slice(2);
const options = JSON.parse(rawOptions);
await mkdir(evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
try {
  const context = await browser.newContext();
  assert.equal((await context.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
  assert.equal((await context.request.post(broker + '/test/configure-project')).status(), 204);
  const page = await context.newPage();
  const checks = [];
  for (const language of ['en', 'zh-CN']) {
    await context.addCookies([{name: 'steward-language', value: language, url}]);
    for (const theme of ['light', 'dark']) {
      for (const width of [1400, 390]) {
        await page.setViewportSize({width, height: 980});
        await page.goto(url + '/settings/apps');
        await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
        await page.reload();
        await page.locator('[data-apps-ready]').waitFor();
        const tile = page.locator('[data-app-tile=gmail]');
        assert.equal(await tile.count(), 1);
        assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-revision'), options.interfaceRevision);
        const geometry = await tile.evaluate(node => {
          const copy = node.querySelector('.app-copy');
          const icon = node.querySelector('.app-logo');
          const control = node.querySelector('button.control-icon-button');
          return {copyBeforeIcon: Boolean(copy.compareDocumentPosition(icon) & Node.DOCUMENT_POSITION_FOLLOWING), copyX: copy.getBoundingClientRect().x, iconX: icon.getBoundingClientRect().x, name: copy.querySelector('strong').textContent, font: getComputedStyle(copy).fontFamily, controlHeight: control.getBoundingClientRect().height};
        });
        assert.equal(geometry.copyBeforeIcon, true);
        assert(geometry.copyX < geometry.iconX);
        assert.equal(geometry.name, 'Gmail');
        assert(geometry.font.length > 0);
        assert(geometry.controlHeight >= (width < 760 ? 44 : 32));
        assert.equal(await tile.locator('form[data-app-quick]').getAttribute('action'), '/apps/configure');
        assert.equal(await tile.locator('[name=action]').inputValue(), 'connect');
        assert.equal(await tile.locator('[name=toolkit]').inputValue(), 'gmail');
        assert.equal(await tile.locator('[name=interface_revision]').inputValue(), options.interfaceRevision);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
        await page.evaluate(() => document.fonts.ready);
        await page.screenshot({path: `${evidence}/card-${language}-${theme}-${width}.png`, fullPage: true});
        await tile.locator('.app-summary').click();
        await page.locator('[data-app-dialog][open] [data-app-detail=gmail]').waitFor();
        checks.push({language, theme, width, geometry, originalParameters: true, detailReachable: true});
      }
    }
  }
  await context.close();
  const native = await browser.newContext({javaScriptEnabled: false});
  assert.equal((await native.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
  const nativePage = await native.newPage();
  await nativePage.goto(url + '/settings/apps');
  const nativeTile = nativePage.locator('[data-app-tile=gmail]');
  assert.equal(await nativeTile.count(), 1);
  assert.equal(await nativeTile.locator('[name=interface_revision]').inputValue(), options.interfaceRevision);
  assert.equal(await nativeTile.locator('[name=action]').inputValue(), 'connect');
  assert.equal(await nativeTile.locator('[name=toolkit]').inputValue(), 'gmail');
  await nativeTile.locator('.app-summary').click();
  assert.equal(await nativePage.locator('[data-app-detail=gmail]').count(), 1);
  await native.close();
  await writeFile(`${evidence}/checks.json`, JSON.stringify({checks, actualResourceVariant: true, nativeFormAndDetail: true, interfaceRevision: options.interfaceRevision, backendScope: 'pre-account native compatibility;full account/browser lifecycle covered separately'}, null, 2) + '\n');
} finally {
  await browser.close();
}
