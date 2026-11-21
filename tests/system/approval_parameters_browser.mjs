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
      assert.equal(await page.locator('.approval').count(),5);
      const mail = page.locator('[data-approval="mail-preview"]');
      const mailField = label => mail.locator('.approval-fields > div').filter({has: page.getByText(label, {exact:true})}).locator('dd');
      for (const [label, value] of [['已授权账户','Gmail / work'],['指定发件地址','alias@example.test'],['收件人','primary@example.test\nother@example.test'],['抄送','copy@example.test'],['密送','archive@example.test']]) {
        assert.equal(await mailField(label).innerText(), value);
      }
      assert((await mailField('邮件附件 (1)').innerText()).includes('议程.pdf'));
      const body = mailField('邮件正文');
      assert(!(await body.innerText()).includes('完整正文末尾'));
      await body.locator('summary').focus();
      await page.keyboard.press('Enter');
      assert((await body.innerText()).includes('完整正文末尾'));
      assert.equal(await mail.locator('input[name=hash]').inputValue(), 'c'.repeat(64));
      const mailGeometry = await mail.evaluate(node=>({width:node.getBoundingClientRect().width,buttons:[...node.querySelectorAll('button[name=decision]')].map(b=>b.getBoundingClientRect().height),color:getComputedStyle(node.querySelector('dd')).color}));
      assert(mailGeometry.width>0 && mailGeometry.width<=viewport.width);
      assert.equal(mailGeometry.buttons.length, 2);
      assert(mailGeometry.buttons.every(h=>h>=(viewport.width<760?44:36)));
      assert.equal(mailGeometry.color,theme==='light'?'rgb(33, 33, 33)':'rgb(237, 237, 237)');
      await body.locator('summary').click();
      // Capture the original rendered approval without the sticky Chat header covering its upper rows.
      const inbox = await context.newPage();
      await inbox.goto(url+'/tasks/attention');
      const inboxMail = inbox.locator('[data-approval="mail-preview"]');
      await inboxMail.waitFor({timeout:4000});
      await inboxMail.evaluate(node => node.scrollIntoView({block: 'center'}));
      const row = inboxMail.locator('.approval-fields > div').first();
      const [labelBox, valueBox] = await Promise.all([row.locator('dt').boundingBox(), row.locator('dd').boundingBox()]);
      assert(labelBox && valueBox && valueBox.x >= labelBox.x + labelBox.width, 'show the named account next to its label');
      assert(Math.abs(labelBox.y - valueBox.y) < valueBox.height / 2, 'align the nonempty account row on one baseline');
      await inboxMail.screenshot({path: `${evidence}/mail-${viewport.width}-${theme}.png`});
      await inbox.close();

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
  const nativeMail = page.locator('[data-approval="mail-preview"]');
  assert.equal(await nativeMail.getByText('archive@example.test', {exact:true}).count(),1);
  assert.equal(await nativeMail.locator('form input[name=hash]').inputValue(),'c'.repeat(64));
  assert.equal(await page.locator('.approval-fields dt').filter({hasText: 'Scroll amount'}).count(), 1);
  assert.equal(await page.locator('.approval-fields dt').filter({hasText: 'Timeout (ms)'}).count(), 2);
  assert.equal(await page.locator('.approval-fields dt').filter({hasText: /^Media type$/}).count(), 2);
  assert.equal(await page.locator('.approval input[name=hash]').first().inputValue(), 'a'.repeat(64));
  await fallback.close();
  await writeFile(`${evidence}/checks.json`, JSON.stringify({checks, nativeFallback: true}, null, 2));
} finally {
  await browser.close();
}
