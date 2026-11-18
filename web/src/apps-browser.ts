import {connectBrowserProgram} from './browser-program';
import type {AppInterfacePresentation} from './app-interface';

// Apps-private transport. Only display facts cross the opaque frame boundary.
// The caller validates the reply against its exact DOM snapshot before applying it.
export interface AppsBrowser {
  categoryAutoSubmit: boolean;
  call(value: Record<string, unknown>): Promise<Record<string, unknown>>;
  close(): void;
}
export async function connectAppsBrowser(revision: string, presentation: AppInterfacePresentation): Promise<AppsBrowser> {
  const errorMessage = 'Apps presentation program unavailable';
  const {call, close, initial: categoryAutoSubmit} = await connectBrowserProgram<Record<string, unknown>, boolean>({
    src: '/api/apps/browser?interface_revision=' + encodeURIComponent(revision),
    title: 'Apps isolated presentation',
    channel: 'steward-apps-browser',
    errorMessage,
    async initialize(call) {
      const reply = await call({type: 'init', presentation});
      if (reply.type !== 'ready' || typeof reply.category_auto_submit !== 'boolean') throw new Error(errorMessage);
      return reply.category_auto_submit;
    },
  });
  return {call, close, categoryAutoSubmit};
}

export interface AppsDisplayFacts {
  accounts: {slug: string; state: string; public: boolean}[];
  tiles: string[];
  details: {slug: string; suggested: boolean; no_auth: boolean; require_alias: boolean}[];
}
export interface AppsDisplayProjection {
  tiles: {manage_hidden: boolean; add_hidden: boolean}[];
  details: {accounts: number[]; empty_hidden: boolean; first_hidden: boolean; additional_hidden: boolean}[];
}
// Candidate projections can arrange existing rows, but cannot omit, duplicate,
// substitute or move an account across its original application scope.
export function validateAppsProjection(raw: Record<string, unknown>, facts: AppsDisplayFacts): AppsDisplayProjection {
  const bad = () => new Error('Invalid Apps presentation projection');
  if (raw.type !== 'projection' || !Array.isArray(raw.tiles) || raw.tiles.length !== facts.tiles.length || !Array.isArray(raw.details) || raw.details.length !== facts.details.length) throw bad();
  for (const tile of raw.tiles) if (!tile || typeof tile !== 'object' || typeof tile.manage_hidden !== 'boolean' || typeof tile.add_hidden !== 'boolean') throw bad();
  raw.details.forEach((detail, index) => {
    if (!detail || typeof detail !== 'object' || !['empty_hidden', 'first_hidden', 'additional_hidden'].every(key => typeof detail[key] === 'boolean') || !Array.isArray(detail.accounts)) throw bad();
    const matching = facts.accounts.flatMap((account, at) => account.slug === facts.details[index].slug ? [at] : []);
    const indexes: unknown[] = detail.accounts;
    if (indexes.length !== matching.length || new Set(indexes).size !== matching.length || !indexes.every(at => Number.isSafeInteger(at) && matching.includes(at as number))) throw bad();
  });
  return {tiles: raw.tiles, details: raw.details} as AppsDisplayProjection;
}

export interface AppsPollFacts {
  state: string; stopped: boolean; scheduled: boolean; in_flight: boolean;
  now: number; deadline?: number;
}
export interface AppsPollPlan {schedule: boolean; deadline: number; delay: number; notice: string}
export interface AppsActionFacts {action: string; authorization: boolean; popup_opened: boolean; dialog_open: boolean; detail_page: boolean}
const displayText = (value: unknown): value is string => typeof value === 'string' && [...value].length <= 512;
export function validateAppsPollPlans(raw: Record<string, unknown>, facts: AppsPollFacts[]): AppsPollPlan[] {
  if (raw.type !== 'poll-plans' || !Array.isArray(raw.plans) || raw.plans.length !== facts.length) throw Error('Invalid Apps poll projection');
  raw.plans.forEach((plan, at) => {
    const fact = facts[at];
    if (!plan || typeof plan.schedule !== 'boolean' || !Number.isSafeInteger(plan.deadline) || !Number.isSafeInteger(plan.delay) || plan.delay < 5000 || plan.delay > 60000 || !displayText(plan.notice) || (fact.deadline !== undefined ? plan.deadline !== fact.deadline : plan.deadline < fact.now || plan.deadline > fact.now + 600000) || (plan.schedule && (fact.stopped || fact.scheduled || fact.in_flight || fact.state !== 'pending' || fact.now >= plan.deadline))) throw Error('Invalid Apps poll projection');
  });
  return raw.plans;
}
export function validateAppsNotice(raw: Record<string, unknown>): {text: string; error: boolean} {
  if (raw.type !== 'notice' || !displayText(raw.text) || typeof raw.error !== 'boolean') throw Error('Invalid Apps notice projection');
  return {text: raw.text, error: raw.error};
}
export function validateAppsActionView(raw: Record<string, unknown>, facts: AppsActionFacts): {notice: string; open_details: boolean} {
  if (raw.type !== 'action-view' || !displayText(raw.notice) || typeof raw.open_details !== 'boolean' || (raw.open_details && (!facts.authorization || facts.dialog_open || facts.detail_page))) throw Error('Invalid Apps action projection');
  return {notice: raw.notice, open_details: raw.open_details};
}
export function validateAppsFailureView(raw: Record<string, unknown>, code: string): {reveal_alias: boolean} {
  if (raw.type !== 'failure-view' || typeof raw.reveal_alias !== 'boolean' || (raw.reveal_alias && code !== 'alias_required')) throw Error('Invalid Apps failure projection');
  return {reveal_alias: raw.reveal_alias};
}
