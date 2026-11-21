(() => {
  if (
    !['localhost', '127.0.0.1', '[::1]'].includes(location.hostname) &&
    location.protocol !== 'file:'
  ) {
    alert('Steward records only a local sample page.');
    return;
  }
  if (document.querySelector('[data-steward-demonstration]')) return;
  const host = document.createElement('dialog');
  host.dataset.stewardDemonstration = '';
  host.setAttribute('aria-label', 'Steward demonstration');
  document.body.append(host);
  host.show();
  const button = (text) => {
    const node = document.createElement('button');
    node.type = 'button';
    node.textContent = text;
    host.append(node);
    return node;
  };
  const status = document.createElement('span');
  status.setAttribute('role', 'status');
  host.append(status);
  const start = button('Start recording'),
    stop = button('Stop and save'),
    cancel = button('Cancel');
  stop.disabled = true;
  const steps = [],
    transitions = [];
  let active = false;
  const listener = new AbortController();
  function finish() {
    active = false;
    listener.abort();
    host.remove();
  }
  function record(event) {
    if (
      !event.isTrusted ||
      !active ||
      host.contains(event.target) ||
      !(event.target instanceof Element)
    )
      return;
    const control = event.target.closest(
      'button,a[href],input,textarea,select,[role=button]'
    );
    if (
      !control ||
      control.matches(
        '[disabled],[type=password],[type=hidden],[autocomplete^=cc-],[autocomplete*=password],[autocomplete=one-time-code]'
      )
    )
      return;
    const tag = control.localName;
    const action =
      tag === 'select'
        ? 'select'
        : tag === 'textarea' ||
            (tag === 'input' &&
              [
                'text',
                'search',
                'email',
                'url',
                'number',
                'date',
                'time',
              ].includes(control.type))
          ? 'enter'
          : 'activate';
    if (event.type !== (action === 'activate' ? 'click' : 'change')) return;
    if (
      tag === 'input' &&
      action === 'activate' &&
      !['checkbox', 'radio', 'button', 'submit'].includes(control.type)
    )
      return;
    let label = control.getAttribute('aria-label');
    if (!label) {
      const source =
        control.labels?.[0] || (['button', 'a'].includes(tag) ? control : null);
      const copy = source?.cloneNode(true);
      copy
        ?.querySelectorAll(
          'input,textarea,select,[contenteditable],script,style,[hidden],[aria-hidden=true]'
        )
        .forEach((node) => node.remove());
      label = copy?.textContent || '';
    }
    label = label.trim().replace(/\s+/g, ' ');
    if (!label || [...label].length > 120 || steps.length >= 32) return;
    const step = { action, label };
    transitions.push({ from: steps.length, step, to: steps.length + 1 });
    steps.push(step);
    status.textContent = steps.length + ' steps ';
  }
  start.onclick = () => {
    active = true;
    start.disabled = true;
    stop.disabled = false;
    status.textContent = 'Recording ';
    for (const type of ['click', 'change'])
      document.addEventListener(type, record, {
        capture: true,
        signal: listener.signal,
      });
  };
  cancel.onclick = finish;
  stop.onclick = () => {
    if (!steps.length) {
      status.textContent = 'Record a labeled control first. ';
      return;
    }
    const capture = {
      format: 'steward-browser-demo/1',
      id: crypto.randomUUID(),
      steps,
      sample: transitions,
      goal: steps.length,
    };
    const link = document.createElement('a'),
      url = URL.createObjectURL(
        new Blob([JSON.stringify(capture)], { type: 'application/json' })
      );
    link.href = url;
    link.download = 'steward-demonstration.json';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    finish();
  };
})();
