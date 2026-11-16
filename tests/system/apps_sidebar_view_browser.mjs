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
  const page = await context.newPage();
  await page.goto(url + '/settings/apps');
  await page.locator('[data-apps-ready]').waitFor();
  await page.locator('[data-app-tile=gmail] .app-summary').click();
  const detail = page.locator('[data-app-dialog][open]');
  await detail.waitFor();
  const popupEvent = page.waitForEvent('popup');
  await detail.locator('[data-app-first] button').click();
  const popup = await popupEvent;
  await popup.waitForURL('https://app.composio.dev/link/ca_1');
  if (options.sidebarUnavailable) {
    await page.waitForFunction(() => document.querySelector('[data-app-feedback]')?.textContent.includes('The account action was accepted.'));
    assert.equal(await page.locator('[data-app-feedback] a').getAttribute('href'), 'https://app.composio.dev/link/ca_1');
    assert.equal(await page.locator('#apps-sidebar a').count(), 0);
    assert.equal((await context.request.get(broker + '/test/count').then(response => response.json())).created, 1);
    await page.screenshot({path: evidence + '/accepted-link-en-1400.png', fullPage: true});
    // Obtain the actual durable local identity from the accepted JSON receipt.
    const receiptResponse = await context.request.post(url + '/apps/configure', {form: {action: 'connect', toolkit: 'gmail'}, headers: {Accept: 'application/json'}});
    assert.equal(receiptResponse.status(), 200);
    const receipt = await receiptResponse.json();
    assert.equal(receipt.presentation_unavailable, 'true');
    assert.equal(receipt.authorization_url, 'https://app.composio.dev/link/ca_1');
    assert(receipt.connection_id);
    const nativeReceipt = await context.request.post(url + '/apps/configure', {form: {action: 'continue', connection: receipt.connection_id}});
    assert.equal(nativeReceipt.status(), 200);
    const nativeHTML = await nativeReceipt.text();
    assert(nativeHTML.includes('The account action was accepted.'));
    assert(nativeHTML.includes('https://app.composio.dev/link/ca_1'));
    assert(!nativeHTML.includes('data-apps '));
    assert.equal((await context.request.get(broker + '/test/count').then(response => response.json())).created, 1);
    await popup.close();
    await writeFile(evidence + '/checks.json', JSON.stringify({acceptedLinkPreserved: true, nativeAndJSONReceipts: true, created: 1, sidebarUnavailable: true, interfaceRevision: options.interfaceRevision}, null, 2) + '\n');
  } else {
    await page.locator('#apps-sidebar .app-pending-dot').waitFor();
    await popup.close();
    const inspect = async () => page.locator('#apps-sidebar .apps-connections > a').evaluateAll(nodes => nodes.map(node => {
      const icon = node.querySelector('.app-logo');
      const name = [...node.children].find(child => child.tagName === 'SPAN' && child.textContent === 'Gmail');
      return {name: name?.textContent, nameFirst: Boolean(name?.compareDocumentPosition(icon) & Node.DOCUMENT_POSITION_FOLLOWING), target: node.getAttribute('href'), toolkit: node.getAttribute('data-app-open'), font: getComputedStyle(node).fontFamily, bodyFont: getComputedStyle(document.body).fontFamily, size: getComputedStyle(node).fontSize, width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height, nameX: name?.getBoundingClientRect().x, iconX: icon.getBoundingClientRect().x, pending: Boolean(node.querySelector('.app-pending-dot')), pendingLabel: node.querySelector('.app-pending-dot')?.getAttribute('aria-label'), iconURL: icon.querySelector('img')?.getAttribute('src')};
    }));
    let entries = await inspect();
    assert.equal(entries.length, 1);
    assert.equal(entries[0].nameFirst, true);
    assert.equal(entries[0].pending, true);
    assert.equal(entries[0].pendingLabel, 'Awaiting authorization');
    assert.equal(entries[0].target, '/settings/apps?app=gmail');
    assert.equal(entries[0].toolkit, 'gmail');
    await page.screenshot({path: evidence + '/sidebar-pending-en-1400.png', fullPage: true});
    assert.equal((await context.request.post(broker + '/test/activate?id=ca_1')).status(), 204);
    await page.waitForFunction(() => document.querySelector('[data-app-dialog] [data-app-account][data-app-state=active]'));
    await page.waitForFunction(() => document.querySelector('#apps-sidebar .apps-connections a') && !document.querySelector('#apps-sidebar .app-pending-dot'));
    entries = await inspect();
    assert.equal(entries[0].nameFirst, true);
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
          if (width < 760) await page.locator('.workspace-topbar [data-sidebar-toggle]').click();
          const navigation = page.locator('#apps-sidebar .apps-connections > a');
          assert.equal(await navigation.count(), 1);
          await navigation.waitFor({state: 'visible'});
          const rows = await inspect();
          assert.equal(rows[0].nameFirst, true);
          assert.equal(rows[0].pending, false);
          assert(rows[0].width > 0 && rows[0].height > 0);
          assert(rows[0].nameX < rows[0].iconX);
          assert.equal(rows[0].font, rows[0].bodyFont);
          assert.equal(rows[0].size, '13px');
          assert(rows[0].iconURL?.startsWith('/apps/logo/gmail'));
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
          await page.evaluate(() => document.fonts.ready);
          await page.screenshot({path: `${evidence}/sidebar-${language}-${theme}-${width}.png`, fullPage: true});
          checks.push({language, theme, width, rows});
        }
      }
    }
    const native = await browser.newContext({javaScriptEnabled: false});
    assert.equal((await native.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
    const nativePage = await native.newPage();
    await nativePage.goto(url + '/settings/apps');
    const target = nativePage.locator('#apps-sidebar .apps-connections > a');
    assert.equal(await target.count(), 1);
    assert.equal(await target.getAttribute('href'), '/settings/apps?app=gmail');
    await target.click();
    assert.equal(await nativePage.locator('[data-app-detail=gmail]').count(), 1);
    await native.close();
    assert.equal((await context.request.get(broker + '/test/count').then(response => response.json())).created, 1);
    await writeFile(evidence + '/checks.json', JSON.stringify({checks, actualPendingAndAutomaticActive: true, nativeSidebarNavigation: true, created: 1, interfaceRevision: options.interfaceRevision}, null, 2) + '\n');
  }
  await context.close();
} finally {
  await browser.close();
}
