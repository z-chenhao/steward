import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

test('foundation.css reduced-motion covers pseudo-elements and AA status colors', () => {
  const css = fs.readFileSync(new URL('./src/foundation.css', import.meta.url), 'utf8');
  assert.match(css, /@media\(prefers-reduced-motion:reduce\)\{\*,\*::before,\*::after\{/);
  assert.match(css, /--status-ready:#226943/);
  assert.match(css, /--status-attention:#805411/);
});

test('controls.css keeps accessible switch off-track, dark ogui focus token, and 44px mobile compact controls', () => {
  const css = fs.readFileSync(new URL('./src/components/ui/controls.css', import.meta.url), 'utf8');
  assert.match(css, /--switch-off,var\(--control-border\)/);
  assert.match(css, /:root\.ogui-theme\[data-widget-theme=dark\][^\n]*--color-text-info:#f09575/);
  assert.match(css, /\.control-density-compact \.control-help-trigger\{width:44px;min-width:44px;height:44px;min-height:44px\}/);
  assert.match(css, /\.control-identifier-button\[data-copy-label\]::after\{content:attr\(data-copy-label\)/);
});

test('sheet.ts focuses non-modal side sheet on open and allows Escape dismissal', () => {
  const src = fs.readFileSync(new URL('./src/components/ui/sheet.ts', import.meta.url), 'utf8');
  assert.match(src, /dialog\.tabIndex=-1/);
  assert.match(src, /dialog\.focus\(\{preventScroll:true\}\)/);
  assert.doesNotMatch(src, /dialog\.contains\(document\.activeElement\)/);
});
