import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';
import {selectValue} from './channels_controls.mjs';
const [url, peer, evidence, rawOptions] = process.argv.slice(2);
const options = JSON.parse(rawOptions);
const search = 'mail & jobs';
const nextCursor = 'next opaque /<&" 中文';
const initial = url + '/settings/apps?' + new URLSearchParams({view: 'all', search, category: 'communication'});
await mkdir(evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
let page;
try {
  const context = await browser.newContext({viewport: {width: 1400, height: 980}});
  assert.equal((await context.request.post(url + '/session', {form: {token: options.token}})).status(), 200);
  assert.equal((await context.request.post(peer + '/test/configure-project')).status(), 204);
  page = await context.newPage();
  const checks = [];
  if (options.directoryUnavailable) {
    assert.equal((await context.request.get(initial)).status(), 503);
    assert.equal((await context.request.get(peer + '/test/catalog-requests')).status(), 200);
    assert.equal((await context.request.get(peer + '/test/count').then(r => r.json())).created, 0);
    await writeFile(evidence + '/checks.json', JSON.stringify({invalidContributionRejected: true, noCreation: true}, null, 2));
  } else {
    const nav = async () => {
      const paths = await page.locator('.apps-tabs a').evaluateAll(nodes => nodes.map(n => n.getAttribute('href')));
      const expected = ['/settings/apps', '/settings/apps?view=connected', '/settings/apps?view=all'];
      if (options.directoryReversed) expected.reverse();
      assert.deepEqual(paths, expected);
      assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-revision'), options.interfaceRevision);
    };
    for (const width of [1400, 390]) for (const theme of ['light', 'dark']) {
      await page.setViewportSize({width, height: 980});
      await page.goto(initial);
      await page.evaluate(value => localStorage.setItem('steward-theme', value), theme);
      await page.reload();
      await page.locator('[data-apps-ready]').waitFor();
    if(options.clientView){
      assert.equal(await page.locator('[data-apps]').evaluate(root=>JSON.parse(root.dataset.appInterfaceProfile).browser_view),true);
      assert.equal(await page.locator('iframe[title="Apps isolated presentation"]').count(),1);
      assert(await page.locator('link[rel=stylesheet]').evaluateAll(links=>links.some(link=>link.href.includes('interface_revision=')&&link.href.includes('format=css'))));
    }

      await page.locator('#app-category-picker').waitFor();
      await nav();
      assert.equal(await page.locator('#app-category').inputValue(), 'communication');
      assert.equal(await page.locator('[data-app-category-form] input[name=search]').inputValue(), search);
      assert.equal(await page.locator('.apps-tabs a[aria-current=page]').innerText(), 'All apps');
      const controls = await page.locator('#app-category-picker, .apps-tabs a').evaluateAll(nodes => nodes.map(n => ({height: n.getBoundingClientRect().height, width: n.getBoundingClientRect().width, font: getComputedStyle(n).fontFamily})));
      assert.equal(controls.length, 4);
      assert(controls.every(n => n.width > 0 && n.height >= (width < 760 ? 44 : 36) && n.font.includes('Inter')));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 2), false);
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({path: evidence + '/directory-' + width + '-' + theme + '.png', fullPage: true});
      await selectValue(page, page.locator('#app-category'), '');
      await page.waitForURL(u => u.searchParams.get('category') === '');
      assert.equal(new URL(page.url()).searchParams.get('search'), search);
      await page.locator('[data-apps-ready]').waitFor();
      await selectValue(page, page.locator('#app-category'), 'communication');
      await page.waitForURL(u => u.searchParams.get('category') === 'communication');
      await page.locator('[data-apps-ready]').waitFor();
      await page.getByRole('link', {name: 'More apps', exact: true}).click();
      await page.waitForURL(u => u.searchParams.get('cursor') === nextCursor);
      const continued = new URL(page.url());
      assert.equal(continued.searchParams.get('search'), search);
      assert.equal(continued.searchParams.get('category'), 'communication');
      assert.equal(await page.getByRole('link', {name: 'More apps', exact: true}).count(), 0);
      await page.locator('.apps-tabs').getByRole('link', {name: 'Discover', exact: true}).click();
      await page.waitForURL(url + '/settings/apps');
      await page.locator('[data-apps-ready]').waitFor();
      assert.equal(await page.locator('.apps-tabs a[aria-current=page]').innerText(), 'Discover');
      await page.locator('.apps-group a[href="/settings/apps?collection=communication"]').click();
      await page.waitForURL(u => u.searchParams.get('collection') === 'communication');
      await page.getByRole('link', {name: 'Back to apps', exact: true}).click();
      await page.waitForURL(url + '/settings/apps');
      if (options.pagesView) {
        await page.goto(url + '/settings/apps?app=gmail');
        await page.locator('[data-apps-ready]').waitFor();
        const heading = page.locator('.app-detail > header');
        assert.equal(await heading.locator('h2').innerText(), 'Gmail');
        assert.equal(await heading.locator('p').innerText(), 'Read and organize your email');
        assert.deepEqual(await heading.locator('div > h2, div > p').evaluateAll(nodes => nodes.map(n => n.tagName)), options.pagesVariant ? ['P', 'H2'] : ['H2', 'P']);
        assert.equal(await page.locator('[data-apps]').getAttribute('data-app-interface-revision'), options.interfaceRevision);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 2), false);
        await page.evaluate(() => document.fonts.ready);
        await page.screenshot({path: evidence + '/page-' + width + '-' + theme + '.png', fullPage: true});
        await page.goto(url + '/settings/apps/service');
        await page.locator('input[name=key_ref]').waitFor();
        assert.equal(await page.locator('.settings-page h1').count(), 1);
        const fields = await page.locator('input[name=key_ref]').evaluateAll(nodes => nodes.map(n => ({width: n.getBoundingClientRect().width, height: n.getBoundingClientRect().height, font: getComputedStyle(n).fontFamily})));
        assert.equal(fields.length, 1);
        assert(fields.every(n => n.width > 0 && n.height >= (width < 760 ? 44 : 36) && n.font.includes('Inter')));
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 2), false);
        await page.screenshot({path: evidence + '/service-' + width + '-' + theme + '.png', fullPage: true});
        checks.push({width, theme, controls, fields, actualDetailOrder: true, originalServiceControls: true});
      } else {
        checks.push({width, theme, controls});
      }
    }
    const native = await browser.newContext({javaScriptEnabled: false, storageState: await context.storageState()});
    const nativePage = await native.newPage();
    await nativePage.goto(url + '/settings/apps?' + new URLSearchParams({view: 'all', search}));
    await nativePage.locator('#app-category').selectOption('communication');
    await nativePage.getByRole('button', {name: 'Filter', exact: true}).click();
    await nativePage.waitForURL(u => u.searchParams.get('category') === 'communication');
    assert.equal(new URL(nativePage.url()).searchParams.get('search'), search);
    await nativePage.getByRole('link', {name: 'More apps', exact: true}).click();
    await nativePage.waitForURL(u => u.searchParams.get('cursor') === nextCursor);
    if (options.pagesView) {
      await nativePage.locator('#app-search').fill('updated & jobs');
      await nativePage.locator('#app-search').press('Enter');
      await nativePage.waitForURL(u => u.searchParams.get('search') === 'updated & jobs');
      assert.equal(new URL(nativePage.url()).searchParams.get('category'), 'communication');
      await nativePage.goto(url + '/settings/apps?app=gmail');
      assert.deepEqual(await nativePage.locator('.app-detail > header div > h2, .app-detail > header div > p').evaluateAll(nodes => nodes.map(n => n.tagName)), options.pagesVariant ? ['P', 'H2'] : ['H2', 'P']);
    }
    await native.close();
    const queries = await context.request.get(peer + '/test/catalog-requests').then(r => r.json());
    assert(queries.some(q => q.cursor === nextCursor && q.search === search && q.category === 'communication'));
    assert.equal((await context.request.get(peer + '/test/count').then(r => r.json())).created, 0);
    await writeFile(evidence + '/checks.json', JSON.stringify({checks, actualCollectionBack: true, actualSharedSelectFilter: true, actualOriginalCursor: true, actualNativeFilterAndPagination: true, noCreation: true, wholePageViews: !!options.pagesView, independentPageOrder: !!options.pagesVariant, independentNavigation: !!options.directoryReversed}, null, 2));
  }
  await context.close();
} catch (error) {
  if (page) { await page.screenshot({path: evidence + '/failure.png'}); await writeFile(evidence + '/failure.txt', await page.locator('body').innerText()); }
  throw error;
} finally { await browser.close(); }
