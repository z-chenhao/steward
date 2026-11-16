import {createSideSheet} from './components/ui/sheet';
import {enhanceSplitView} from './components/ui/split-view';
import {connectMemoryBrowser} from './memory-browser';
import {enhanceLegacyMemoryV1} from './legacy/memory-v1';
import {memoryCopy, readMemoryPresentation} from './memory-presentation';

// The server owns memory visibility and selection. Native links remain usable
// without this enhancement; only pane widths are persisted in the browser.
export async function enhanceMemory() {
  const page = document.querySelector<HTMLElement>('#memory-page');
  if (!page) return;
  let presentation: ReturnType<typeof readMemoryPresentation>;
  try {presentation = readMemoryPresentation()} catch {page.dataset.memoryUnavailable = ''; return}
  if (!presentation) {enhanceLegacyMemoryV1();return}
  const revision = presentation.revision;
  const text = (value: string) => memoryCopy(presentation, document.documentElement.lang, value);
  const rail = document.querySelector<HTMLDetailsElement>('.obs-filter-rail');
  let browser: Awaited<ReturnType<typeof connectMemoryBrowser>>;
  try {browser = await connectMemoryBrowser(revision, {rail_present: !!rail, rail_open: rail?.open ?? false})}
  catch {page.dataset.memoryUnavailable = '';return}
  const listPage = !document.querySelector('#memory-workbench');
  let sheet: ReturnType<typeof createSideSheet> | null = null;
  let pending: AbortController | null = null;
  let disposeSplit = () => {};
  const initialize = (scope: ParentNode) => {
    const root = scope.querySelector<HTMLElement>('[data-split-view]');
    if (root) disposeSplit = enhanceSplitView(root, text(browser.defaults.labels.resize_sources));
  };
  const dismiss = () => { pending?.abort(); disposeSplit(); sheet?.dispose(); sheet = null; };
  initialize(document);
  const compact = matchMedia(`(max-width:${browser.defaults.compact_width}px)`);
  let wideOpen = browser.defaults.wide_open;
  let fitQueue = Promise.resolve();
  const fit = () => { fitQueue = fitQueue.then(async () => {
    const reply = await browser.call({type:'fit', rail_present:!!rail, rail_open:rail?.open ?? false, compact:compact.matches, wide_open:wideOpen});
    if (reply.type !== 'fit' || typeof reply.wide_open !== 'boolean' || (rail ? typeof reply.rail_open !== 'boolean' : reply.rail_open !== null)) throw Error('invalid fit');
    wideOpen = reply.wide_open;if (rail) rail.open = reply.rail_open as boolean;
  }).catch(() => {page.dataset.memoryUnavailable = '';browser.close()}) };
  compact.addEventListener('change', fit); fit();
  const depth = (): number | null => Number.isSafeInteger(history.state?.memorySheetDepth) && history.state.memorySheetDepth >= 0 && history.state.memorySheetDepth <= 4096 ? history.state.memorySheetDepth : null;
  let closing = false;
  async function historyAction(kind: 'close' | 'popstate' | 'pageshow', persisted = false) {
    if (kind === 'close' && closing) return;
    if (kind === 'close') closing = true;
    const originalDepth = depth(), href = location.href;
    try {
      const reply = await browser.call({type:'history',kind,list_page:listPage,has_ref:location.pathname === '/developer/memory' && new URL(href).searchParams.has('ref'),persisted,depth:originalDepth});
      if (href !== location.href || depth() !== originalDepth) return;
      if (reply.type !== 'history' || !['none','back','dismiss','select'].includes(String(reply.mode))) throw Error('invalid history');
      if (reply.mode === 'back') {
        if (kind !== 'close' || !originalDepth || reply.steps !== originalDepth) throw Error('invalid history depth');
        history.go(-originalDepth);
      } else if (reply.mode === 'dismiss') dismiss();
      else if (reply.mode === 'select' && location.pathname === '/developer/memory' && new URL(href).searchParams.has('ref')) await select(new URL(href),false);
    } catch {browser.close();page!.dataset.memoryUnavailable = ''}
    finally {closing = false}
  }
  async function select(url: URL, push: boolean) {
    if (url.searchParams.get('inspector_revision') !== revision) {location.assign(url); return}
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
      const links = Array.from(document.querySelectorAll<HTMLAnchorElement>('a.memory-open'));
      const index = links.findIndex(link => new URL(link.href).searchParams.get('ref') === url.searchParams.get('ref'));
      const originalDepth = depth();
      const plan = await browser.call({type:'commit',list_page:listPage,same_record:sameRecord,has_focused:!!focused,push,count:links.length,index,depth:originalDepth});
      const validIndex = (value: unknown) => value === null || (Number.isSafeInteger(value) && Number(value) >= 0 && Number(value) < links.length);
      if (plan.type !== 'commit' || !validIndex(plan.previous) || !validIndex(plan.next) || (plan.previous !== null && Number(plan.previous) >= index) || (plan.next !== null && Number(plan.next) <= index) || !['previous_hidden','next_hidden','previous_disabled','next_disabled','restore_source','restore_focus','history_push'].every(key => typeof plan[key] === 'boolean') || (plan.restore_source && !sameRecord) || (plan.restore_focus && !focused) || (plan.history_push && (!push || (listPage ? plan.history_depth !== (originalDepth ?? 0)+1 : plan.history_depth !== null)))) throw Error('invalid commit');
      if (controller.signal.aborted || pending !== controller) return;
      disposeSplit();
      if (current) current.replaceWith(next);
      else {
        sheet = createSideSheet(text(browser.defaults.labels.details), () => {
          void historyAction('close');
        }, {modal: browser.defaults.sheet_modal, storageKey: browser.defaults.storage_key});
        sheet.body.append(next); sheet.open();
      }
      initialize(next);
      if (sheet) {
        sheet.full.href = url.href; sheet.title.textContent = text(browser.defaults.labels.details);
        sheet.previous.hidden = plan.previous_hidden as boolean; sheet.next.hidden = plan.next_hidden as boolean;
        sheet.previous.disabled = plan.previous_disabled as boolean; sheet.next.disabled = plan.next_disabled as boolean;
        sheet.previous.onclick = () => { if (typeof plan.previous === 'number') void select(new URL(links[plan.previous].href), true); };
        sheet.next.onclick = () => { if (typeof plan.next === 'number') void select(new URL(links[plan.next].href), true); };
        sheet.previous.setAttribute('aria-label', text(browser.defaults.labels.previous)); sheet.previous.title = text(browser.defaults.labels.previous);
        sheet.next.setAttribute('aria-label', text(browser.defaults.labels.next)); sheet.next.title = text(browser.defaults.labels.next);
      }
      if (plan.restore_source) { const pane = next.querySelector('.memory-sources'); if (pane) pane.scrollTop = sourceTop; }
      if (plan.restore_focus && focused) Array.from(next.querySelectorAll<HTMLAnchorElement>('a')).find(anchor => anchor.href === focused)?.focus({preventScroll: true});
      if (plan.history_push) history.pushState(listPage ? {memorySheetDepth: plan.history_depth} : null, '', url);
    } catch { if (!controller.signal.aborted) location.assign(url); }
    finally { if (pending === controller) pending = null; }
  }
  let clickSequence = 0;
  const allowed = (anchor: HTMLAnchorElement | null | undefined): URL | null => {
    if (!anchor || anchor.target || anchor.download) return null;
    const url = new URL(anchor.href);
    return url.origin === location.origin && url.pathname === '/developer/memory' && url.searchParams.has('ref') ? url : null;
  };
  document.addEventListener('click', event => {
    if (!(event instanceof MouseEvent) || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const target = event.target instanceof Element ? event.target : null;
    const direct = target?.closest<HTMLAnchorElement>('a.memory-open,a.memory-select');
    const row = target?.closest('tr[data-memory-ref]');
    const rowAnchor = row?.querySelector<HTMLAnchorElement>('a.memory-open');
    const directURL = allowed(direct), rowURL = allowed(rowAnchor);
    if (!directURL && !rowURL) return;
    if (!directURL && (target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]') || window.getSelection()?.toString())) return;
    // Cancel browser default synchronously; failure keeps the exact managed link.
    event.preventDefault();const sequence = ++clickSequence;
    void browser.call({type:'click',list_page:listPage,primary:true,row:!!row,blocked:!!target?.closest('a,button,input,select,textarea,label,summary,[role=button],[contenteditable]'),selection:!!window.getSelection()?.toString(),direct_allowed:!!directURL,row_allowed:!!rowURL,anchor_kind:direct?.classList.contains('memory-select')?'select':direct?'open':'none'}).then(reply => {
      if (sequence !== clickSequence) return;
      if (reply.type !== 'click' || typeof reply.intercept !== 'boolean' || typeof reply.focus_row !== 'boolean' || !['none','direct','row'].includes(String(reply.target))) throw Error('invalid click');
      const anchor = reply.target === 'direct' ? direct : reply.target === 'row' ? rowAnchor : null;
      const url = reply.target === 'direct' ? directURL : reply.target === 'row' ? rowURL : null;
      if (reply.intercept && (!url || !anchor?.isConnected)) throw Error('invalid click target');
      if (reply.focus_row && rowAnchor?.isConnected) rowAnchor.focus({preventScroll:true});
      if (reply.intercept && url) void select(url,true);
      else if (directURL) location.assign(directURL);
    }).catch(() => {browser.close();page.dataset.memoryUnavailable = '';if (sequence === clickSequence && directURL) location.assign(directURL)});
  });
  window.addEventListener('popstate', () => {void historyAction('popstate')});
  window.addEventListener('pageshow', event => {
    if (!event.persisted) return;
    // pagehide terminates the opaque Worker even when the document enters BFCache.
    // Reconnect the same frozen graph before replaying the retained selection.
    void connectMemoryBrowser(revision,{rail_present:!!rail,rail_open:rail?.open ?? false}).then(next => {
      browser = next;compact.addEventListener('change',fit);fit();page.dataset.memoryReady = '';
      void historyAction('pageshow',true);
    }).catch(() => {page.dataset.memoryUnavailable = ''});
  });
  window.addEventListener('pagehide', () => {clickSequence++;delete page.dataset.memoryReady;dismiss();browser.close();compact.removeEventListener('change',fit)});
  page.dataset.memoryReady = '';
}
