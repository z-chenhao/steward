import {readFileSync,readdirSync} from 'node:fs';
import {join,relative} from 'node:path';
import {createScanner,SyntaxKind} from 'typescript/unstable/ast';

const selectOwner='web/src/components/ui/select.ts';
const styleOwner='web/src/components/ui/controls.css';
const popupRoles=new Set(['listbox','option']);

// This enforces ownership of public control behavior, not class names or pixels.
export function controlViolations(path,source){
 const violations=[];
 const report=(offset,message)=>violations.push(`${path}:${source.slice(0,offset).split('\n').length}: ${message}`);
 const useSelect='Use the shared Select; pages keep native select values and business save handlers.';
 if(/\.(?:ts|tsx|js|jsx|mjs|cjs)$/.test(path)&&path!==selectOwner){
  const scanner=createScanner(true,undefined,source),tokens=[];
  for(let kind=scanner.scan();kind!==SyntaxKind.EndOfFile;kind=scanner.scan()){
   const literal=kind===SyntaxKind.StringLiteral||kind===SyntaxKind.NoSubstitutionTemplateLiteral;
   tokens.push({kind,literal,text:literal||kind===SyntaxKind.Identifier?scanner.getTokenValue():scanner.getTokenText(),offset:scanner.getTokenStart()});
  }
  tokens.forEach((token,i)=>{
   const prev=tokens[i-1]?.text,next=tokens[i+1]?.text;
   if(!path.startsWith('web/src/components/ui/')&&['showPopover','hidePopover'].includes(token.text)&&['.','?.','['].includes(prev))report(token.offset,useSelect);
   if(token.text==='role'&&next==='='&&tokens[i+2]?.literal&&popupRoles.has(tokens[i+2].text))report(token.offset,useSelect);
   if(token.text==='setAttribute'&&next==='('&&tokens[i+2]?.text==='role'&&tokens[i+3]?.text===','&&popupRoles.has(tokens[i+4]?.text))report(token.offset,useSelect);
   if(token.literal&&/role\s*=\s*["'](?:listbox|option)["']/.test(token.text))report(token.offset,useSelect);
  });
 }
 if(path.endsWith('.html')){
  const html=source.replace(/<!--[\s\S]*?-->|\{\{\/\*[\s\S]*?\*\/\}\}/g,match=>match.replace(/[^\n]/g,' '));
  for(const match of html.matchAll(/\brole\s*=\s*["'](?:listbox|option)["']/g))report(match.index,useSelect);
 }
 if(path.endsWith('.css')&&path!==styleOwner&&path!=='web/src/foundation.css'){
  const css=source.replace(/\/\*[\s\S]*?\*\//g,match=>match.replace(/[^\n]/g,' '));
  for(const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)){
   const [rule,selector,body]=match;
   const declarations=body.replace(/"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'/g,'""');
   const ownsPopup=/\[\s*role\s*=\s*["']?(?:switch|listbox|option)\b/.test(selector);
   const nativeSelect=/(?:^|[\s>,+~:(])select(?=[\s.#[:>,+~)]|$)/.test(selector);
   const checkbox=/\binput\s*\[\s*type\s*=\s*["']?checkbox\b/.test(selector);
   const properties=Array.from(declarations.matchAll(/(?:^|;)\s*([-\w]+)\s*:/g),match=>match[1].toLowerCase());
   const skin=/^(?:-webkit-appearance|appearance|background(?:-.*)?|border(?:-.*)?|color|font(?:-.*)?|padding(?:-.*)?|outline(?:-.*)?|box-shadow|opacity|(?:min-|max-)?height|accent-color)$/;
   const nativeSkin=(nativeSelect||checkbox)&&properties.some(property=>skin.test(property));
   const nativeAppearance=/\binput\b/.test(selector)&&properties.some(property=>property==='appearance'||property==='-webkit-appearance');
   if(ownsPopup||nativeSkin||nativeAppearance)report(match.index+rule.indexOf(selector),'Control skins belong in components/ui/controls.css; pages may only arrange their containers.');
  }
 }
 return violations;
}

export function assertSharedControls(repositoryRoot){
 const files=[];
 const visit=directory=>{
  for(const entry of readdirSync(directory,{withFileTypes:true})){
   const path=join(directory,entry.name);
   if(entry.isDirectory())visit(path);
   else if(/\.(?:ts|tsx|js|jsx|mjs|cjs|css|html)$/.test(entry.name))files.push(path);
  }
 };
 visit(join(repositoryRoot,'web/src'));
 visit(join(repositoryRoot,'internal/interaction/web/templates'));
 visit(join(repositoryRoot,'internal/plugin'));
 const errors=files.flatMap(path=>controlViolations(relative(repositoryRoot,path).replaceAll('\\','/'),readFileSync(path,'utf8')));
 if(errors.length)throw new Error('Shared control ownership violations:\n'+errors.join('\n'));
}
