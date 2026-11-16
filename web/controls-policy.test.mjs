import assert from 'node:assert/strict';
import test from 'node:test';
import {mkdtempSync,mkdirSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {controlViolations,assertSharedControls} from './controls-policy.mjs';

const page='web/src/future-page.tsx';
for(const source of [
 'menu.showPopover();',
 'menu?.hidePopover();',
 'menu["showPopover"]();',
 'list.role = "listbox";',
 'item.setAttribute("role", "option");',
 'const element=<div role="listbox" />;',
 'const html=`<div role="listbox"></div>`;',
])test('reject a page-owned picker: '+source,()=>assert.ok(controlViolations(page,source).length));

test('JavaScript and JSX pages obey the same ownership boundary',()=>{
 for(const path of ['web/src/page.jsx','web/src/page.mjs','web/src/page.cjs'])assert.ok(controlViolations(path,'menu.showPopover();').length);
});

test('shared owner can render popups and list options',()=>{
 assert.deepEqual(controlViolations('web/src/components/ui/select.ts','menu.showPopover(); list.role="listbox";'),[]);
 assert.deepEqual(controlViolations('web/src/components/ui/tooltip.ts','tip.showPopover(); tip.role="tooltip";'),[]);
 assert.ok(controlViolations('web/src/components/ui/private-picker.ts','list.role="listbox";').length);
});
test('native values, switch consumers, comments and prose remain valid',()=>{
 assert.deepEqual(controlViolations(page,'// menu.showPopover();\nconst note="showPopover";\nconst form=<><select name="frequency"/><input type="checkbox" role="switch"/></>;'),[]);
 assert.deepEqual(controlViolations('internal/interaction/web/templates/schedule-editor.html','<select name="frequency"><option>Weekly</option></select><input type="checkbox" role="switch">'),[]);
});
test('all server templates use native selects, including language fallback',()=>{
 const markup='<div role="listbox"><div role="option">English</div></div>';
 assert.equal(controlViolations('internal/interaction/web/templates/future.html',markup).length,2);
 assert.equal(controlViolations('internal/interaction/web/templates/settings.html',markup).length,2);
});
for(const source of [
 'select { appearance:none }',
 'select { border-radius:3px; font-size:12px!important }',
 'input[type=checkbox] { background:red }',
 '@media(max-width:760px){input[type=checkbox]{-webkit-appearance:none}}',
 'input[role="switch"]:checked { background:red }',
 '[role=listbox] { border-radius:3px }',
])test('reject a page-owned control skin: '+source,()=>assert.ok(controlViolations('web/src/future-page.css',source).length));
test('shared skins and ordinary layout rules remain valid',()=>{
 assert.deepEqual(controlViolations('web/src/components/ui/controls.css','input[role=switch]{appearance:none}'),[]);
 assert.deepEqual(controlViolations('web/src/foundation.css','input,select{font:inherit}'),[]);
 assert.deepEqual(controlViolations('web/src/future-page.css','/* select{appearance:none} */\n.row{display:flex;gap:10px} select{width:100%} select::before{content:"appearance:none"}'),[]);
});

test('the gate discovers new nested pages and reports an actionable file location',()=>{
 const root=mkdtempSync(join(tmpdir(),'steward-controls-policy-'));
 try{
  mkdirSync(join(root,'web/src/future'),{recursive:true});mkdirSync(join(root,'internal/interaction/web/templates'),{recursive:true});mkdirSync(join(root,'internal/plugin/modelconnections/view'),{recursive:true});
  writeFileSync(join(root,'web/src/future/picker.ts'),'// private dropdown\nmenu.showPopover();');
  assert.throws(()=>assertSharedControls(root),/web\/src\/future\/picker.ts:2: Use the shared Select/);
  writeFileSync(join(root,'web/src/future/picker.ts'),'const field=document.querySelector("select");');
  assert.doesNotThrow(()=>assertSharedControls(root));
  const owned=join(root,'internal/plugin/modelconnections/view/models.html');
  writeFileSync(owned,'<div role="listbox"></div>');
  assert.throws(()=>assertSharedControls(root),/internal\/plugin\/modelconnections\/view\/models.html:1: Use the shared Select/);
  writeFileSync(owned,'<select name="service"><option>OpenAI</option></select>');
  assert.doesNotThrow(()=>assertSharedControls(root));
 }finally{rmSync(root,{recursive:true,force:true})}
});
