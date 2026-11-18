interface BrowserProgram<Request extends object> {
  call(request: Request): Promise<Record<string, unknown>>;
  close(): void;
}

interface ProgramOptions<Request extends object, Initial> {
  src: string;
  title: string;
  channel: string;
  errorMessage: string;
  initialize(call: BrowserProgram<Request>['call']): Initial | Promise<Initial>;
  acceptsReply?(reply: Record<string, unknown>, requestType: unknown): boolean;
}

export function connectBrowserProgram<Request extends object, Initial>(options: ProgramOptions<Request, Initial>): Promise<BrowserProgram<Request> & {initial: Initial}> {
  return new Promise((resolve, reject) => {
    const frame = document.createElement('iframe');
    frame.hidden = true;
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.title = options.title;
    let port: MessagePort | undefined;
    let sequence = 0, connected = false, stopped = false;
    const pending = new Map<number, {
      requestType: unknown;
      resolve(value: Record<string, unknown>): void;
      reject(error: Error): void;
      timer: ReturnType<typeof setTimeout>;
    }>();
    const unavailable = () => new Error(options.errorMessage);

    const close = () => {
      if (stopped) return;
      stopped = true;
      clearTimeout(startup);
      window.removeEventListener('message', loaded);
      window.removeEventListener('pagehide', close);
      port?.close();
      frame.remove();
      for (const entry of pending.values()) {
        clearTimeout(entry.timer);
        entry.reject(unavailable());
      }
      pending.clear();
      reject(unavailable());
    };
    const call: BrowserProgram<Request>['call'] = request => new Promise((done, failed) => {
      if (stopped || !connected || !port || pending.size >= 32) {
        failed(unavailable());
        close();
        return;
      }
      const seq = ++sequence;
      const message = {...request, seq};
      const requestType = 'type' in message ? message.type : undefined;
      const timer = setTimeout(close, requestType === 'init' ? 10000 : 2000);
      pending.set(seq, {requestType, resolve: done, reject: failed, timer});
      try { port.postMessage(message); } catch { close(); }
    });
    const receive = async ({data}: MessageEvent<unknown>) => {
      if (!data || typeof data !== 'object' || Array.isArray(data)) { close(); return; }
      const reply = data as Record<string, unknown>;
      if (reply.type === 'connected' && !connected) {
        connected = true;
        try {
          const initial = await options.initialize(call);
          if (stopped) return;
          clearTimeout(startup);
          resolve({call, close, initial});
        } catch { close(); }
        return;
      }
      const seq = reply.seq;
      const entry = typeof seq === 'number' ? pending.get(seq) : undefined;
      if (typeof seq !== 'number' || !entry || options.acceptsReply?.(reply, entry.requestType) === false) { close(); return; }
      clearTimeout(entry.timer);
      pending.delete(seq);
      entry.resolve(reply);
    };
    const loaded = (event: MessageEvent) => {
      const target = frame.contentWindow;
      if (stopped || port || !target || event.source !== target || event.origin !== 'null' || event.data?.channel !== options.channel || event.data.type !== 'loaded') return;
      const channel = new MessageChannel();
      port = channel.port1;
      port.onmessageerror = close;
      port.onmessage = receive;
      port.start();
      target.postMessage({channel: options.channel, type: 'connect'}, '*', [channel.port2]);
      window.removeEventListener('message', loaded);
    };
    const startup = setTimeout(close, 15000);
    window.addEventListener('message', loaded);
    window.addEventListener('pagehide', close);
    frame.src = options.src;
    document.body.append(frame);
  });
}
