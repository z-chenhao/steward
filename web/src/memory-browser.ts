import {connectBrowserProgram} from './browser-program';

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
function validMemoryDefaults(reply: Record<string, unknown>): reply is Record<string, unknown> & MemoryBrowserDefaults {
  return reply.type === 'ready' && validMemoryLabels(reply.labels) && Number.isSafeInteger(reply.compact_width) && Number(reply.compact_width) >= 320 && Number(reply.compact_width) <= 1600 && typeof reply.sheet_modal === 'boolean' && typeof reply.wide_open === 'boolean' && typeof reply.storage_key === 'string' && /^steward\.memory\.[a-z0-9.-]{1,64}$/.test(reply.storage_key);
}

// Memory-private transport. Only boolean/index facts cross the opaque frame boundary.
// The caller validates the reply against its exact DOM snapshot before applying it.
export interface MemoryBrowser {
  defaults: MemoryBrowserDefaults;
  call(value: MemoryBrowserRequest): Promise<Record<string, unknown>>;
  close(): void;
}
export async function connectMemoryBrowser(revision: string, facts: {rail_present: boolean; rail_open: boolean}): Promise<MemoryBrowser> {
  const errorMessage = 'Memory presentation program unavailable';
  if (!/^sha256:[a-f0-9]{64}$/.test(revision)) throw new Error(errorMessage);
  const {call, close, initial: defaults} = await connectBrowserProgram<MemoryBrowserRequest, MemoryBrowserDefaults>({
    src: '/api/developer/memory/program?inspector_revision=' + encodeURIComponent(revision),
    title: 'Memory isolated presentation',
    channel: 'steward-memory-browser',
    errorMessage,
    acceptsReply: reply => reply.type !== 'failed',
    async initialize(call) {
      const reply = await call({type: 'init', ...facts});
      if (!validMemoryDefaults(reply)) throw new Error(errorMessage);
      return reply;
    },
  });
  return {call, close, defaults};
}
