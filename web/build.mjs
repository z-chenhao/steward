import {build} from 'esbuild';
import {THEME_CSS,SVG_CLASSES_CSS,FORM_STYLES_CSS} from './src/components/open-intelligent-ui/design-system.ts';
import {createHash} from 'node:crypto';
import {assertSharedControls} from './controls-policy.mjs';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readFileSync,readdirSync,rmSync,writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('.',import.meta.url));
assertSharedControls(fileURLToPath(new URL('..',import.meta.url)));
const notices=['ad-agent','OpenIntelligentUI','shadcn-ui'].map(name=>readFileSync(root+'licenses/'+name+'.txt','utf8')).join('\n');
const js=await build({absWorkingDir:root,entryPoints:['src/main.tsx'],bundle:true,minify:true,write:false,metafile:true,
  format:'iife',target:['es2022'],outfile:'../internal/interaction/web/assets/client.js',
  jsx:'automatic',define:{'process.env.NODE_ENV':'"production"'},
  banner:{js:'/*! Steward UI; bundled third-party licenses:\n'+notices+'\n*/'},legalComments:'inline'});
const appearance=await build({absWorkingDir:root,entryPoints:['src/appearance.ts'],bundle:true,minify:true,write:false,format:'iife',target:['es2022']});
const diagrams=await build({absWorkingDir:root,entryPoints:['src/diagrams.ts'],bundle:true,minify:true,write:false,metafile:true,format:'iife',target:['es2022'],legalComments:'inline'});
const previewRuntime=await build({absWorkingDir:root,entryPoints:['src/preview-runtime.js'],bundle:true,minify:true,write:false,format:'iife',target:['es2022']});
const widgetRuntime=await build({absWorkingDir:root,entryPoints:['src/widget-runtime.js'],bundle:true,minify:true,write:false,metafile:true,format:'iife',target:['es2022'],legalComments:'inline'});
// Use Tailwind's file output: a synchronous child can otherwise lose buffered stdout.
const cssTemp=mkdtempSync(join(tmpdir(),'steward-web-css-'));
let css;
try{
  const output=join(cssTemp,'style.css');
  execFileSync(root+'node_modules/.bin/tailwindcss',['-i','src/style.css','--minify','--output',output],{cwd:root,stdio:['ignore','ignore','inherit']});
  css=readFileSync(output);
  if(!css.length)throw new Error('Tailwind produced an empty stylesheet');
}finally{rmSync(cssTemp,{recursive:true,force:true});}
// Only fonts get persistent caching; HTML, scripts and styles still refresh.
// The server accepts immutable caching only when this digest matches its bytes.
css=Buffer.from(css.toString().replace(/url\(['"]?(\/brand\/[^)'"?]+\.(?:woff2|ttf))['"]?\)/g,(_,path)=>{
  const digest=createHash('sha256').update(readFileSync(root+'../internal/interaction/web/assets'+path)).digest('hex');
  return `url('${path}?v=${digest}')`;
}));
const dependencyDirs=new Set(['node_modules/tailwindcss']);
for(const input of [...Object.keys(js.metafile.inputs),...Object.keys(diagrams.metafile.inputs),...Object.keys(widgetRuntime.metafile.inputs)]){
  const at=input.lastIndexOf('node_modules/');if(at<0)continue;
  const pieces=input.slice(at+13).split('/');
  dependencyDirs.add(input.slice(0,at+13)+pieces.slice(0,pieces[0].startsWith('@')?2:1).join('/'));
}
const dependencyLicenses=[...dependencyDirs].sort().map(directory=>{
  const path=join(root,directory),pkg=JSON.parse(readFileSync(join(path,'package.json'),'utf8'));
  const files=readdirSync(path,{withFileTypes:true}).filter(file=>file.isFile()&&/^(licen[cs]e|copying|notice)([.-]|$)/i.test(file.name));
  if(!files.length)throw new Error('Missing bundled license for '+pkg.name);
  return pkg.name+' '+pkg.version+'\n'+files.map(file=>readFileSync(join(path,file.name),'utf8')).join('\n');
});
const stewardLicenses=['LICENSE-Inter.txt','LICENSE-SourceSerif4.md','LICENSE-SourceHanSans.txt','LICENSE-JetBrainsMono.txt','LICENSE-Michroma.txt'].map(name=>readFileSync(root+'licenses/'+name,'utf8').replace(/[ \t]+$/gm,'')).join('\n\n');
const licenses=[notices,readFileSync(root+'licenses/app-icons.txt','utf8'),stewardLicenses,readFileSync(root+'licenses/steward-artwork.md','utf8'),...dependencyLicenses].join('\n\n').replace(/\r\n/g,'\n').replace(/[ \t]+$/gm,'');
const lightTheme=THEME_CSS.match(/:root\s*\{([\s\S]*?)\}/)[1],darkTheme=THEME_CSS.match(/@media[^\{]+\{\s*:root\s*\{([\s\S]*?)\}/)[1];
const widgetStyles=THEME_CSS+'\n'+SVG_CLASSES_CSS+'\n'+FORM_STYLES_CSS+'\n:root[data-widget-theme=light]{'+lightTheme+'}\n:root[data-widget-theme=dark]{'+darkTheme+'}';
const frameTheme=readFileSync(root+'src/components/ui/controls.css','utf8').split('/* Intelligent UI sandbox theme:')[1];
const embeddedFont="@font-face{font-family:Inter;src:url(data:font/woff2;base64,"+readFileSync(root+'../internal/interaction/web/assets/brand/InterVariable.woff2').toString('base64')+") format('woff2');font-style:normal;font-weight:100 900;font-display:swap}";
const outputs={'preview-runtime.js':previewRuntime.outputFiles[0].contents,'widget.css':widgetStyles+'\n/* Intelligent UI sandbox theme:'+frameTheme+'\n'+embeddedFont,'widget-runtime.js':widgetRuntime.outputFiles[0].contents,'appearance.js':appearance.outputFiles[0].contents,'client.js':js.outputFiles[0].contents,'diagrams.js':diagrams.outputFiles[0].contents,'style.css':css,'licenses.txt':licenses,'brand/NOTICE.txt':readFileSync(root+'licenses/steward-artwork.md')};
for(const [name,bytes] of Object.entries(outputs)){
  const path=root+'../internal/interaction/web/assets/'+name;
  if(process.argv.includes('--check')){
    if(!readFileSync(path).equals(Buffer.from(bytes)))throw new Error('Stale embedded UI asset: '+name+'; run npm run build --prefix web');
  }else writeFileSync(path,bytes);
}
