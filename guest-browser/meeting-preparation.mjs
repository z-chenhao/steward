import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {chromium} from 'playwright-core';
import {selectValue} from '../tests/system/channels_controls.mjs';

const [base, evidence, phase] = process.argv.slice(2);
assert.ok(['calendar', 'mail'].includes(phase));
await mkdir(evidence, {recursive: true});
const options = {headless: true};
if (process.env.STEWARD_BROWSER_EXECUTABLE) options.executablePath = process.env.STEWARD_BROWSER_EXECUTABLE;
const browser = await chromium.launch(options);
try {
  const page = await browser.newPage({reducedMotion: 'reduce'});
  const response = await page.goto(base + '/settings/meeting-preparation');
  assert.equal(response.status(), 200, await page.locator('body').innerText());
  const calendar = page.locator('select[name=calendar]');
  const mail = page.locator('select[name=mail]');
  const zone = page.getByLabel('Meeting time zone');
  const allow = page.getByRole('button', {name: 'Allow read-only preparation'});
  const event = page.getByRole('link', {name: 'Release review', exact: true});
  const thread = page.getByRole('link', {name: 'Re: Release review', exact: true});
  const eventURL = 'https://calendar.google.com/calendar/event?eid=owned';
  const partial = page.getByText('Prepared from your calendar only.', {exact: true});

  if (phase === 'calendar') {
    const connect = page.getByRole('link', {name: 'Connect Gmail to include related email'});
    assert.equal(await connect.getAttribute('href'), '/settings/apps?app=gmail');
    await selectValue(page, calendar, 'cal');
    await zone.fill('Asia/Shanghai');
  } else {
    await partial.waitFor();
    assert.equal(await event.getAttribute('href'), eventURL);
    assert.equal(await calendar.inputValue(), 'cal');
    assert.equal(await zone.inputValue(), 'Asia/Shanghai');
    assert.equal(await mail.inputValue(), '');
    assert.equal(await thread.count(), 0);
    await selectValue(page, mail, 'mail');
  }
  await allow.focus();
  await page.keyboard.press('Enter');
  await event.waitFor();
  assert.equal(await event.getAttribute('href'), eventURL);
  if (phase === 'calendar') {
    await partial.waitFor();
    assert.equal(await thread.count(), 0);
  } else {
    await thread.waitFor();
    assert.equal(await thread.getAttribute('href'), 'https://mail.google.com/mail/u/owner@example.test/#all/abc123');
    assert.equal(await partial.count(), 0);
  }

  for (const width of [1400, 390]) {
    for (const theme of ['dark', 'light']) {
      await page.setViewportSize({width, height: 900});
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), theme === 'dark');
      await page.evaluate(() => document.fonts.ready);
      const bounds = await event.boundingBox();
      assert.ok(bounds && bounds.width > 0 && bounds.x >= 0 && bounds.x + bounds.width <= width, 'Meeting link is outside the viewport');
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'Horizontal overflow');
      const zoneBounds = await zone.boundingBox();
      const labelBounds = await page.locator('label[for=meeting-zone]').boundingBox();
      assert.ok(Math.min(zoneBounds.y + zoneBounds.height, labelBounds.y + labelBounds.height) > Math.max(zoneBounds.y, labelBounds.y), 'Time zone field must share its label row');
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({path: evidence + '/meeting-' + phase + '-' + width + '-' + theme + '.png', fullPage: true});
    }
  }
  if (phase === 'mail') {
    await page.getByRole('button', {name: 'Turn off preparation'}).click();
    await allow.waitFor();
    assert.equal(await event.count(), 0);
    assert.equal(await thread.count(), 0);
  }
} finally {
  await browser.close();
}
