import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {chromium} from 'playwright-core';
import {selectValue} from '../tests/system/channels_controls.mjs';

const [base, evidence] = process.argv.slice(2);
await mkdir(evidence, {recursive: true});
const options = {headless: true};
if (process.env.STEWARD_BROWSER_EXECUTABLE) options.executablePath = process.env.STEWARD_BROWSER_EXECUTABLE;
const browser = await chromium.launch(options);
try {
  const page = await browser.newPage();
  const first=await page.goto(base + '/settings/meeting-preparation');
  assert.equal(first.status(),200,await page.locator('body').innerText());
  await selectValue(page,page.locator('select[name=calendar]'),'cal');
  await selectValue(page,page.locator('select[name=mail]'),'mail');
  await page.getByLabel('Meeting time zone').fill('Asia/Shanghai');
  await page.getByRole('button',{name:'Allow read-only preparation'}).click();
  const event=page.getByRole('link',{name:'Release review',exact:true});
  await event.waitFor();
  assert.equal(await event.getAttribute('href'),'https://calendar.google.com/calendar/event?eid=owned');
  assert.equal(await page.getByRole('link',{name:'Re: Release review'}).getAttribute('href'),'https://mail.google.com/mail/u/owner@example.test/#all/abc123');
  for (const width of [1400,390]) for (const theme of ['dark','light']) {
    await page.setViewportSize({width,height:900});await page.evaluate(value=>document.documentElement.classList.toggle('dark',value==='dark'),theme);
    await page.evaluate(()=>window.scrollTo(0,0));await page.screenshot({path:evidence+'/meeting-'+width+'-'+theme+'.png',fullPage:true});
  }
  await page.getByRole('button',{name:'Turn off meeting preparation'}).click();
  await page.getByRole('button',{name:'Allow read-only preparation'}).waitFor();
  assert.equal(await page.getByRole('link',{name:'Re: Release review'}).count(),0);
} finally {await browser.close()}
