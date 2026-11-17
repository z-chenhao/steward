import type {AppInterfacePresentation} from './app-interface';

// Exact pre-interface/default23/direct browser interpretation only. Missing or
// invalid managed profiles never enter this path. Delete when these real old
// pages/compositions and their retained references retire.
export function nativeAppInterface(): AppInterfacePresentation {
  const cards = Array.from({length: 64}, (_, index) => {
    const suggested = Boolean(index & 1), noAuth = Boolean(index & 2), active = Boolean(index & 4), publicActive = Boolean(index & 8), managed = Boolean(index & 16), alias = Boolean(index & 32);
    return {unavailable: suggested, first_label: noAuth ? 'Enable app' : 'Connect', first_hidden: active || alias, additional: !suggested, additional_hidden: (!active && !alias) || publicActive, empty_hidden: managed, manage_hidden: !managed, add_hidden: managed};
  });
  return {cards, browser: {
    poll_notices: [{text: '', error: false}, {text: 'Account connected.', error: false}, {text: '', error: false}, {text: 'Authorization expired. Reconnect the account to continue.', error: true}, {text: '', error: false}],
    success_notices: ['App settings updated.', 'App settings updated.', 'App settings updated.', 'App settings updated.', 'Account disconnected.'],
    active_fold: [0, 1, 2, 1, 1, 1, 2, 2, 2],
    popup_actions: ['connect', 'continue', 'reconnect'],
    open_details_after_authorization: true, reveal_alias: true,
    poll_interval_ms: 5000, poll_window_ms: 600000,
    detail_timeout_ms: 15000, action_timeout_ms: 45000,
    messages: {
      open_authorization: 'Open authorization page',
      details_failed: 'App details could not be loaded. Refresh and try again.',
      connection_unknown: 'The connection could not be confirmed. Refresh the page before trying again.',
      poll_paused: 'Automatic status checks paused. Refresh the status or reconnect the account.',
      poll_failed: 'Status could not be refreshed. Your accounts have been kept; retrying shortly.',
      authorization_failed: 'The authorization page could not be opened.',
      authorization_opened: 'Finish signing in in the authorization tab. This page checks automatically.',
      popup_blocked: 'The browser blocked the authorization tab. Use Open authorization page to continue.',
      link_expired: 'This authorization link has expired. Reconnect the account to continue.'
    }
  }};
}

// Exact pre-browser graph consumer only. Current isolated programs never use
// this decision implementation. Retire with those graphs and original pages.
export function legacyAppProjection(facts: import('./apps-browser').AppsDisplayFacts, presentation: AppInterfacePresentation): import('./apps-browser').AppsDisplayProjection {
  return {
    tiles: facts.tiles.map(slug => {
      const plan = presentation.cards[facts.accounts.some(account => account.slug === slug) ? 16 : 0];
      return {manage_hidden: plan.manage_hidden, add_hidden: plan.add_hidden};
    }),
    details: facts.details.map(detail => {
      const indexes = facts.accounts.flatMap((account, at) => account.slug === detail.slug ? [at] : []);
      let active = 0;
      for (const index of indexes) {const account = facts.accounts[index];const kind = account.state === 'active' ? (account.public ? 2 : 1) : 0;active = presentation.browser.active_fold[active * 3 + kind]}
      const bits = (detail.suggested ? 1 : 0) | (detail.no_auth ? 2 : 0) | (active ? 4 : 0) | (active === 2 ? 8 : 0) | (indexes.length ? 16 : 0) | (detail.require_alias ? 32 : 0);
      const plan = presentation.cards[bits];
      return {accounts: indexes, empty_hidden: plan.empty_hidden, first_hidden: plan.first_hidden, additional_hidden: plan.additional_hidden};
    })
  };
}

export function legacyAppPollPlan(fact: import('./apps-browser').AppsPollFacts, p: AppInterfacePresentation): import('./apps-browser').AppsPollPlan {
  const eligible=!fact.stopped&&!fact.scheduled&&!fact.in_flight&&fact.state==='pending';
  const deadline=fact.deadline??fact.now+p.browser.poll_window_ms;
  return {schedule:eligible&&fact.now<deadline,deadline,delay:p.browser.poll_interval_ms,notice:eligible&&fact.now>=deadline?p.browser.messages.poll_paused:''};
}
export function legacyAppPollNotice(state: string, p: AppInterfacePresentation): {text:string;error:boolean} {
  const index=state==='active'?1:state==='pending'?2:state==='expired'?3:state==='disconnected'?4:0;
  return p.browser.poll_notices[index];
}
export function legacyAppActionView(f: import('./apps-browser').AppsActionFacts, p: AppInterfacePresentation): {notice:string;open_details:boolean} {
  const index=['connect','continue','reconnect','check','disconnect'].indexOf(f.action);
  return {notice:f.authorization?(f.popup_opened?p.browser.messages.authorization_opened:p.browser.messages.popup_blocked):p.browser.success_notices[Math.max(0,index)],open_details:p.browser.open_details_after_authorization&&f.authorization&&!f.dialog_open&&!f.detail_page};
}
export function legacyAppFailureView(code: string, p: AppInterfacePresentation): {reveal_alias:boolean} {
  return {reveal_alias:p.browser.reveal_alias&&code==='alias_required'};
}
