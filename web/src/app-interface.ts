export interface AppCardPresentation {
  unavailable: boolean;
  first_label: string;
  first_hidden: boolean;
  additional: boolean;
  additional_hidden: boolean;
  empty_hidden: boolean;
  manage_hidden: boolean;
  add_hidden: boolean;
}
export interface AppBrowserPresentation {
  poll_notices: {text: string; error: boolean}[];
  success_notices: string[];
  active_fold: number[];
  popup_actions: string[];
  open_details_after_authorization: boolean;
  reveal_alias: boolean;
  poll_interval_ms: number;
  poll_window_ms: number;
  detail_timeout_ms: number;
  action_timeout_ms: number;
  messages: {
    open_authorization: string;
    details_failed: string;
    connection_unknown: string;
    poll_paused: string;
    poll_failed: string;
    authorization_failed: string;
    authorization_opened: string;
    popup_blocked: string;
    link_expired: string;
  };
}
export interface AppInterfacePresentation {
  browser_view?: boolean;
  cards: AppCardPresentation[];
  browser: AppBrowserPresentation;
}
// This is an internal browser DTO consumed from the authenticated, escaped page,
// not a plugin SDK or permission to run a candidate's browser code.
export function managedAppInterface(raw: string | undefined): AppInterfacePresentation | null {
  try {
    const value = JSON.parse(raw ?? '') as AppInterfacePresentation;
    if (!Array.isArray(value.cards) || value.cards.length !== 64 || !Array.isArray(value.browser?.active_fold) || value.browser.active_fold.length !== 9) return null;
    if (value.cards.some(card => !card || typeof card.first_hidden !== 'boolean' || typeof card.additional_hidden !== 'boolean')) return null;
    if(value.browser_view!==undefined&&typeof value.browser_view!=='boolean')return null;
    const browser = value.browser;
    if (browser.poll_interval_ms < 5000 || browser.poll_interval_ms > 60000 || browser.poll_window_ms < 5000 || browser.poll_window_ms > 600000 || browser.detail_timeout_ms < 1 || browser.detail_timeout_ms > 15000 || browser.action_timeout_ms < 1 || browser.action_timeout_ms > 45000) return null;
    if (!Array.isArray(browser.poll_notices) || browser.poll_notices.length !== 5 || !Array.isArray(browser.success_notices) || browser.success_notices.length !== 5) return null;
    if (!browser.messages || Object.values(browser.messages).length !== 9 || Object.values(browser.messages).some(text => typeof text !== 'string' || !text.trim() || [...text].length > 512)) return null;
    if (!Array.isArray(browser.popup_actions) || browser.popup_actions.some(action => !['connect', 'continue', 'reconnect'].includes(action))) return null;
    return value;
  } catch { return null; }
}
