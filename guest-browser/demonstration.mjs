import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdir, readFile } from 'node:fs/promises';
import { chromium } from 'playwright-core';

// Point this at an operator-owned disposable Steward instance with its default managed packages.
const base = process.argv[2],
  out = process.argv[3];
await mkdir(out, { recursive: true });
const browser = await chromium.launch({
  headless: true,
  ...(process.env.STEWARD_BROWSER_EXECUTABLE
    ? { executablePath: process.env.STEWARD_BROWSER_EXECUTABLE }
    : {}),
});
let hits = 0;
const sample = createServer((req, res) => {
  hits++;
  res.setHeader('Content-Type', 'text/html');
  res.end(
    '<html lang="en"><label>Topic<input id="topic"></label><label>Passphrase<input id="secret" type="password"></label><button>Preview</button></html>'
  );
});
await new Promise((resolve) => sample.listen(0, '127.0.0.1', resolve));
try {
  const ctx = await browser.newContext({ acceptDownloads: true });
  if (process.env.STEWARD_WEB_TEST_TOKEN) {
    const auth = await ctx.request.post(base + '/session', {
      form: { token: process.env.STEWARD_WEB_TEST_TOKEN },
    });
    assert.ok(auth.ok());
  }
  const page = await ctx.newPage();
  page.setDefaultTimeout(15000);
  await page.goto(base + '/developer/demonstration');
  const bookmark = page.locator('[data-demo-bookmark]');
  await bookmark.waitFor();
  const href = await bookmark.getAttribute('href');
  assert.ok(href.startsWith('javascript:'));
  const target = await browser.newPage({ acceptDownloads: true });
  await target.goto('http://127.0.0.1:' + sample.address().port);
  async function capture() {
    await target.evaluate((url) => {
      location.href = url;
    }, href);
    await target.getByRole('button', { name: 'Start recording' }).click();
    await target.locator('#secret').fill('private-field-value');
    await target.locator('#topic').fill('private-topic-value');
    await target.getByRole('button', { name: 'Preview', exact: true }).click();
    const done = target.waitForEvent('download');
    await target.getByRole('button', { name: 'Stop and save' }).click();
    const bytes = await readFile(await (await done).path());
    const file = JSON.parse(bytes.toString());
    assert.deepEqual(file.steps, [
      { action: 'enter', label: 'Topic' },
      { action: 'activate', label: 'Preview' },
    ]);
    assert.ok(!bytes.includes('private-'));
    assert.ok(!bytes.includes(base));
    return bytes;
  }
  const a = await capture();
  await target.locator('#topic').fill('');
  const b = await capture();
  assert.notEqual(JSON.parse(a).id, JSON.parse(b).id);
  const files = (name, buffer) => ({
    name,
    mimeType: 'application/json',
    buffer,
  });
  await page
    .locator('[data-demo-capture]')
    .setInputFiles(files('trace.json', a));
  await page.locator('[name=name]').fill('Preview report');
  await page
    .locator('[data-demo-sample]')
    .setInputFiles(files('sample.json', a));
  await page.getByRole('button', { name: 'Review steps' }).click();
  await page.getByText('Use a separately recorded sample.').waitFor();
  await page
    .locator('[data-demo-sample]')
    .setInputFiles(files('sample.json', b));
  const count = hits;
  await page.locator('textarea').fill('activate Preview');
  await page.getByRole('button', { name: 'Review steps' }).click();
  await page.getByText(/Sample did not reach/).waitFor();
  await page.locator('textarea').fill('enter Topic\nactivate Preview');
  await page.getByRole('button', { name: 'Review steps' }).click();
  await page.getByText('Verified against the local sample.').waitFor();
  const done = page.waitForEvent('download');
  await page
    .getByRole('button', { name: 'Download private skill draft' })
    .click();
  const download = await done;
  assert.equal(download.suggestedFilename(), 'SKILL.md');
  const md = await readFile(await download.path(), 'utf8');
  assert.match(md, /1\. Enter the requested value in "Topic"/);
  assert.match(md, /2\. Activate "Preview"/);
  assert.equal(hits, count, 'verification must not revisit demo site');
  for (const width of [1400, 390])
    for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(
        (theme) =>
          document.documentElement.classList.toggle('dark', theme === 'dark'),
        theme
      );
      await page
        .locator('.workspace-page')
        .screenshot({ path: out + '/demo-' + width + '-' + theme + '.png' });
    }
  console.log(
    'Captured two explicit local sessions, checked redaction and distinct evidence, rejected edited mismatch, verified through Host and downloaded reviewable skill with zero target requests.'
  );
} finally {
  await browser.close();
  sample.close();
}
