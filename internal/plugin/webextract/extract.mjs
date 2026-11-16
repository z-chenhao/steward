// Plugin-owned HTML/PDF recipe. Host supplies original bytes and an isolated interpreter.
import { Readability } from '@mozilla/readability';
import { Defuddle } from 'defuddle';
import { parseHTML } from 'linkedom';
import TurndownService from 'turndown';
import { extractText, getDocumentProxy } from 'unpdf';

globalThis.fetch = async () => { throw new Error('Extractor network access is disabled'); };
console.log = console.info = console.debug = () => {};
try {
  let input = '';
  for await (const chunk of process.stdin) { input += chunk; if (input.length > 12 * 1024 * 1024) throw new Error('INPUT_LIMIT'); }
  const request = JSON.parse(input);
  const data = Buffer.from(request.data, 'base64');
  let title = '', text = '';
  if (request.mediaType === 'application/pdf') {
    const pdf = await getDocumentProxy(new Uint8Array(data));
    try {
      if (pdf.numPages > 200) throw new Error('PAGE_LIMIT');
      const result = await extractText(pdf, { mergePages: false });
      text = result.text.map((page, index) => `## Page ${index + 1}\n\n${page}`).join('\n\n');
    } finally { await pdf.destroy?.(); }
  } else {
    const { document } = parseHTML(data.toString('utf8'));
    for (const node of document.querySelectorAll('script,style,noscript,iframe,form,template,svg')) node.remove();
    // Resolve only safe HTTP links before Markdown conversion.
    for (const link of document.querySelectorAll('a[href]')) {
      try { const target = new URL(link.getAttribute('href'), request.url); if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password) link.removeAttribute('href'); else link.setAttribute('href', target.href); }
      catch { link.removeAttribute('href'); }
    }
    let article;
    try { article = new Readability(document.cloneNode(true)).parse(); } catch { /* Alternate local parser below. */ }
    if (!article?.content) {
      const fallback = new Defuddle(document).parse();
      article = { title: fallback.title, content: fallback.content };
    }
    title = article?.title || document.title || '';
    const converter = new TurndownService({ headingStyle: 'atx', codeBlockStyle: 'fenced' });
    converter.remove(['script', 'style', 'nav', 'footer', 'form', 'iframe', 'img']);
    text = converter.turndown(article?.content || document.body?.innerHTML || '');
  }
  if (!text.trim() || Buffer.byteLength(text) > 5 * 1024 * 1024) throw new Error('CONTENT_LIMIT');
  process.stdout.write(JSON.stringify({ title: title.slice(0, 1024), text }));
} catch { process.stdout.write(JSON.stringify({ error: 'WEB_EXTRACTION_FAILED' })); process.exitCode = 1; }
