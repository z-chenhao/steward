// Private Memory feature configuration, authored by Host after sealed-resource
// validation. It grants copy/version facts only, never a DOM or effect API.
export interface MemoryTranslation {english: string; chinese: string}
export interface MemoryPresentation {revision: string; copy: MemoryTranslation[]}

export function validateMemoryPresentation(value: unknown, revision: string): MemoryPresentation {
  const bad = () => new Error('Memory presentation unavailable');
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw bad();
  const v = value as Record<string, unknown>;
  if (Object.keys(v).length !== 2 || Object.keys(v).some(key => key !== 'revision' && key !== 'copy') || v.revision !== revision || !/^sha256:[a-f0-9]{64}$/.test(revision) || !Array.isArray(v.copy) || v.copy.length < 1 || v.copy.length > 256) throw bad();
  const seen = new Set<string>();
  const copy = v.copy.map(entry => {
    if (!entry || typeof entry !== 'object' || Array.isArray(entry) || Object.keys(entry).length !== 2 || Object.keys(entry).some(key => key !== 'english' && key !== 'chinese') || typeof entry.english !== 'string' || !entry.english || entry.english.length > 1024 || typeof entry.chinese !== 'string' || !entry.chinese || entry.chinese.length > 2048 || seen.has(entry.english)) throw bad();
    seen.add(entry.english);
    return {english: entry.english, chinese: entry.chinese};
  });
  return {revision, copy};
}

export function memoryCopy(presentation: MemoryPresentation, language: string, text: string): string {
  if (language !== 'zh-CN') return text;
  const key = text.trim();
  const value = presentation.copy.find(entry => entry.english === key)?.chinese;
  return value ? text.replace(key, () => value) : text;
}

export function readMemoryPresentation(): MemoryPresentation | null {
  const styles = document.querySelector<HTMLLinkElement>('link[rel="stylesheet"][href^="/api/developer/memory/style?"]');
  if (!styles) return null; // Exact pre-service/direct consumer, not managed failure.
  const revision = new URL(styles.href, location.href).searchParams.get('inspector_revision') ?? '';
  const scripts = document.querySelectorAll<HTMLScriptElement>('script#memory-inspector-config[type="application/json"]');
  if (scripts.length !== 1 || !(scripts[0] instanceof HTMLScriptElement)) throw new Error('Memory presentation unavailable');
  return validateMemoryPresentation(JSON.parse(scripts[0].textContent ?? ''), revision);
}
