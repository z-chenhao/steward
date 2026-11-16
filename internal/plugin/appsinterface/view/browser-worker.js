// Private Apps browser policy. Executed only in the owning isolated Worker.
// Inputs contain display facts and positional indexes, never account IDs,
// authorization links, credentials, form bodies or an effect capability.
'use strict';
(() => {
  let presentation;
  const reply = (seq, value) => postMessage({seq, ...value});
  const card = bits => presentation.cards[bits];
  const pollPlan = value => {
    const policy = presentation.browser;
    const eligible = !value.stopped && !value.scheduled && !value.in_flight && value.state === 'pending';
    const deadline = value.deadline ?? value.now + policy.poll_window_ms;
    return {schedule: eligible && value.now < deadline, deadline, delay: policy.poll_interval_ms,
      notice: eligible && value.now >= deadline ? policy.messages.poll_paused : ''};
  };
  onmessage = event => {
    const value = event.data;
    if (value.type === 'init' && !presentation) {
      presentation = value.presentation;
      reply(value.seq, {type: 'ready', category_auto_submit: true});
      return;
    }
    if (!presentation) return;
    const policy = presentation.browser;
    if (value.type === 'project') {
      const accounts = value.accounts;
      const tiles = value.tiles.map(slug => {
        const plan = card(accounts.some(item => item.slug === slug) ? 16 : 0);
        return {manage_hidden: plan.manage_hidden, add_hidden: plan.add_hidden};
      });
      const details = value.details.map(detail => {
        const matching = [];
        let active = 0;
        accounts.forEach((item, index) => {
          if (item.slug !== detail.slug) return;
          matching.push(index);
          const kind = item.state === 'active' ? (item.public ? 2 : 1) : 0;
          active = policy.active_fold[active * 3 + kind];
        });
        const bits = (detail.suggested ? 1 : 0) | (detail.no_auth ? 2 : 0) |
          (active ? 4 : 0) | (active === 2 ? 8 : 0) |
          (matching.length ? 16 : 0) | (detail.require_alias ? 32 : 0);
        const plan = card(bits);
        return {accounts: matching, empty_hidden: plan.empty_hidden,
          first_hidden: plan.first_hidden, additional_hidden: plan.additional_hidden};
      });
      reply(value.seq, {type: 'projection', tiles, details});
      return;
    }
    if (value.type === 'poll') {
      reply(value.seq, {type: 'poll-plan', ...pollPlan(value)});
      return;
    }
    if (value.type === 'polls') {
      reply(value.seq, {type: 'poll-plans', plans: value.items.map(pollPlan)});
      return;
    }
    if (value.type === 'poll-result') {
      const index = value.state === 'active' ? 1 : value.state === 'pending' ? 2 :
        value.state === 'expired' ? 3 : value.state === 'disconnected' ? 4 : 0;
      reply(value.seq, {type: 'notice', ...policy.poll_notices[index]});
      return;
    }
    if (value.type === 'action-result') {
      const index = ['connect', 'continue', 'reconnect', 'check', 'disconnect'].indexOf(value.action);
      reply(value.seq, {type: 'action-view',
        notice: value.authorization ? (value.popup_opened ? policy.messages.authorization_opened : policy.messages.popup_blocked) : policy.success_notices[Math.max(0, index)],
        open_details: policy.open_details_after_authorization && value.authorization && !value.dialog_open && !value.detail_page});
      return;
    }
    if (value.type === 'action-failed') {
      reply(value.seq, {type: 'failure-view', reveal_alias: policy.reveal_alias && value.code === 'alias_required'});
    }
  };
})();
