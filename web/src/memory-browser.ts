// No record bodies, URLs, identifiers or effect capabilities cross this seam.
export type MemoryBrowserRequest =
  | {type: 'init'; rail_present: boolean; rail_open: boolean}
  | {type: 'fit'; rail_present: boolean; rail_open: boolean; compact: boolean; wide_open: boolean}
  | {type: 'click'; list_page: boolean; primary: boolean; row: boolean; blocked: boolean; selection: boolean; direct_allowed: boolean; row_allowed: boolean; anchor_kind: 'none' | 'open' | 'select'}
  | {type: 'commit'; list_page: boolean; same_record: boolean; has_focused: boolean; push: boolean; count: number; index: number; depth: number | null}
  | {type: 'history'; kind: 'close' | 'popstate' | 'pageshow'; list_page: boolean; has_ref: boolean; persisted: boolean; depth: number | null};
export interface MemoryBrowserLabels {details: string; previous: string; next: string; resize_sources: string}
export interface MemoryBrowserDefaults {
  labels: MemoryBrowserLabels;
  compact_width: number;
  sheet_modal: boolean;
  storage_key: string;
  wide_open: boolean;
}
function validMemoryLabels(value: unknown): value is MemoryBrowserLabels {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const labels = value as Record<string, unknown>;
  return Object.keys(labels).length === 4 && ['details','previous','next','resize_sources'].every(key => Object.hasOwn(labels,key) && typeof labels[key] === 'string' && (labels[key] as string).trim().length > 0 && (labels[key] as string).length <= 512);
}
function validMemoryDefaults(reply: Record<string, unknown>): boolean {
  return reply.type === 'ready' && validMemoryLabels(reply.labels) && Number.isSafeInteger(reply.compact_width) && Number(reply.compact_width) >= 320 && Number(reply.compact_width) <= 1600 && typeof reply.sheet_modal === 'boolean' && typeof reply.wide_open === 'boolean' && typeof reply.storage_key === 'string' && /^steward\.memory\.[a-z0-9.-]{1,64}$/.test(reply.storage_key);
}

// Memory-private transport. Only boolean/index facts cross the opaque frame boundary.
// The caller validates the reply against its exact DOM snapshot before applying it.
export interface MemoryBrowser {
  defaults: MemoryBrowserDefaults;
  call(value: MemoryBrowserRequest): Promise<Record<string, unknown>>;
  close(): void;
}
export function connectMemoryBrowser(revision: string, facts: {rail_present: boolean; rail_open: boolean}): Promise<MemoryBrowser> {
  return new Promise((resolve, reject) => {
    if (!/^sha256:[a-f0-9]{64}$/.test(revision)) {reject(new Error('Memory presentation program unavailable'));return}
    const frame = document.createElement('iframe');
    frame.hidden = true;
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.title = 'Memory isolated presentation';
    let port: MessagePort | undefined, sequence = 0, stopped = false, initialized = false;
    const pending = new Map<number, {resolve: (v: Record<string, unknown>) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout>}>();
    const unavailable = () => new Error('Memory presentation program unavailable');
    const close = () => {
      if (stopped) return;
      stopped = true;
      clearTimeout(startup);
      window.removeEventListener('message', loaded);
      window.removeEventListener('pagehide', close);
      port?.close();frame.remove();
      for (const entry of pending.values()) {clearTimeout(entry.timer);entry.reject(unavailable())}
      pending.clear();
      if (!initialized) reject(unavailable());
    };
    const call = (value: MemoryBrowserRequest): Promise<Record<string, unknown>> => new Promise((done, failed) => {
      if (stopped || !port || pending.size >= 32) {failed(unavailable());close();return}
      const seq = ++sequence;
      const timer = setTimeout(close, value.type === 'init' ? 10000 : 2000);
      pending.set(seq, {resolve: done, reject: failed, timer});
      try {port.postMessage({...value, seq})} catch {close()}
    });
    const loaded = (event: MessageEvent) => {
      if (stopped || port || event.source !== frame.contentWindow || event.origin !== 'null' || event.data?.channel !== 'steward-memory-browser' || event.data.type !== 'loaded') return;
      const channel = new MessageChannel();port = channel.port1;
      port.onmessageerror = close;
      port.onmessage = event => {
        const value = event.data;
        if (!value || typeof value !== 'object' || Array.isArray(value)) {close();return}
        if (value.type === 'connected' && !initialized && sequence === 0) {
          void call({type: 'init', ...facts}).then(reply => {
            if (!validMemoryDefaults(reply) || stopped) {close();return}
            initialized = true;clearTimeout(startup);resolve({call, close, defaults: reply as unknown as MemoryBrowserDefaults});
          }).catch(close);
          return;
        }
        const entry = pending.get(value.seq);
        if (!entry || value.type === 'failed') {close();return}
        clearTimeout(entry.timer);pending.delete(value.seq);entry.resolve(value);
      };
      port.start();
      frame.contentWindow!.postMessage({channel: 'steward-memory-browser', type: 'connect'}, '*', [channel.port2]);
      window.removeEventListener('message', loaded);
    };
    const startup = setTimeout(close, 15000);
    window.addEventListener('message', loaded);
    window.addEventListener('pagehide', close);
    frame.src = '/api/developer/memory/program?inspector_revision=' + encodeURIComponent(revision);
    document.body.append(frame);
  });
}
