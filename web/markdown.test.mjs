import assert from 'node:assert/strict';
import test from 'node:test';
import {buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';

const code=buildSync({entryPoints:[new URL('src/markdown-parser.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;
const module={exports:{}};runInNewContext(code,{module,exports:module.exports,URL});
const {parseMarkdown}=module.exports;
test('Chinese punctuation adjacent to emphasis renders without literal stars',()=>{
 for(const text of ['**调用与回传：**用户向 Main 提问。','**增长趋势：**A在增长。','**总体看，A更快。**从一月开始。']){
  const html=parseMarkdown(text);assert(html.includes('<strong>'));assert(!html.includes('**'));
 }
 assert(parseMarkdown('`**调用与回传：**`').includes('<code>**调用与回传：**</code>'));
 assert(parseMarkdown('\\*\\*literal\\*\\*').includes('**literal**'));
});
test('only the ephemeral streaming view completes unfinished formatting',()=>{
 const source='完整段落。\n\n**正在解释';
 assert(parseMarkdown(source,true).includes('<strong>正在解释</strong>'));
 assert(!parseMarkdown(source,false).includes('<strong>'));
 assert.equal(source,'完整段落。\n\n**正在解释');
});
test('format completion cannot authorize markup, images or unsafe destinations',()=>{
 for(const streaming of [true,false]){
  const html=parseMarkdown('<img src=x onerror=alert(1)>\n\n![image](https://outside.invalid)\n\n[bad](javascript:alert(1))',streaming);
  assert(!html.includes('<img'));assert(!html.includes('href="javascript:'));assert(html.includes('&lt;img'));
  assert(parseMarkdown('[source](artifact:owned)',streaming).includes('href="/artifact?ref=artifact%3Aowned"'));
 }
});
