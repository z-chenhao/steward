(async () => {
  const form = document.querySelector('[data-demo-review]');
  if (!form) return;
  const status = document.querySelector('[data-demo-status]'),
    list = document.querySelector('[data-demo-result]'),
    save = document.querySelector('[data-demo-export]');
  const cn = document.documentElement.lang.startsWith('zh'),
    say = (en, zh) => {
      status.textContent = cn ? zh : en;
    };
  const bookmark = document.querySelector('[data-demo-bookmark]');
  try {
    const response = await fetch('/teaching/recorder.js', {
      credentials: 'same-origin',
    });
    if (!response.ok) throw Error();
    bookmark.href = 'javascript:' + encodeURIComponent(await response.text());
    bookmark.hidden = false;
  } catch {
    say(
      'Recorder unavailable. Reload to try again.',
      '录制书签不可用，请刷新后再试。'
    );
  }
  let capture = null,
    sample = null,
    revision = 0,
    verified = null;
  const invalidate = () => {
    revision++;
    verified = null;
    save.hidden = true;
    list.replaceChildren();
    status.textContent = '';
  };
  form.addEventListener('input', invalidate);
  const validStep = (step) =>
    step &&
    ['activate', 'enter', 'select'].includes(step.action) &&
    typeof step.label === 'string' &&
    step.label.length > 0;
  for (const [selector, isSample] of [
    ['[data-demo-capture]', false],
    ['[data-demo-sample]', true],
  ]) {
    const field = form.querySelector(selector);
    field.onchange = async () => {
      invalidate();
      const file = field.files[0];
      if (isSample) sample = null;
      else {
        capture = null;
        form.elements.steps.value = '';
      }
      if (!file) return;
      try {
        if (file.size > 65536) throw Error();
        const value = JSON.parse(await file.text());
        if (field.files[0] !== file) return;
        if (
          value.format !== 'steward-browser-demo/1' ||
          typeof value.id !== 'string' ||
          !value.id ||
          !Array.isArray(value.steps) ||
          !value.steps.length ||
          value.steps.length > 32 ||
          !value.steps.every(validStep) ||
          !Array.isArray(value.sample) ||
          !Number.isSafeInteger(value.goal)
        )
          throw Error();
        if (isSample) sample = value;
        else {
          capture = value;
          form.elements.steps.value = value.steps
            .map((s) => s.action + ' ' + s.label)
            .join('\n');
        }
      } catch {
        if (field.files[0] === file) {
          field.value = '';
          say(
            'Invalid recording. Choose a recorder export.',
            '录制文件无效，请选择录制书签导出的文件。'
          );
        }
      }
    };
  }
  form.addEventListener(
    'submit',
    async (event) => {
      event.preventDefault();
      invalidate();
      const attempt = revision;
      if (!capture) return;
      if (sample?.id === capture.id) {
        say('Use a separately recorded sample.', '请选择另行录制的样例。');
        return;
      }
      const name = form.elements.name.value.trim();
      const steps = form.elements.steps.value
        .split('\n')
        .filter((line) => line.trim())
        .map((line) => {
          const match = /^(activate|enter|select)\s+(.+)$/.exec(line.trim());
          return match ? { action: match[1], label: match[2] } : null;
        });
      if (
        !steps.length ||
        steps.length > 32 ||
        steps.some((s) => !validStep(s))
      ) {
        say(
          'Check the action and label on each line.',
          '请检查每行的动作和标签。'
        );
        return;
      }
      try {
        const result = await fetch('/api/skills/demonstration', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name,
            steps,
            sample: sample?.sample ?? [],
            goal: sample?.goal ?? 0,
          }),
          signal: AbortSignal.timeout(15000),
        });
        if (!result.ok) throw Error();
        const view = await result.json();
        if (attempt !== revision) return;
        for (const text of view.steps) {
          const item = document.createElement('li');
          item.textContent = text.replace(/^\d+\. /, '');
          list.append(item);
        }
        if (!sample)
          say(
            'Draft ready. Select an independent sample to verify.',
            '草案已生成。请选择独立样例复验。'
          );
        else if (!view.verified)
          say(
            'Sample did not reach its goal. Matched steps: ' + view.matched,
            '样例未到达目标。已匹配步骤数：' + view.matched
          );
        else {
          verified = { name, steps: view.steps };
          save.hidden = false;
          say('Verified against the local sample.', '已在本地样例中复验。');
        }
      } catch {
        if (attempt === revision)
          say(
            'Review unavailable. Check access and the active experience plugin.',
            '复验不可用，请检查访问权限和经验插件状态。'
          );
      }
    },
    { capture: true }
  );
  save.onclick = () => {
    if (!verified) return;
    const name =
      verified.name
        .toLowerCase()
        .replace(/[^a-z0-9-]+/g, '-')
        .replace(/^-+|-+$/g, '')
        .slice(0, 80) || 'browser-demonstration';
    const source =
      '---\nname: ' +
      JSON.stringify(name) +
      '\ndescription: ' +
      JSON.stringify(verified.name) +
      '\n---\n\n' +
      verified.steps.join('\n') +
      '\n';
    const url = URL.createObjectURL(
        new Blob([source], { type: 'text/markdown' })
      ),
      link = document.createElement('a');
    link.href = url;
    link.download = 'SKILL.md';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
})();
