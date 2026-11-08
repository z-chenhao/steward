import assert from 'node:assert/strict';
import {selectValue,selectParts,assertDataList} from './channels_controls.mjs';
import {writeFile} from 'node:fs/promises';

// One scenario module for the maintained channels browser driver; no runner,
// browser lifecycle, fake DOM or alternate product endpoint lives here.
async function navigationViewports(page,evidence){
 const original=page.viewportSize();
 const measurements=[];
 for(const width of [1400,1024,761,760,390,320]){
  await page.setViewportSize({width,height:844});
  await page.evaluate(()=>document.fonts.ready);
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error(`settings overflowed at ${width}px`);
  const mobile=width<=760;
  if(mobile)await page.locator('.sidebar-open').click();
  const geometry=await page.evaluate(()=>{
   const rect=selector=>{const r=document.querySelector(selector).getBoundingClientRect();return {left:r.left,right:r.right,bottom:r.bottom}};
   return {primary:rect('.app-sidebar'),secondary:rect('.section-sidebar'),main:rect('.workspace-main'),settings:rect('.primary-settings'),viewport:innerWidth,height:innerHeight};
  });
  if(geometry.primary.left<0||Math.abs(geometry.primary.right-geometry.secondary.left)>1||geometry.secondary.right>width+1||geometry.height-geometry.settings.bottom>32||geometry.settings.bottom>geometry.height)throw Error(`navigation overlap or misplaced Settings at ${width}px: ${JSON.stringify(geometry)}`);
  if(!mobile&&Math.abs(geometry.secondary.right-geometry.main.left)>1)throw Error(`content overlaps navigation at ${width}px`);
  if(!await page.locator('.app-sidebar [aria-current="page"]').isVisible()||!await page.locator('.section-sidebar [aria-current="page"]').isVisible())throw Error(`navigation lost the selected module/category at ${width}px`);
  // The rail owns module links and its collapse control; personal preferences
  // have a single home in Settings, even if their old selectors are renamed.
  if(await page.locator('.app-sidebar').getByRole('button').count()!==1)throw Error(`primary rail acquired extra preference controls at ${width}px`);
  if(mobile){
   await page.keyboard.press('Escape');
   if(!await page.locator('.sidebar-open').evaluate(node=>node===document.activeElement)||await page.locator('#workspace-main').evaluate(node=>node.inert))throw Error(`closing navigation lost focus or kept content inert at ${width}px`);
  }
  measurements.push({width,...geometry});
 }
 await page.setViewportSize(original);
 await writeFile(evidence+'/navigation-viewports.json',JSON.stringify(measurements,null,2));
}

// Real navigation, native form writes and delayed bundle delivery. Check
// relationships before/after enhancement rather than platform-specific pixels.
export async function pageTransitions(page,context,url,evidence){
 const measurements={resources:[],collapse:[],loading:[],fonts:[],focus:[],settings:[]};
 await page.setViewportSize({width:1400,height:980});
 await page.evaluate(()=>localStorage.setItem('steward-sidebar','open'));
 const widths=[];
 for(const path of ['/', '/tasks', '/settings/preferences', '/developer/observability']){
  await page.goto(url+path);
  const sizes=await page.evaluate(()=>({primary:document.querySelector('.app-sidebar').getBoundingClientRect().width,secondary:document.querySelector('.section-sidebar')?.getBoundingClientRect().width}));
  widths.push(sizes.primary);if(sizes.secondary&&Math.abs(sizes.primary-sizes.secondary)>1)throw Error('primary and secondary navigation widths differ across modules');
 }
 if(Math.max(...widths)-Math.min(...widths)>1)throw Error('primary navigation resizes when switching modules');

 await page.addInitScript(()=>{
  window.navigationFontFrames=[];
  if(!['/settings/preferences','/settings/notifications'].includes(location.pathname))return;
  const sample=()=>{
   const brand=document.querySelector('.brand');
   if(brand)window.navigationFontFrames.push({brand:brand.getBoundingClientRect().width,ready:document.fonts.check('14px Inter')&&document.fonts.check('14px StewardWordmark')});
   if(performance.now()<300)requestAnimationFrame(sample);
  };
  requestAnimationFrame(sample);
 });
 const settle=async()=>{await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))};
 for(const language of ['en','zh-CN']){
  await context.request.post(url+'/settings/language',{form:{language}});
  for(const theme of ['light','dark']){
   await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
   for(const width of [1400,1024,820,800,780,761,760,390,320]){
    await page.setViewportSize({width,height:844});await page.goto(url+'/settings/models');await settle();
    const geometry=await page.evaluate(()=>({width:innerWidth,scrollWidth:document.documentElement.scrollWidth,
     title:document.querySelector('.settings-page h1').getBoundingClientRect().toJSON(),
     bounds:[...document.querySelectorAll('.resource-model .resource-name,.model-connection,.model-use,.model-consent')].map(node=>node.getBoundingClientRect().toJSON()),
     nameLines:[...document.querySelectorAll('.resource-model .resource-name')].map(node=>{const range=document.createRange();range.selectNodeContents(node);return range.getClientRects().length})}));
    if(geometry.scrollWidth>width||geometry.title.width<2||geometry.bounds.some(r=>r.left<0||r.right>width+1)||geometry.nameLines.some(lines=>lines>1))throw Error('Resources title, model name or controls overflowed: '+JSON.stringify({language,theme,...geometry}));
    const commands=await page.locator('.model-resources .setting-row .setting-action').evaluateAll(nodes=>nodes.filter(node=>node.checkVisibility()).map(node=>{const box=node.getBoundingClientRect(),row=node.closest('.setting-row').getBoundingClientRect();return {width:box.width,right:box.right,rowRight:row.right}}));
    if(commands.some(command=>Math.abs(command.right-command.rowRight)>1)||new Set(commands.map(command=>Math.round(command.width))).size>1)throw Error('Settings command widths or trailing alignment differ: '+JSON.stringify(commands));
    measurements.resources.push({language,theme,...geometry});
    if(width===761||width===390)await page.screenshot({path:`${evidence}/resources-${language}-${theme}-${width}.png`,fullPage:true});
   }
  }
 }
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 await page.setViewportSize({width:1400,height:980});await page.goto(url+'/settings/preferences');
 await settle();
 // Real native clicks must reuse the critical fonts from the previous page.
 for(const path of ['/settings/notifications','/settings/preferences','/settings/notifications']){
  await page.locator(`.settings-navigation a[href="${path}"]`).click();await page.waitForURL(url+path);await settle();
  const fonts=await page.evaluate(()=>({frames:window.navigationFontFrames,resources:performance.getEntriesByType('resource').filter(entry=>/\/(InterVariable\.woff2|Michroma-Regular\.ttf)\?/.test(entry.name)).map(entry=>({name:new URL(entry.name).pathname,bytes:entry.transferSize}))}));
  if(!fonts.frames.length||fonts.frames.some(frame=>!frame.ready)||fonts.frames.some(frame=>Math.abs(frame.brand-fonts.frames[0].brand)>1)||fonts.resources.length!==2||fonts.resources.some(resource=>resource.bytes!==0))throw Error('Settings navigation flashed or downloaded critical fonts again: '+JSON.stringify(fonts));
  measurements.fonts.push({path,...fonts});
 }
 const heading=await page.locator('.settings-page h1').boundingBox();
 for(const path of ['/settings/notifications','/settings/models','/settings/apps','/settings/capabilities']){
  await page.goto(url+path);const next=await page.locator('.settings-page h1').boundingBox();
  if(!next||Math.abs(next.x-heading.x)>1||Math.abs(next.y-heading.y)>1||Math.abs(next.height-heading.height)>1)throw Error('Settings title left its shared alignment: '+path);
 }
 for(const language of ['en','zh-CN']){
  await context.request.post(url+'/settings/language',{form:{language}});
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:844});await page.goto(url+'/settings/apps');await settle();
   if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error(`Apps overflowed at ${width}px`);
   if(await page.locator('.settings-navigation a[aria-current="page"]').count()!==1||!await page.locator('.settings-navigation a[href="/settings/apps"][aria-current="page"]').count())throw Error('Apps selection disagrees with Settings navigation');
   if(!await page.getByText('Gmail',{exact:true}).count()||await page.locator('button[value="connect"]').count())throw Error('unconfigured directory lost suggestions or enabled connection actions: '+JSON.stringify({language,width,url:page.url(),tiles:await page.locator('[data-app-tile]').count()}));
   await page.screenshot({path:`${evidence}/apps-${language}-${width}.png`});
   await page.locator('#app-search').fill('Notion');await page.locator('#app-search').press('Enter');
   if(!await page.getByText('Notion',{exact:true}).count()||await page.getByText('Gmail',{exact:true}).count())throw Error('Apps search did not filter the directory');
  }
 }
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 await page.setViewportSize({width:1400,height:980});await page.goto(url+'/settings/capabilities');
 // Collapse once, then cross every module and reload. The shared preference
 // must be respected even when the destination has two navigation rails.
 await page.locator('.brand-row [data-sidebar-toggle]').click();
 for(const path of ['/settings/preferences','/settings/models','/developer/observability','/developer/memory','/tasks','/']){
  await page.goto(url+path);await page.reload();
  const state=await page.evaluate(()=>({path:location.pathname,main:document.querySelector('main').getBoundingClientRect().x,secondary:document.querySelector('.section-sidebar')?.getBoundingClientRect().width??0,sidebar:getComputedStyle(document.querySelector('.app-sidebar')).display}));
  if((path.startsWith('/settings')||path.startsWith('/developer'))&&state.secondary===0)throw Error('collapsing primary navigation hid secondary navigation');
  if(Math.abs(state.main-(64+state.secondary))>1||state.sidebar==='none'||!await page.locator('.brand-row [data-sidebar-toggle]').isVisible())throw Error('collapsed navigation reset: '+JSON.stringify(state));
  measurements.collapse.push(state);
  if(path==='/settings/preferences'||path==='/developer/observability')await page.screenshot({path:evidence+'/navigation-collapsed-'+(path.includes('settings')?'settings':'developer')+'.png'});
 }
 for(const [width,path,collapsed,theme] of [[390,'/settings/preferences',false,'light'],[320,'/settings/preferences',false,'dark'],[1400,'/settings/preferences',false,'light'],[1400,'/',true,'dark'],[390,'/tasks',false,'dark']]){
  await page.setViewportSize({width,height:980});
  await page.evaluate(({collapsed,theme})=>{localStorage.setItem('steward-sidebar',collapsed?'collapsed':'open');localStorage.setItem('steward-theme',theme)},{collapsed,theme});
  let release;const held=new Promise(resolve=>{release=resolve});
  await page.route('**/app.js',async route=>{await held;await route.continue()});
  try{
   await page.goto(url+path,{waitUntil:'commit'});await page.locator('main').waitFor();
   await page.waitForFunction(()=>document.querySelector('link[rel=stylesheet]')?.sheet);await settle();
   if(await page.locator('[data-sidebar-toggle],[data-sidebar-close]').evaluateAll(buttons=>buttons.some(button=>!button.disabled)))throw Error('navigation became actionable before its event handlers loaded');
   const geometry=()=>{
    const selectors=['.workspace-main','.page-heading','.settings-group','.settings-footer','[data-appearance-setting]','[data-avatar-setting]',...Array.from({length:5},(_,i)=>`.settings-navigation a:nth-child(${i+1})`)];
    return {dark:document.documentElement.classList.contains('dark'),boxes:Object.fromEntries(selectors.map(selector=>{const node=document.querySelector(selector);return [selector,node?.getBoundingClientRect().toJSON()??null]}))};
   };
   if(path==='/settings/preferences'&&width===320){
    // Exercise the native/enhanced shrink boundary with every platform font.
    await page.locator('.preference-row').first().evaluate(node=>{
     const style=getComputedStyle(node),name=node.querySelector('.preference-name');
     node.style.inlineSize=`${name.getBoundingClientRect().width+parseFloat(style.columnGap)+116+parseFloat(style.paddingLeft)+parseFloat(style.paddingRight)}px`;
    });
   }
   const before=await page.evaluate(geometry);
   if(before.dark!==(theme==='dark')||before.boxes['.workspace-main'].y!==0||collapsed&&before.boxes['.workspace-main'].x!==64)throw Error('first paint used the wrong theme/navigation');
   await page.screenshot({path:`${evidence}/loading-${width}-${path.split('/').filter(Boolean).join('-')}-before.png`});
   release();await page.waitForLoadState('load');await settle();const after=await page.evaluate(geometry);
   for(const [selector,rect] of Object.entries(before.boxes))if(rect){
    const next=after.boxes[selector];
    if(!next||['x','y','width','height'].some(key=>Math.abs(rect[key]-next[key])>1))throw Error('bundle initialization moved '+selector+': '+JSON.stringify({width,path,before:rect,after:next}));
   }
   if(await page.locator('[data-sidebar-toggle],[data-sidebar-close]').evaluateAll(buttons=>buttons.some(button=>button.disabled)))throw Error('initialized navigation remained disabled');
   const toggle=page.locator(width<=760?'.sidebar-open':'.brand-row [data-sidebar-toggle]');
   await toggle.click();
   await page.waitForFunction(({mobile,collapsed})=>mobile?document.documentElement.classList.contains('mobile-nav'):document.documentElement.classList.contains('sidebar-collapsed')!==collapsed,{mobile:width<=760,collapsed});
   await page.locator('.brand-row [data-sidebar-toggle]').click();
   await page.waitForFunction(({mobile,collapsed})=>mobile?!document.documentElement.classList.contains('mobile-nav'):document.documentElement.classList.contains('sidebar-collapsed')===collapsed,{mobile:width<=760,collapsed});
   measurements.loading.push({width,path,theme,collapsed,before,after});
   await page.screenshot({path:`${evidence}/loading-${width}-${path.split('/').filter(Boolean).join('-')}-after.png`});
  }finally{release();await page.unroute('**/app.js')}
 }
 // A failed bundle download must reveal the no-script form/navigation fallback.
 await page.route('**/app.js',route=>route.abort());await page.goto(url+'/settings/preferences');await page.unroute('**/app.js');
 if(!await page.locator('.app-sidebar').isVisible()||!await page.locator('.preference-save').isVisible()||!await page.locator('#interface-language').isVisible())throw Error('bundle failure left native navigation or form inaccessible');
 await page.setViewportSize({width:1400,height:980});await page.goto(url+'/settings/notifications');
 const target=page.locator('#notification-target');
 const notification=await page.locator('.notification-preference .setting-row').evaluate(row=>{const key=row.querySelector('label').getBoundingClientRect(),value=row.querySelector('.control-select-trigger').getBoundingClientRect(),bounds=row.getBoundingClientRect();return {key:key.y+key.height/2,value:value.y+value.height/2,right:value.right,rowRight:bounds.right}});
 if(Math.abs(notification.key-notification.value)>1||Math.abs(notification.right-notification.rowRight)>1)throw Error('notification key and value split into separate rows: '+JSON.stringify(notification));
 if(await page.getByRole('button',{name:'Save notification channel',exact:true}).isVisible())throw Error('enhanced notifications still needs Save');
 for(const value of ['tui:','auto:']){
  await Promise.all([page.waitForResponse(response=>response.url().endsWith('/notification')&&response.request().method()==='POST'&&response.status()===200),selectValue(page,target,value)]);
  await page.waitForFunction(()=>document.querySelector('[data-notification-feedback]').hidden);
  await page.reload();if(await target.inputValue()!==value)throw Error('notification autosave was lost');
 }
 await page.screenshot({path:evidence+'/notifications-saved.png'});
 for(const theme of ['light','dark'])for(const width of [1400,390]){
  await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
  await page.setViewportSize({width,height:980});
  await page.goto(url+'/settings/preferences');await settle();
  await page.getByRole('combobox',{name:'Language',exact:true}).waitFor();
  const geometry=await page.locator('.settings-group').evaluate(group=>{
   const left=node=>node.getBoundingClientRect().left;
   return {title:left(document.querySelector('h1')),language:left(group.querySelector('.preference-name label')),appearance:left(group.querySelector('[data-appearance-setting] label')),border:getComputedStyle(group).borderWidth,scrollWidth:document.documentElement.scrollWidth};
  });
  if(geometry.border!=='0px'||geometry.scrollWidth>width||Math.max(geometry.title,geometry.language,geometry.appearance)-Math.min(geometry.title,geometry.language,geometry.appearance)>1)throw Error('Settings lost their flat, aligned layout: '+JSON.stringify(geometry));
  if(await page.locator('.settings-page').getByRole('heading').count()!==1)throw Error('Preferences gained a redundant section heading');
  measurements.settings.push({theme,width,...geometry});
  await page.screenshot({path:`${evidence}/simple-preferences-${theme}-${width}.png`});
  await page.goto(url+'/settings/capabilities');await settle();
  const entries=page.locator('.settings-group .object-row');
  if(await entries.count()!==5||await page.locator('.settings-group').evaluate(node=>parseFloat(getComputedStyle(node).borderWidth))!==0)throw Error('Capability directory lost entries or acquired an outer frame');
  await page.screenshot({path:`${evidence}/simple-capabilities-${theme}-${width}.png`});
  await page.locator('a[href="/settings/capabilities/memory"]').click();
  await page.getByRole('button',{name:'Help: Memory',exact:true}).click();
  await page.getByRole('tooltip').waitFor();await page.keyboard.press('Escape');
 }
 for(const theme of ['light','dark']){
  await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
  await page.goto(url+'/');await settle();
  if(await page.locator('#workspace-status .sr-only').innerText()!=='Ready'||await page.locator('#workspace-status .status-pill').count()!==0||await page.locator('#workspace-status [data-can-send]').getAttribute('data-can-send')!=='true')throw Error('Normal readiness lost its accessible text or send availability');
  for(const width of [1400,390]){
   await page.setViewportSize({width,height:980});
   const text=page.locator('.composer textarea');await text.click();
   for(const input of ['pointer','keyboard']){
    if(input==='keyboard'){await page.keyboard.press('Tab');await page.keyboard.press('Shift+Tab')}
    const focus=await text.evaluate(node=>{
     const composer=node.closest('.composer'),style=getComputedStyle(composer),inner=getComputedStyle(node);
     return {active:node===document.activeElement,outline:parseFloat(style.outlineWidth),border:style.borderColor,background:style.backgroundColor,shadow:style.boxShadow,innerBorder:parseFloat(inner.borderWidth),innerOutline:parseFloat(inner.outlineWidth),innerShadow:inner.boxShadow};
    });
    if(!focus.active||focus.outline!==0||focus.border===focus.background||focus.shadow!=='none'||focus.innerBorder!==0||focus.innerOutline!==0||focus.innerShadow!=='none')throw Error('composer focus added an outer frame or lost its indicator: '+JSON.stringify(focus));
    measurements.focus.push({theme,width,input,...focus});
   }
   await page.locator('.composer').screenshot({path:`${evidence}/composer-focus-${theme}-${width}.png`});
  }
 }
 await writeFile(evidence+'/page-transitions.json',JSON.stringify({...measurements,notificationAutosave:true,bundleFailureFallback:true},null,2));
}

export async function capabilityDefaults(page,context,url,evidence){
 await page.goto(url+'/');
 await page.evaluate(()=>document.fonts.ready);
 const primary=page.getByRole('navigation',{name:'Workspace',exact:true});
 const primaryLinks=await primary.getByRole('link').evaluateAll(links=>links.map(link=>({href:link.getAttribute('href'),top:link.getBoundingClientRect().top,bottom:link.getBoundingClientRect().bottom})));
 if(primaryLinks.map(link=>link.href).join(',')!=='/,/tasks,/developer/observability,/settings/preferences'||await page.evaluate(()=>innerHeight)-primaryLinks[3].bottom>32)throw Error('primary navigation lost module order or bottom Settings entry');
 await primary.getByRole('link',{name:'Settings',exact:true}).click();
 const stableLinks=await primary.getByRole('link').evaluateAll(links=>links.map(link=>link.getBoundingClientRect().top));
 if(stableLinks.some((top,i)=>Math.abs(top-primaryLinks[i].top)>1))throw Error('opening Settings moved the primary navigation');
 const rails=await page.evaluate(()=>{const primary=document.querySelector('.app-sidebar').getBoundingClientRect(),secondary=document.querySelector('.section-sidebar').getBoundingClientRect(),main=document.querySelector('.workspace-main').getBoundingClientRect();return {primary:primary.right,secondaryLeft:secondary.left,secondaryRight:secondary.right,mainLeft:main.left}});
 if(rails.primary!==rails.secondaryLeft||rails.secondaryRight!==rails.mainLeft)throw Error('secondary navigation replaced or overlaps the primary rail/content');
 await primary.getByRole('link',{name:'Developer',exact:true}).click();
 await page.getByRole('navigation',{name:'Developer sections'}).getByRole('link',{name:'Memory',exact:true}).click();
 await page.screenshot({path:evidence+'/developer-navigation.png',fullPage:true});
 await primary.getByRole('link',{name:'Tasks',exact:true}).click();
 if(await page.getByRole('navigation',{name:'Task sections',exact:true}).getByRole('link').count()!==2)throw Error('Tasks lost its execution/configuration categories');
 await primary.getByRole('link',{name:'Settings',exact:true}).click();
 // Explicit collapse can be reversed without leaving a settings page.
 await page.locator('.brand-row').getByRole('button',{name:'Close navigation',exact:true}).click();
 await page.getByRole('button',{name:'Open navigation',exact:true}).click();
 if(!await page.locator('.section-sidebar').isVisible())throw Error('collapsed settings navigation could not be reopened');
 // Low-frequency presentation preferences live in Preferences, not the rail.
 if(await page.locator('.app-sidebar').innerText().then(text=>text.includes('Your personal AI steward.'))||await page.locator('[data-theme-toggle],[data-avatar-pause]').count())throw Error('sidebar retained duplicate presentation controls');
 const motion=page.getByRole('switch',{name:'Avatar motion',exact:true});await motion.uncheck();
 await primary.getByRole('link',{name:'Chat',exact:true}).click();
 const avatar=page.locator('.steward-avatar').first();await avatar.waitFor();
 await page.waitForFunction(()=>document.querySelector('.steward-avatar')?.dataset.renderer==='prerendered');
 if(await avatar.getAttribute('data-frame')!=='0'||await avatar.locator('.ghost-art').evaluate(node=>getComputedStyle(node).animationPlayState)!=='paused')throw Error('saved avatar preference did not stop motion in Chat');
 await primary.getByRole('link',{name:'Settings',exact:true}).click();
 if(await motion.isChecked())throw Error('avatar preference did not persist across pages');
 await page.reload();if(await motion.isChecked())throw Error('avatar preference did not persist on reload');
 await motion.check();
 // System reduced motion takes precedence over the local animation setting.
 const quiet=await page.context().browser().newContext({reducedMotion:'reduce'});
 try{
  await quiet.addCookies(await context.cookies());const p=await quiet.newPage();await p.goto(url+'/settings/preferences');
  const control=p.getByRole('switch',{name:'Avatar motion',exact:true});await control.waitFor();
  if(!await control.isDisabled()||await control.isChecked())throw Error('reduced motion preference was ignored');
  await p.getByText('Follows your system’s reduced motion preference.',{exact:true}).waitFor();
  await p.emulateMedia({reducedMotion:'no-preference'});
  await p.waitForFunction(()=>{const input=document.querySelector('[data-avatar-motion]');return !input.disabled&&input.checked});
  await control.uncheck();
  await p.emulateMedia({reducedMotion:'reduce'});
  await p.waitForFunction(()=>document.querySelector('[data-avatar-motion]').disabled);
  await p.emulateMedia({reducedMotion:'no-preference'});
  await p.waitForFunction(()=>{const input=document.querySelector('[data-avatar-motion]');return !input.disabled&&!input.checked});
  if(await p.evaluate(()=>localStorage.getItem('steward-avatar-motion'))!=='paused')throw Error('system motion changes overwrote the saved user preference');
 }finally{await quiet.close()}
 // A two-option language list uses native Select without a search field.
 const language=page.locator('[data-language-trigger]');
 const languagePopup=page.locator('[data-language-popup]');
 await language.click();await languagePopup.waitFor();
 if(await languagePopup.locator('input').count()||await languagePopup.getByRole('option').count()!==2)throw Error('short language list gained search or lost an option');
 await page.screenshot({path:evidence+'/language-dropdown.png',fullPage:true});
 await page.keyboard.press('End');await page.keyboard.press('Enter');
 await page.waitForURL(u=>u.searchParams.get('language')==='zh-CN');await page.waitForLoadState();
 await page.goto(url+'/settings/preferences');await language.waitFor();
 await page.screenshot({path:evidence+'/preferences.png',fullPage:true});
 await language.press('Enter');await page.waitForFunction(()=>document.activeElement?.getAttribute('role')==='option');await page.keyboard.press('ArrowUp');await page.keyboard.press('Escape');await languagePopup.waitFor({state:'hidden'});await language.and(page.locator(':focus')).waitFor();
 if(!await language.evaluate(node=>node===document.activeElement)||await languagePopup.isVisible())throw Error('Escape lost dropdown focus');
 await language.press('Enter');await page.waitForFunction(()=>document.activeElement?.getAttribute('role')==='option');await page.keyboard.press('Tab');await languagePopup.waitFor({state:'hidden'});
 if(!await page.locator('#interface-theme-picker').evaluate(node=>node===document.activeElement)||await languagePopup.isVisible())throw Error('Tab did not continue to the next setting');
 await language.click();await languagePopup.waitFor();
 await page.waitForFunction(()=>!document.querySelector('[data-language-popup]')?.hasAttribute('data-starting-style'));
 const heading=await page.getByRole('heading',{level:1}).boundingBox();await page.mouse.click(heading.x+heading.width/2,heading.y+heading.height/2);await languagePopup.waitFor({state:'hidden'});
 if(await languagePopup.isVisible())throw Error('outside click kept dropdown open');
 const popup=page.locator('.control-select-menu:visible');
 if(await page.locator('.section-sidebar input[type=search]').count()||await page.locator('.settings-navigation a:visible').count()!==5)throw Error('Settings should expose all five categories directly');
 await navigationViewports(page,evidence);
 await selectValue(page,page.locator('#interface-theme'),'dark');
 if(!await page.locator('html').evaluate(node=>node.classList.contains('dark')))throw Error('appearance did not apply');
 await page.reload();await page.locator('#interface-theme-picker').waitFor();
 if(await page.locator('#interface-theme').inputValue()!=='dark')throw Error('appearance did not persist');
 await page.screenshot({path:evidence+'/preferences-dark.png',fullPage:true});
 await selectValue(page,page.locator('#interface-theme'),'light');
 await page.goto(url+'/settings/notifications');await page.screenshot({path:evidence+'/notifications.png',fullPage:true});
 await page.goto(url+'/settings/models');await page.screenshot({path:evidence+'/models.png',fullPage:true});
 await page.goto(url+'/settings/models/new?provider=compatible');await page.screenshot({path:evidence+'/model-inputs.png',fullPage:true});
 await page.goto(url+'/settings/capabilities');
 if(await page.locator('.object-row').count()!==5)throw Error('duplicate Memory search entry');
 const memory=page.locator('.object-row[href="/settings/capabilities/memory"]');
 await memory.getByText('已启用',{exact:true}).waitFor();
 await page.screenshot({path:evidence+'/capabilities-default.png',fullPage:true});
 await memory.click();
 await page.getByText('已启用',{exact:true}).waitFor();
 if(!await page.locator('section.settings-group').isVisible()||await page.locator('[data-capability-fields]').isVisible())throw Error('default mode exposed custom configuration');
 await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/capability/configure'&&r.request().method()==='POST'),selectValue(page,page.locator('#capability-mode'),'custom')]);
 await page.locator('[data-capability-draft]').filter({hasText:'已保存。'}).waitFor();
 if(!await page.locator('[data-capability-fields]').isVisible())throw Error('custom dropdown did not reveal inputs');
 await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/capability/configure'&&r.request().method()==='POST'),selectValue(page,page.locator('#capability-mode'),'automatic')]);
 await page.locator('[data-capability-draft]').filter({hasText:'已保存。'}).waitFor();
 await page.goto(url+'/settings/capabilities/memory');await page.locator('[data-capability-fields][hidden]').waitFor({state:'hidden'});
 await page.screenshot({path:evidence+'/memory-default.png',fullPage:true});
 await page.setViewportSize({width:390,height:844});
 if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Memory settings overflowed on mobile');
 await page.screenshot({path:evidence+'/memory-mobile.png',fullPage:true});
 await page.getByRole('button',{name:'打开导航',exact:true}).click();
 const bothRails=await page.evaluate(()=>{const first=document.querySelector('.app-sidebar').getBoundingClientRect(),second=document.querySelector('.section-sidebar').getBoundingClientRect();return first.left>=0&&first.right<=second.left&&second.right<=innerWidth});
 if(!bothRails||!await page.locator('.app-sidebar [aria-current="page"]').isVisible()||!await page.locator('.settings-navigation [aria-current="page"]').isVisible())throw Error('mobile navigation did not preserve both levels');
 const last=page.locator('.settings-navigation a').last();await last.focus();await page.keyboard.press('Tab');
 if(!await page.locator('.app-sidebar .brand').evaluate(node=>node===document.activeElement))throw Error('two-level drawer did not wrap keyboard focus');
 await page.screenshot({path:evidence+'/settings-navigation-mobile.png',fullPage:true});
 await page.keyboard.press('Escape');
 if(!await page.getByRole('button',{name:'打开导航',exact:true}).evaluate(node=>node===document.activeElement))throw Error('mobile settings navigation lost focus');
 // All settings remain navigable and submit real forms without the bundle.
 const plain=await page.context().browser().newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});
 try{
  await plain.addCookies(await context.cookies());const p=await plain.newPage();await p.goto(url+'/settings/preferences');
  await p.locator('#interface-language').selectOption('en');await p.getByRole('button',{name:'保存',exact:true}).click();
  if(await p.locator('html').getAttribute('lang')!=='en')throw Error('native language fallback did not save');
  await p.screenshot({path:evidence+'/settings-no-script.png',fullPage:true});
 }finally{await plain.close()}
}

export async function capabilitySetup(page,context,url,evidence){
 const facts=JSON.parse(process.env.STEWARD_CAPABILITY_SETUP_FACTS);
 const guided=()=>page.locator('form.capability-guided');
 const status=()=>guided().locator('[data-capability-draft]');
 const traffic=[];
 page.on('request',r=>{if(new URL(r.url()).pathname==='/capability/configure'&&r.method()==='POST')traffic.push({event:'request',kind:new URLSearchParams(r.postData()).get('capability'),time:Date.now()})});
 page.on('response',r=>{if(new URL(r.url()).pathname==='/capability/configure')traffic.push({event:'response',status:r.status(),time:Date.now()})});
 const response=()=>page.waitForResponse(r=>new URL(r.url()).pathname==='/capability/configure'&&r.request().method()==='POST');
 const open=async id=>{await page.goto(url+'/settings/capabilities/'+id);if(await page.locator('[name=configuration],[data-capability-import]').count()||/YAML|config\.ya?ml|Download example/.test(await page.locator('.capabilities-page').innerText()))throw Error('implementation document leaked into capability settings')};
 const fill=async fields=>{
  await guided().evaluate((form,fields)=>{for(const [name,value]of Object.entries(fields)){const control=form.elements.namedItem(name);if(!control)throw Error('missing field '+name);control.value=String(value)}form.dispatchEvent(new Event('input',{bubbles:true}))},fields);
 };
 const wait=async(error)=>{await status().filter({hasText:error??'Saved.'}).waitFor()};
 const apply=async(fields,error)=>{
  try{await Promise.all([response(),fill(fields)]);await wait(error)}catch(e){
   const state=await guided().evaluate(form=>({invalid:Array.from(form.elements).filter(c=>c.validity&&!c.validity.valid).map(c=>c.name),disabled:Array.from(form.elements).filter(c=>c.disabled).map(c=>c.name),status:form.querySelector('[data-capability-draft]')?.textContent}));
   await writeFile(evidence+'/capability-requests.json',JSON.stringify({traffic,state},null,2));throw e;
  }
 };
 const custom=async()=>{await selectValue(page,guided().getByLabel('Configuration mode',{exact:true}),'custom')};
 await page.goto(url+'/settings/capabilities');
 if(await page.locator('.object-row').count()!==5||await page.locator('.settings-footer').count())throw Error('capability navigation or footer regressed');
 await open('skills');
 if(await guided().getByRole('button',{name:'Save',exact:true}).count())throw Error('enhanced capability form still requires Save');
 for(const mode of ['automatic','off']){
  await Promise.all([response(),selectValue(page,guided().getByLabel('Configuration mode',{exact:true}),mode)]);await wait();await page.reload();
  if(await (await selectParts(guided().getByLabel('Configuration mode',{exact:true}))).native.inputValue()!==mode)throw Error('automatic mode/opt-out did not persist');
 }
 await open('memory');
 const revision=await guided().locator('[name=revision]').inputValue();
 await custom();await apply({key_ref:'env:STEWARD_MISSING_SETUP_KEY'},'Memory key unavailable');
 if(await guided().locator('[name=revision]').inputValue()!==revision)throw Error('failed automatic save changed the file revision');
 await page.screenshot({path:evidence+'/memory-guided-failed.png',fullPage:true});
 await apply({key_ref:'env:STEWARD_SETUP_MEMORY_KEY'});
 if(await guided().locator('[name=revision]').inputValue()===revision)throw Error('automatic save did not commit');
 await page.screenshot({path:evidence+'/memory-guided-saved.png',fullPage:true});
 await open('embedding');await custom();
 await apply({endpoint:facts.embedding,model:'semantic-fixture-v1',dimensions:2},'Embedding connection failed');
 await apply({dimensions:5});
 await open('skills');await custom();await apply({root:facts.skills+'/missing',scope:'agent'},'Skill catalog unavailable');await apply({root:facts.skills});
 await open('mcp');await guided().getByRole('switch',{name:'Enabled',exact:true}).check();
 await apply({'provider.0.name':'owner_catalog','provider.0.command':facts.binary,'provider.0.directory':facts.provider,'provider.0.args':'-test-mcp-server\n'+facts.provider,'provider.0.tool.0.name':'not_advertised'},'MCP discovery failed');
 await Promise.all([page.waitForNavigation(),guided().getByRole('button',{name:'Add allowed tool',exact:true}).click()]);
 await apply({'provider.0.tool.0.name':'read','provider.0.tool.1.name':'send','provider.0.tool.1.permission':'external_effect'});
 await page.reload();
 await Promise.all([page.waitForNavigation(),guided().locator('button[value="remove-tool-0-1"]').click()]);
 if(await guided().locator('[name="provider.0.tool_count"]').inputValue()!=='1')throw Error('tool removal did not persist');
 await Promise.all([page.waitForNavigation(),guided().getByRole('button',{name:'Add allowed tool',exact:true}).click()]);
 await apply({'provider.0.tool.1.name':'send','provider.0.tool.1.permission':'external_effect'});
 await page.reload();
 if(await guided().locator('[name="provider.0.tool.1.permission"]').inputValue()!=='external_effect')throw Error('tool permission did not persist');
 await page.screenshot({path:evidence+'/mcp-guided.png',fullPage:true});
 // Native forms validate and save field values without exposing serialization.
 const plain=await page.context().browser().newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});
 try{
  await plain.addCookies(await context.cookies());const p=await plain.newPage();await p.goto(url+'/settings/capabilities/embedding');
  await p.locator('.capability-guided').getByRole('button',{name:'Save',exact:true}).click();
  if(await p.locator('.feedback.error').count()||await p.locator('[name=configuration]').count())throw Error('native field save failed');
  await p.screenshot({path:evidence+'/capability-no-script.png',fullPage:true});
 }finally{await plain.close()}
 await open('execution');await guided().getByRole('switch').check();await apply({root:facts.workspace,toolchains:''});
 await Promise.all([response(),guided().getByRole('switch').uncheck()]);await wait();
 await open('computer');await custom();
 const c=facts.computer.computer,j=facts.computer.javascript;
 await apply({root:c.root,endpoint:c.endpoint,provider_version:c.compatibility.provider_version,guest_build:c.compatibility.guest_build,driver_version:c.compatibility.driver_version,driver_sha256:c.compatibility.driver_sha256,driver_team:c.compatibility.driver_team,driver_bundle:c.compatibility.driver_bundle,architecture:c.compatibility.architecture,guest_user:c.guest.user,guest_port:c.guest.port,password_ref:c.guest.password_ref,host_key:c.guest.host_key_sha256,manifest_version:c.manifest.version,node:j.command,script:j.script});
 // Every capability ships at compact and desktop widths in both themes.
 const geometry=[];
 for(const theme of ['light','dark']){
  await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:980});
   for(const id of ['memory','embedding','mcp','skills','execution','computer']){
    await open(id);await page.evaluate(()=>document.fonts.ready);
    if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('capability overflow '+id+' '+width);
    if(id==='mcp'||id==='execution'){
     const row=await guided().locator('.setting-row').first().evaluate(node=>{const label=node.querySelector('label').getBoundingClientRect(),control=node.querySelector('input').getBoundingClientRect(),bounds=node.getBoundingClientRect();return {labelBottom:label.bottom,controlBottom:control.bottom,labelTop:label.top,controlTop:control.top,labelRight:label.right,controlLeft:control.left,height:bounds.height}});
     if(row.height>90||row.labelRight>row.controlLeft||Math.max(row.labelTop,row.controlTop)>=Math.min(row.labelBottom,row.controlBottom))throw Error('boolean field is not a compact key/value row');
     geometry.push({theme,width,id,...row});
    }
    await page.screenshot({path:`${evidence}/capability-${id}-${theme}-${width}.png`,fullPage:true});
   }
  }
 }
 await page.goto(url+'/settings/capabilities');
 if(await page.getByText('Restart required',{exact:true}).count()!==5)throw Error('saved state was confused with active runtime');
 await writeFile(evidence+'/capability-geometry.json',JSON.stringify(geometry,null,2));
}

export async function settingsAccount(page,context,url,evidence){
 await page.goto(url+'/settings/models');

 let posts=0;page.on('request',request=>{if(new URL(request.url()).pathname==='/auth/logout'&&request.method()==='POST')posts++});
 const trigger=page.getByRole('button',{name:/^Manage connection:/}).first();
 const logout=async()=>{await trigger.click();await page.getByRole('menuitem',{name:'Sign out of provider',exact:true}).click()};
 await logout();
 const dialog=page.getByRole('dialog',{name:'Sign out of provider',exact:true});await dialog.waitFor();
 if(posts!==0||!await dialog.getByRole('button',{name:'Cancel',exact:true}).evaluate(node=>node===document.activeElement))throw Error('sign-out confirmation submitted early or lost safe focus');
 const centered=await dialog.evaluate(node=>{const rect=node.getBoundingClientRect();return Math.abs(rect.x+rect.width/2-innerWidth/2)<2&&Math.abs(rect.y+rect.height/2-innerHeight/2)<2});
 if(!centered)throw Error('confirmation dialog is outside the center of the viewport');
 await page.screenshot({path:evidence+'/signout-confirmation.png',fullPage:true});
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
 await trigger.and(page.locator(':focus')).waitFor();
 if(posts!==0||!await trigger.evaluate(node=>node===document.activeElement))throw Error('cancel changed account or lost focus');
 await logout();await page.keyboard.press('Escape');
 if(posts!==0||await dialog.count())throw Error('Escape changed account');
 await logout();
 await Promise.all([page.waitForNavigation({waitUntil:'domcontentloaded'}),dialog.getByRole('button',{name:'Sign out of provider',exact:true}).click()]);
 if(posts!==1)throw Error('confirmed sign-out was submitted more than once');
}

export async function taskNavigation(page,url,evidence){
 const context=page.context(),measurements=[];
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 await page.setViewportSize({width:1440,height:1000});
 const settle=async()=>{await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))};
 for(const route of ['/settings/preferences','/tasks?view=all','/tasks/configurations']){
  await page.goto(url+route);await settle();
  const geometry=await page.evaluate(()=>{const rect=selector=>{const r=document.querySelector(selector).getBoundingClientRect();return {left:r.left,right:r.right,width:r.width}};return {primary:rect('.app-sidebar'),secondary:rect('.section-sidebar'),main:rect('.workspace-main')}});
  if(Math.abs(geometry.primary.right-geometry.secondary.left)>1||Math.abs(geometry.secondary.right-geometry.main.left)>1)throw Error('Task rails overlap content: '+JSON.stringify({route,geometry}));
  measurements.push({route,...geometry});
 }
 if(measurements.some(item=>item.primary.width!==measurements[0].primary.width||item.secondary.width!==measurements[0].secondary.width))throw Error('Tasks did not reuse Settings/Developer rail dimensions');
 await page.goto(url+'/tasks?view=all');const execution=await page.locator('.task-list-title[href^="/tasks/executions/"]').first().getAttribute('href');
 await page.goto(url+'/tasks/configurations');
 if(await page.locator('.primary-settings svg').innerHTML()===await page.locator('.section-navigation a[href="/tasks/configurations"] svg').innerHTML())throw Error('Task configuration reuses the Settings icon');
 const configuration=await page.locator('.task-list-title[href^="/tasks/task:"]').first().getAttribute('href');
 for(const [route,selected] of [[execution,'Execution records'],[configuration,'Configurations'],['/tasks/schedules/new','Configurations'],['/tasks/attention','Execution records']]){
  await page.goto(url+route);await page.getByRole('navigation',{name:'Task sections',exact:true}).getByRole('link',{name:selected,exact:true}).and(page.locator('[aria-current=page]')).waitFor();
 }
 for(const theme of ['light','dark']){
  await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
  for(const width of [1440,761,390,320]){
   await page.setViewportSize({width,height:1000});
   for(const [route,label]of [['/tasks?view=all','Execution records'],['/tasks/configurations','Configurations']]){
    await page.goto(url+route);await settle();if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Task shell overflowed at '+width);
    if(await page.locator('.tasks-page .section-tabs').count())throw Error('Task categories remain duplicated inside the page');
    await page.getByRole('heading',{name:label,exact:true}).waitFor();await assertDataList(page);
    if(width<=760){await page.locator('.sidebar-open').click();await page.getByRole('navigation',{name:'Task sections',exact:true}).getByRole('link',{name:label,exact:true}).and(page.locator('[aria-current=page]')).waitFor();await page.screenshot({path:`${evidence}/task-rail-${label.split(' ')[0]}-${theme}-${width}.png`});await page.keyboard.press('Escape');if(!await page.locator('.sidebar-open').evaluate(node=>node===document.activeElement)||await page.locator('#workspace-main').evaluate(node=>node.inert))throw Error('Task drawer lost focus or kept content inert')}
    else await page.screenshot({path:`${evidence}/task-rail-${label.split(' ')[0]}-${theme}-${width}.png`});
   }
  }
 }
 await context.request.post(url+'/settings/language',{form:{language:'zh-CN'}});await page.goto(url+'/tasks/configurations');await page.locator('.sidebar-open').click();await page.getByRole('navigation',{name:'任务分类',exact:true}).getByRole('link',{name:'任务配置',exact:true}).and(page.locator('[aria-current=page]')).waitFor();await page.keyboard.press('Escape');
 await page.setViewportSize({width:1440,height:1000});await page.goto(url+'/tasks?view=all');await page.locator('.brand-row .sidebar-toggle').click();await page.goto(url+'/tasks/configurations');if(!await page.evaluate(()=>document.documentElement.classList.contains('sidebar-collapsed'))||!await page.locator('.section-sidebar').isVisible())throw Error('Task category navigation lost collapse preference');await page.locator('.brand-row .sidebar-toggle').click();await page.getByRole('navigation',{name:'任务分类',exact:true}).getByRole('link',{name:'任务配置',exact:true}).and(page.locator('[aria-current=page]')).waitFor();
 const plain=await context.browser().newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});try{await plain.addCookies(await context.cookies());const p=await plain.newPage();await p.goto(url+'/tasks');await p.getByRole('navigation',{name:'任务分类',exact:true}).getByRole('link',{name:'任务配置',exact:true}).click();await p.waitForURL('**/tasks/configurations');if(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('native Task rails overflowed')}finally{await plain.close()}
 await context.request.post(url+'/settings/language',{form:{language:'en'}});await page.goto(url+'/tasks?view=all');
 await writeFile(evidence+'/task-navigation.json',JSON.stringify({sharedRails:measurements,categoryDetails:true,mobileFocus:true,collapse:true,native:true},null,2));
}

export async function settingsConsistency(page,context,url,evidence){
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 const choose=async value=>{
  await Promise.all([page.waitForResponse(r=>r.url()===url+'/notification'&&r.request().method()==='POST'),selectValue(page,page.locator('#notification-target'),value)]);
 };
 let posts=0;page.on('request',r=>{if(r.url()===url+'/notification'&&r.method()==='POST')posts++});
 await page.goto(url+'/settings/notifications');
 await choose('tui:');await page.waitForFunction(()=>document.querySelector('[data-notification-feedback]').hidden);
 await choose('auto:');await page.waitForFunction(()=>document.querySelector('[data-notification-feedback]').hidden);
 if(posts!==2||await page.getByRole('button',{name:'Save notification channel',exact:true}).isVisible())throw Error('autosave is not single-flight or exposes Save');
 await page.reload();if(await page.locator('#notification-target').inputValue()!=='auto:')throw Error('autosave revision was not advanced');
 // A competing tab wins its revision. A stale candidate never overwrites it.
 const revision=await page.locator('[name=version]').inputValue();
 await context.request.post(url+'/notification',{headers:{Accept:'application/json'},form:{target:'tui:',version:revision}});
 await choose('tui:');await page.locator('[data-notification-feedback][role=alert]').waitFor();
 if(!await page.locator('#notification-target-picker').isDisabled())throw Error('CAS conflict allows a second write');
 await page.locator('[data-notification-refresh]').click();if(await page.locator('#notification-target').inputValue()!=='tui:')throw Error('conflict readback lost canonical choice');
 // A lost acknowledgement may have committed. Refresh reads it without replay.
 const beforeLost=posts;
 await page.route('**/notification',async route=>{await route.fetch();await route.abort()});
 await selectValue(page,page.locator('#notification-target'),'auto:');await page.locator('[data-notification-feedback][role=alert]').waitFor();
 await page.unroute('**/notification');await page.locator('[data-notification-refresh]').click();
 if(posts!==beforeLost+1||await page.locator('#notification-target').inputValue()!=='auto:')throw Error('lost acknowledgement replayed or hid persisted choice');
 // No script retains the native save path.
 const plain=await context.browser().newContext({javaScriptEnabled:false,viewport:{width:390,height:844}});
 try{
  await plain.addCookies(await context.cookies());const native=await plain.newPage();await native.goto(url+'/settings/notifications');await native.locator('#notification-target').selectOption('tui:');await native.getByRole('button',{name:'Save notification channel',exact:true}).click();await native.reload();if(await native.locator('#notification-target').inputValue()!=='tui:')throw Error('native preference save lost');
  await native.goto(url+'/tasks');await native.locator('[data-task-filter-fallback]>summary').click();await native.locator('select[name=view]').selectOption('closed');await native.locator('[name=needs_me]').check();await native.getByRole('button',{name:'Apply filters',exact:true}).click();if(new URL(native.url()).searchParams.get('needs_me')!=='1')throw Error('native filter lost attention');
 }finally{await plain.close()}
 await page.goto(url+'/tasks');
 if(await page.locator('.task-status-filters').count()||await page.getByRole('link',{name:'All tasks',exact:true}).count())throw Error('permanent filters remain');
 await page.getByRole('button',{name:'Filters',exact:true}).press('Enter');
 await page.getByRole('menuitemradio',{name:'All statuses',exact:true}).and(page.locator('[aria-checked=true]')).waitFor();
 await page.getByRole('menuitemradio',{name:'Finished',exact:true}).click();await page.waitForURL(u=>u.searchParams.get('view')==='closed');
 await page.getByRole('button',{name:'Filters',exact:true}).click();await page.getByRole('menuitemcheckbox',{name:'Needs me',exact:true}).click();await page.waitForURL(u=>u.searchParams.get('needs_me')==='1'&&u.searchParams.get('view')==='closed');
 await page.locator('#task-search').fill('report');await page.getByRole('button',{name:'Search',exact:true}).click();await page.waitForURL(u=>u.searchParams.get('q')==='report');
 if(new URL(page.url()).searchParams.get('needs_me')!=='1'||new URL(page.url()).searchParams.get('view')!=='closed')throw Error('search discarded filters');
 const measurements=[];
 for(const theme of ['light','dark']){
  await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
  for(const width of [1400,390,320]){
   await page.setViewportSize({width,height:980});
   for(const path of ['/settings/preferences','/settings/models','/settings/apps/service','/settings/notifications','/tasks']){
    await page.goto(url+path);await page.evaluate(()=>document.fonts.ready);
    if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('settings overflow: '+path+' '+width);
    if(path==='/settings/preferences'){
     const bounds=await page.locator('#interface-language-picker,#interface-theme-picker').evaluateAll(nodes=>nodes.map(n=>{const r=n.getBoundingClientRect();return {width:r.width,right:r.right}}));
     if(bounds.length!==2||Math.abs(bounds[0].width-bounds[1].width)>1||Math.abs(bounds[0].right-bounds[1].right)>1)throw Error('sibling preference controls differ: '+JSON.stringify(bounds));
     measurements.push({theme,width,bounds});
    }
    if(path==='/settings/models'){
     if(await page.getByRole('button',{name:'Test connection',exact:true}).count())throw Error('manual connection test remains visible');
     if(await page.getByRole('link',{name:'App settings',exact:true}).count()||await page.getByRole('heading',{name:'Models',exact:true}).count()!==1)throw Error('Models mixes application service configuration');
     const commands=await page.locator('.model-connections>.list-heading>.setting-action').evaluateAll(nodes=>nodes.map(n=>{const r=n.getBoundingClientRect(),row=n.closest('.list-heading').getBoundingClientRect();return {width:r.width,right:r.right,rowRight:row.right}}));
     if(commands.length!==2||new Set(commands.map(n=>Math.round(n.width))).size!==1||commands.some(n=>Math.abs(n.right-n.rowRight)>1))throw Error('model connection commands misaligned: '+JSON.stringify({width,commands}));
    }
    if(path==='/settings/apps/service'){
     const row=await page.locator('.service-configuration .setting-row').evaluate(n=>{const key=n.querySelector('label').getBoundingClientRect(),value=n.querySelector('input').getBoundingClientRect(),button=n.closest('section').querySelector('button[type=submit]').getBoundingClientRect();return {key:key.y+key.height/2,value:value.y+value.height/2,right:value.right,buttonRight:button.right}});
     if(Math.abs(row.key-row.value)>1||Math.abs(row.right-row.buttonRight)>1||await page.locator('.service-configuration.panel').count())throw Error('app service is not a compact key-value form: '+JSON.stringify(row));
     await page.locator('.service-advanced>summary').click();
     if(await page.locator('.service-advanced textarea').count())throw Error('app service exposes a serialized configuration editor');
     const fields=await page.locator('.service-advanced fieldset>label').evaluateAll(nodes=>nodes.map(n=>getComputedStyle(n).display));
     if(!fields.length||fields.some(display=>display!=='grid'))throw Error('advanced app settings do not use key/value rows');

    }
    if(path==='/tasks'){await page.getByRole('button',{name:'Filters',exact:true}).click();const menu=await page.getByRole('menu').boundingBox();if(menu.x<0||menu.x+menu.width>width+1)throw Error('filter popup outside viewport')}
    if(width!==320)await page.screenshot({path:`${evidence}/consistency-${path.split('/').filter(Boolean).join('-')}-${theme}-${width}.png`,fullPage:true});
   }
  }
 }
 await writeFile(evidence+'/settings-consistency.json',JSON.stringify({autosave:true,cas:true,lostAcknowledgement:true,nativeFallback:true,composableFilters:true,measurements},null,2));
}

// Exercise real server pages and shared controls together: a page-local exception
// must fail even when each page is individually within the viewport.
export async function settingsLayout(page,context,browser,url,evidence){
 const measurements=[];
 const tabs=['preferences','notifications','models','apps','capabilities'];
 const close=(actual,expected,label)=>assert(Math.abs(actual-expected)<=1,`${label}: ${actual} != ${expected}`);
 const geometry=async()=>page.evaluate(()=>{
  const rect=n=>{const r=n.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height,right:r.right}};
  const frame=document.querySelector('.settings-page'),box=rect(frame),style=getComputedStyle(frame);
  return {frame:box,left:box.x+parseFloat(style.paddingLeft),right:box.right-parseFloat(style.paddingRight),title:rect(frame.querySelector('h1')),
   overflow:document.documentElement.scrollWidth>innerWidth,
   controls:[...frame.querySelectorAll('.preference-controls .control-select,.setting-row>.control-select,.notification-preference .setting-controls .control-select')].map(rect)};
 });
 const visit=async path=>{
  await page.goto(url+'/settings/'+path);
  await page.evaluate(()=>document.fonts.ready);
  if(path==='preferences')await page.locator('#interface-theme-picker').waitFor();
  if(path==='notifications')await page.locator('#notification-target-picker').waitFor();
  if(path==='capabilities/memory')await page.locator('#capability-mode-picker').waitFor();
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const result=await geometry();assert(!result.overflow,`Settings overflow: ${path} at ${page.viewportSize().width}`);return result;
 };
 for(const language of ['en','zh-CN']){
  await context.request.post(url+'/settings/language',{form:{language}});
  for(const theme of ['light','dark']){
   await page.evaluate(value=>localStorage.setItem('steward-theme',value),theme);
   for(const width of [2048,1400,1024,761,760,390,320]){
    await page.setViewportSize({width,height:980});let reference,value;
    for(const tab of [...tabs,'capabilities/memory']){
     const result=await visit(tab),label=`${language}/${theme}/${width}/${tab}`;
     if(!reference)reference=result;
     close(result.left,reference.left,label+' content left');close(result.right,reference.right,label+' content right');
     close(result.frame.width,reference.frame.width,label+' frame width');close(result.title.x,reference.title.x,label+' title left');
     // Details have a Back link above their heading, within the same frame.
     if(tabs.includes(tab))close(result.title.y,reference.title.y,label+' title top');
     for(const control of result.controls){
      if(!value)value=control;
      close(control.width,value.width,label+' Select width');close(control.height,value.height,label+' Select height');close(control.right,result.right,label+' Select trailing edge');
      if(width<=760)assert(control.height>=44,label+' touch height');
     }
     measurements.push({language,theme,width,tab,...result});
     if(language==='en'&&[2048,390].includes(width))await page.screenshot({path:`${evidence}/settings-${tab.replaceAll('/','-')}-${theme}-${width}.png`});
    }
    assert(reference.controls.length===2,'Language or Appearance selector missing');
   }
  }
 }
 // Collapsing the primary rail changes available space equally for all tabs.
 await page.setViewportSize({width:1400,height:980});await page.evaluate(()=>localStorage.setItem('steward-sidebar','collapsed'));
 let collapsed;
 for(const tab of tabs){const result=await visit(tab);if(!collapsed)collapsed=result;close(result.left,collapsed.left,tab+' collapsed left');close(result.right,collapsed.right,tab+' collapsed right')}
 await page.evaluate(()=>{localStorage.setItem('steward-sidebar','open');localStorage.setItem('steward-theme','light')});
 await context.request.post(url+'/settings/language',{form:{language:'en'}});
 const native=await browser.newContext({javaScriptEnabled:false,viewport:{width:1400,height:980}});
 try{
  await native.addCookies(await context.cookies());const plain=await native.newPage();
  for(const width of [1400,761,390,320]){
   await plain.setViewportSize({width,height:980});let value;
   for(const tab of ['preferences','notifications','capabilities/memory']){
    await plain.goto(url+'/settings/'+tab);await plain.evaluate(()=>document.fonts.ready);
    const result=await plain.locator('#interface-language,#notification-target,#capability-mode').evaluate(n=>{const r=n.getBoundingClientRect();return {width:r.width,right:r.right,height:r.height,overflow:document.documentElement.scrollWidth>innerWidth}});
    const enhanced=measurements.find(row=>row.language==='en'&&row.theme==='light'&&row.width===width&&row.tab==='preferences').controls[0];
    close(result.width,enhanced.width,tab+' native/enhanced width');
    if(!value)value=result;close(result.width,value.width,tab+' native width');close(result.right,value.right,tab+' native right');assert(!result.overflow,tab+' native overflow');
    if(tab==='preferences')await plain.locator('.preference-save').waitFor({state:'visible'});
   }
  }
 }finally{await native.close()}
 await writeFile(evidence+'/settings-layout.json',JSON.stringify(measurements,null,2));
 await page.goto(url+'/settings/apps');await page.locator('[data-apps-ready]').waitFor();
}
