import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';

const [url, peer, evidence, rawOptions] = process.argv.slice(2);
const options = JSON.parse(rawOptions);
await mkdir(evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
let page;
try {
  const context = await browser.newContext({viewport: {width: 1400, height: 980}});
  await context.route('https://app.composio.dev/link/**', route => route.fulfill({contentType: 'text/html', body: '<h1>Owned authorization peer</h1>'}));
  assert.equal((await context.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
  assert.equal((await context.request.post(peer + '/test/configure-project')).status(), 204);
  const count = async () => (await context.request.get(peer + '/test/count').then(r => r.json())).created;
  page = await context.newPage();
  if (options.accountUnavailable) {
    assert.equal((await context.request.get(url + '/settings/apps')).status(), 503);
    const response = await context.request.post(url + '/apps/configure', {form: {action: 'connect', toolkit: 'gmail', interface_revision: options.interfaceRevision}, headers: {Accept: 'application/json'}});
    assert.equal(response.status(), 200);
    const receipt = await response.json();
    assert.equal(receipt.presentation_unavailable, 'true');
    assert.equal(receipt.authorization_url, 'https://app.composio.dev/link/ca_1');
    assert(receipt.connection_id);
    assert.equal(receipt.accounts_html, undefined);
    const native = await context.request.post(url + '/apps/configure', {form: {action: 'continue', connection: receipt.connection_id, interface_revision: options.interfaceRevision}});
    assert.equal(native.status(), 200);
    const html = await native.text();
    assert(html.includes('The account action was accepted.'));
    assert(html.includes(receipt.authorization_url));
    assert(!html.includes('data-apps '));
    assert.equal(await count(), 1, 'presentation failure replayed accepted creation');
    await writeFile(evidence + '/checks.json', JSON.stringify({invalidAccountControlsRejected: true, acceptedLinkPreserved: true, noCreationReplay: true, scope: 'actual authenticated App HTTP and original source peer; unavailable markup never bootstraps a page'}, null, 2));
  } else {
    const posts = [];
    page.on('request', req => { if (req.method() === 'POST' && new URL(req.url()).pathname === '/apps/configure') posts.push(Object.fromEntries(new URLSearchParams(req.postData()))); });
    await page.goto(url + '/settings/apps');
    await page.locator('[data-apps-ready]').waitFor();
    if(options.clientView){
      assert.equal(await page.locator('[data-apps]').evaluate(root=>JSON.parse(root.dataset.appInterfaceProfile).browser_view),true);
      assert.equal(await page.locator('iframe[title="Apps isolated presentation"]').count(),1);
      assert(await page.locator('link[rel=stylesheet]').evaluateAll(links=>links.some(link=>link.href.includes('interface_revision=')&&link.href.includes('format=css'))));
    }

    await page.locator('[data-app-tile=gmail] .app-summary').click();
    const detail = page.locator('[data-app-dialog][open]');
    await detail.waitFor();
    const popupEvent = page.waitForEvent('popup');
    await detail.locator('[data-app-first] button').click();
    const popup = await popupEvent;
    await popup.waitForURL('https://app.composio.dev/link/ca_1');
    await popup.close();
    const row = detail.locator('[data-app-detail-accounts] [data-app-account]');
    await row.waitFor();
    const identity = await row.getAttribute('data-app-account');
    assert(identity);
    const inspect = async target => {
      const name = await target.locator('.setting-copy strong').innerText();
      if(options.clientView){
        // Accepted check results replace the account row asynchronously. Read
        // the connected rendered row, rather than a just-detached DOM handle.
        await page.waitForFunction(({identity,padding})=>[...document.querySelectorAll('[data-app-account]')].some(row=>{
          const box=row.getBoundingClientRect();
          return row.dataset.appAccount===identity&&row.closest('[data-app-dialog][open],.app-detail-page')&&row.isConnected&&box.width>0&&box.height>0&&getComputedStyle(row).paddingTop===padding;
        }),{identity,padding:options.styleVariant?'24px':'16px'},{timeout:5000});
      }
      const parts = name.split(' · ');
      assert.equal(parts.length, 2);
      assert.equal(parts[options.accountVariant ? 1 : 0], 'Gmail');
      assert(parts[options.accountVariant ? 0 : 1].length > 0);
      assert.equal(await target.getAttribute('data-app-account'), identity);
      const forms = await target.locator('form[data-app-action]').evaluateAll(nodes => nodes.map(n => ({action: n.getAttribute('action'), method: n.method, connection: n.elements.connection?.value, revision: n.elements.interface_revision?.value})));
      assert(forms.length > 0);
      for (const form of forms) assert.deepEqual(form, {action: '/apps/configure', method: 'post', connection: identity, revision: options.interfaceRevision});
      if (options.accountCopy) {
        const disconnect = target.locator('.app-account-actions button[value=disconnect]');
        assert.equal(await disconnect.innerText(), options.accountCopyVariant ? 'Close account' : 'Disconnect');
        const form = target.locator('.app-account-actions');
        assert.equal(await form.getAttribute('data-settings-confirm'), 'Disconnect this account?');
        assert.equal(await form.getAttribute('data-confirm-description'), 'Ghost will stop using this account. Your other accounts stay connected.');
      }
      return name;
    };
    await inspect(row);
    assert.equal(await row.getAttribute('data-app-state'), 'pending');
    assert.equal(await row.locator('[data-app-auth-link]').getAttribute('href'), 'https://app.composio.dev/link/ca_1');
    assert.equal((await context.request.post(peer + '/test/activate?id=ca_1')).status(), 204);
    await row.locator('.app-account-actions button[value=check]').click();
    await page.waitForFunction(id => document.querySelector('[data-app-dialog][open] [data-app-account="' + id + '"]')?.dataset.appState === 'active', identity);
    await inspect(row);
    for (const body of posts) {
      assert.equal(body.interface_revision, options.interfaceRevision);
      if (body.connection) assert.equal(body.connection, identity);
      assert(!Object.hasOwn(body, 'arguments'));
    }
    const checks = [];
    for (const width of [1400, 390]) for (const theme of ['light', 'dark']) {
      await page.setViewportSize({width, height: 980});
      await page.goto(url + '/settings/apps?app=gmail');
      await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
      await page.reload();
      await page.locator('[data-apps-ready]').waitFor();
      const visible = page.locator('.app-detail-page [data-app-account]');
      await visible.waitFor();
      const name = await inspect(visible);
      const geometry = await visible.locator('.app-account-actions button').evaluateAll(nodes => nodes.map(n => ({height: n.getBoundingClientRect().height, width: n.getBoundingClientRect().width, font: getComputedStyle(n).fontFamily, size: getComputedStyle(n).fontSize})));
      assert(geometry.length > 0);
      assert(geometry.every(g => g.height >= (width < 760 ? 44 : 36) && g.width > 0 && g.font.includes('Inter')));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 2), false);
      assert(!(await page.locator('body').innerText()).includes('never-expose'));
      await visible.screenshot({path: evidence + '/account-' + width + '-' + theme + '.png'});
      checks.push({width, theme, name, geometry});
    }
    // Reuse the original authenticated session. Logging in again rotates the
    // server's one session and would invalidate the following original-page POST.
    const native = await browser.newContext({javaScriptEnabled: false, storageState: await context.storageState()});
    const nativePage = await native.newPage();
    await nativePage.goto(url + '/settings/apps?app=gmail');
    await inspect(nativePage.locator('.app-detail-page [data-app-account]'));
    await native.close();
    if (options.accountCopy) {
      const current = page.locator('.app-detail-page [data-app-account]');
      const button = current.locator('button[value=disconnect]');
      const postCount = posts.length;
      await button.click();
      const confirmation = page.locator('dialog.settings-confirm');
      await confirmation.waitFor();
      assert.equal(await confirmation.getByRole('button', {name: 'Cancel', exact: true}).evaluate(n => n === document.activeElement), true);
      await page.keyboard.press('Escape');
      await confirmation.waitFor({state: 'hidden'});
      assert.equal(posts.length, postCount, 'cancelling owning copy confirmation submitted an effect');
      await button.click();
      await confirmation.waitFor();
      const accepted = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/apps/configure');
      await confirmation.getByRole('button', {name: 'Disconnect this account?', exact: true}).click();
      assert.equal((await accepted).status(), 200);
      await current.waitFor({state: 'detached'});
      assert.deepEqual(posts.at(-1), {interface_revision: options.interfaceRevision, connection: identity, action: 'disconnect'});
    }
    if (options.connectView) {
      const additional = page.locator('.app-detail-page [data-app-add-account]');
      await additional.locator('summary').click();
      assert.equal(await additional.locator('label').innerText(), options.connectViewVariant ? 'Account name' : 'Account label');
      const alias = additional.locator('input[name=alias]');
      await alias.fill('');
      const before = posts.length;
      await additional.locator('button[type=submit]').click();
      assert.equal(await alias.evaluate(n => n.validity.valueMissing), true);
      assert.equal(posts.length, before, 'required alias submitted an effect');
      await alias.fill('work2');
      const popupEvent = page.waitForEvent('popup');
      await additional.locator('button[type=submit]').click();
      const popup = await popupEvent;
      await popup.waitForURL('https://app.composio.dev/link/ca_2');
      await popup.close();
      await page.waitForFunction(() => document.querySelectorAll('.app-detail-page [data-app-account]').length === 2);
      assert.deepEqual(posts.at(-1), {interface_revision: options.interfaceRevision, action: 'connect', toolkit: 'gmail', alias: 'work2'});
    }
    assert.equal(await count(), options.connectView ? 2 : 1, 'read or rendering replayed account creation');
    await writeFile(evidence + '/checks.json', JSON.stringify({checks, originalIdentityAndRevision: true, actualCreateAndCheck: true, actualAdditionalAndAliasValidation: !!options.connectView, noScriptOriginalControls: true, noReadReplay: true, variant: options.accountVariant, owningAccountCopy: !!options.accountCopy, actualOriginalDisconnectAndCancel: !!options.accountCopy, scope: 'actual managed account view with App/SQLite/source peer; existing pre-opt-in backend path, not full default32 proof'}, null, 2));
  }
  await context.close();
} catch (error) {
  if (page) { await page.screenshot({path: evidence + '/failure.png'}); await writeFile(evidence + '/failure.txt', await page.locator('body').innerText()); }
  throw error;
} finally {
  await browser.close();
}
