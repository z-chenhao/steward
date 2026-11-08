import {createSideSheet} from './components/ui/sheet';
import {enhanceSplitView} from './components/ui/split-view';
import {t} from './i18n';

// The server owns memory visibility and selection. Native links remain usable
// without this enhancement; only pane widths are persisted in the browser.
export function enhanceMemory() {
  const page = document.querySelector<HTMLElement>('#memory-page');
  if (!page) return;
  const listPage = !document.querySelector('#memory-workbench');
  let sheet: ReturnType<typeof createSideSheet> | null = null;
  let pending: AbortController | null = null;
  let disposeSplit = () => {};
  const initialize = (scope: ParentNode) => {
    const root = scope.querySelector<HTMLElement>('[data-split-view]');
    if (root) disposeSplit = enhanceSplitView(root, t('Resize memory sources'));
  };
  const dismiss = () => { pending?.abort(); disposeSplit(); sheet?.dispose(); sheet = null; };
  initialize(document);
  const rail = document.querySelector<HTMLDetailsElement>('.obs-filter-rail');
  const compact = matchMedia('(max-width:760px)');
  let wideOpen = rail?.open ?? true;
  const fit = () => { if (!rail) return; if (compact.matches) { wideOpen = rail.open; rail.open = false; } else rail.open = wideOpen; };
  compact.addEventListener('change', fit); fit();
  async function select(url: URL, push: boolean) {
    pending?.abort(); const controller = new AbortController(); pending = controller;
    const current = document.querySelector<HTMLElement>('#memory-workbench');
    const focused = document.activeElement instanceof HTMLAnchorElement && current?.contains(document.activeElement) ? document.activeElement.href : null;
    const sourceTop = current?.querySelector('.memory-sources')?.scrollTop ?? 0;
    current?.classList.add('obs-loading');
    try {
      const response = await fetch(url, {credentials: 'same-origin', cache: 'no-store', signal: controller.signal, headers: {Accept: 'text/html'}});
      if (!response.ok) throw new Error('memory read unavailable');
      const parsed = new DOMParser().parseFromString(await response.text(), 'text/html');
      const next = parsed.querySelector<HTMLElement>('#memory-workbench');
      if (!next || controller.signal.aborted) throw new Error('invalid memory view');
      const sameRecord = current?.querySelector<HTMLElement>('[data-memory-id]')?.dataset.memoryId === next.querySelector<HTMLElement>('[data-memory-id]')?.dataset.memoryId;
      disposeSplit();
      if (current) current.replaceWith(next);
      else {
        sheet = createSideSheet(t('Memory details'), () => {
          const depth = history.state?.memorySheetDepth;
          if (typeof depth === 'number' && depth > 0) history.go(-depth); else dismiss();
        }, {modal: false, storageKey: 'steward.memory.peek-width.v1'});
        sheet.body.append(next); sheet.open();
      }
      initialize(next);
      if (sheet) {
        sheet.full.href = url.href; sheet.title.textContent = t('Memory details');
        const links = Array.from(document.querySelectorAll<HTMLAnchorElement>('a.memory-open'));
        const index = links.findIndex(link => new URL(link.href).searchParams.get('ref') === url.searchParams.get('ref'));
        sheet.previous.hidden = false; sheet.next.hidden = false;
        sheet.previous.disabled = index <= 0; sheet.next.disabled = index < 0 || index >= links.length - 1;
        sheet.previous.onclick = () => { if (index > 0) void select(new URL(links[index - 1].href), true); };
        sheet.next.onclick = () => { if (index >= 0 && index + 1 < links.length) void select(new URL(links[index + 1].href), true); };
        sheet.previous.setAttribute('aria-label', t('Previous memory')); sheet.previous.title = t('Previous memory');
        sheet.next.setAttribute('aria-label', t('Next memory')); sheet.next.title = t('Next memory');
      }
      if (sameRecord) { const pane = next.querySelector('.memory-sources'); if (pane) pane.scrollTop = sourceTop; }
      if (focused) Array.from(next.querySelectorAll<HTMLAnchorElement>('a')).find(anchor => anchor.href === focused)?.focus({preventScroll: true});
      if (push) history.pushState(listPage ? {memorySheetDepth: (history.state?.memorySheetDepth ?? 0) + 1} : null, '', url);
    } catch { if (!controller.signal.aborted) location.assign(url); }
    finally { if (pending === controller) pending = null; }
  }
  document.addEventListener('click', event => {
    if (!(event instanceof MouseEvent) || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const target = event.target as Element | null;
    let anchor = target?.closest<HTMLAnchorElement>(listPage ? 'a.memory-open,a.memory-select' : 'a.memory-select');
    if (listPage && !anchor && !target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]')) {
      if (window.getSelection()?.toString()) return;
      anchor = target?.closest('tr[data-memory-ref]')?.querySelector<HTMLAnchorElement>('a.memory-open');
      anchor?.focus({preventScroll:true});
    }
    if (!anchor || anchor.target || anchor.download) return;
    const url = new URL(anchor.href);
    if (url.origin !== location.origin || url.pathname !== '/developer/memory' || !url.searchParams.has('ref')) return;
    event.preventDefault(); void select(url, true);
  });
  window.addEventListener('popstate', () => { if (listPage && !history.state?.memorySheetDepth) dismiss(); else if (location.pathname === '/developer/memory' && new URL(location.href).searchParams.has('ref')) void select(new URL(location.href), false); });
  window.addEventListener('pageshow', event => { if (event.persisted && listPage && history.state?.memorySheetDepth) void select(new URL(location.href), false); });
  window.addEventListener('pagehide', () => { dismiss(); compact.removeEventListener('change', fit); });
  page.dataset.memoryReady = '';
}
