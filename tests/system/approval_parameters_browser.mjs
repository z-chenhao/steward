import assert from 'node:assert/strict';
import {mkdir, writeFile} from 'node:fs/promises';
import {chromium} from '../../guest-browser/node_modules/playwright-core/index.mjs';

const [url, evidence] = process.argv.slice(2);
await mkdir(evidence, {recursive: true});
const browser = await chromium.launch({headless: true, ...(process.env.STEWARD_BROWSER_EXECUTABLE ? {executablePath: process.env.STEWARD_BROWSER_EXECUTABLE} : {})});
const checks = [];
try {
  for (const viewport of [{width: 1400, height: 1000}, {width: 390, height: 844}]) {
    for (const theme of ['light', 'dark']) {
      const context = await browser.newContext({viewport, colorScheme: theme});
      await context.addCookies([{name: 'steward-language', value: 'zh-CN', url}]);
      const page = await context.newPage();
      await page.goto(url);
      const card = page.locator('.approval').first();
      await card.waitFor();
      const field = card.locator('.approval-fields > div').filter({has: page.locator('dt', {hasText: '滚动距离'})});
      assert.equal(await field.count(), 1);
      assert.equal(await field.locator('dd').innerText(), '50.5');
      const budget = page.locator('.approval').nth(1).locator('.approval-fields > div').filter({has: page.locator('dt', {hasText: '超时时间（毫秒）'})});
      assert.equal(await budget.count(), 1);
      assert.equal(await budget.locator('dd').innerText(), '4000');
      const transfer = page.locator('.approval').nth(2);
      const name = transfer.locator('.approval-fields > div').filter({has: page.locator('dt', {hasText: /^名称$/})});
      assert.equal(await name.count(), 1);
      assert.equal(await name.locator('dd').innerText(), 'export.bin');
      const media = transfer.locator('.approval-fields > div').filter({has: page.locator('dt', {hasText: /^媒体类型$/})});
      assert.equal(await media.count(), 1);
      assert.equal(await media.locator('dd').innerText(), 'application/octet-stream');
      assert.equal(await transfer.locator('input[name=hash]').inputValue(), 'a'.repeat(64));
      const upload = page.locator('.approval').nth(3);
      for (const [label,value] of [['名称','approved-upload.txt'],['媒体类型','text/plain'],['超时时间（毫秒）','3000']]) {
        const field=upload.locator('.approval-fields > div').filter({has:page.locator('dt',{hasText:new RegExp(`^${label}$`)})});
        assert.equal(await field.count(),1);assert.equal(await field.locator('dd').innerText(),value);
      }
      assert.equal(await upload.locator('input[name=hash]').inputValue(),'b'.repeat(64));
      assert.equal(await page.locator('.approval').count(),4);
      const uploadSizes=await upload.evaluate(node=>({left:node.getBoundingClientRect().left,right:node.getBoundingClientRect().right,color:getComputedStyle(node.querySelector('dd')).color,buttons:[...node.querySelectorAll('button[name=decision]')].map(button=>({height:button.getBoundingClientRect().height}))}));
      assert(uploadSizes.left>=0 && uploadSizes.right<=viewport.width+1);
      assert.equal(uploadSizes.color,theme==='light'?'rgb(33, 33, 33)':'rgb(237, 237, 237)');
      assert.equal(uploadSizes.buttons.length,2);
      assert(uploadSizes.buttons.every(button=>button.height>=(viewport.width<760?44:36)));



      assert.equal(await card.locator('input[name=hash]').inputValue(), 'a'.repeat(64));
      assert.equal(await card.locator('button[name=decision]').count(), 2);
      const sizes = await card.evaluate(node => {
        const box = node.getBoundingClientRect();
        const buttons = [...node.querySelectorAll('button[name=decision]')].map(button => ({height: button.getBoundingClientRect().height, width: button.getBoundingClientRect().width}));
        return {left: box.left, right: box.right, width: window.innerWidth, overflow: document.documentElement.scrollWidth > window.innerWidth, buttons, fontSize: getComputedStyle(node.querySelector('dd')).fontSize, color: getComputedStyle(node.querySelector('dd')).color};
      });
      assert(!sizes.overflow && sizes.left >= 0 && sizes.right <= sizes.width + 1);
      assert.equal(sizes.color, theme === 'light' ? 'rgb(33, 33, 33)' : 'rgb(237, 237, 237)');
      assert(sizes.buttons.every(button => button.height >= (viewport.width < 760 ? 44 : 36)));
      await card.locator('button[name=decision]').first().focus();
      assert(await card.locator('button[name=decision]').first().evaluate(node => document.activeElement === node));
      await card.scrollIntoViewIfNeeded();
      await page.screenshot({path: `${evidence}/${viewport.width}-${theme}.png`, fullPage: true});
      checks.push({viewport, theme, ...sizes, upload:uploadSizes});
      await context.close();
    }
  }
  const fallback = await browser.newContext({viewport: {width: 390, height: 844}, javaScriptEnabled: false});
  const page = await fallback.newPage();
  await page.goto(url);
  assert.equal(await page.locator('.approval-fields dt').filter({hasText: 'Scroll amount'}).count(), 1);
  assert.equal(await page.locator('.approval-fields dt').filter({hasText: 'Timeout (ms)'}).count(), 2);
  assert.equal(await page.locator('.approval-fields dt').filter({hasText: /^Media type$/}).count(), 2);
  assert.equal(await page.locator('.approval input[name=hash]').first().inputValue(), 'a'.repeat(64));
  await fallback.close();
  await writeFile(`${evidence}/checks.json`, JSON.stringify({checks, nativeFallback: true}, null, 2));
} finally {
  await browser.close();
}
