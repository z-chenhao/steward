// Exercise the visible Base UI control, retaining native interactions for no-JS.
export async function selectParts(control){
 const identity=await control.evaluate(node=>{const select=node.tagName==='SELECT'?node:node.closest('.control-select')?.querySelector('select');if(!select)throw Error('Missing native select');return {id:select.id,name:select.name}});
 // getByLabel starts on the native select before enhancement and then resolves
 // the visible trigger. Keep the form-value locator stable across that change.
 const native=control.page().locator(identity.id?'select[id='+JSON.stringify(identity.id)+']':'select[name='+JSON.stringify(identity.name)+']');
 const wrapper=native.locator('xpath=..');
 return {native,trigger:wrapper.locator('button[role=combobox]')};
}
export async function selectValue(page,control,value){
 const{native,trigger}=await selectParts(control);
 if(!await page.evaluate(()=>document.documentElement.classList.contains('js')&&typeof fetch==='function')){await native.selectOption(value);return}
 await trigger.waitFor();
 const label=await native.locator('option').evaluateAll((options,value)=>options.find(option=>option.value===value)?.label,value);
 if(label===undefined)throw Error('Unknown native option: '+value+' in '+await native.evaluate(node=>node.outerHTML));
 await trigger.click();await page.getByRole('listbox').getByRole('option',{name:label,exact:true}).click();
}

// Rendered-list contracts shared by the Tasks and Developer compact-list consumers.
// These compare actual regions and text bounds, not a second CSS implementation.
export async function assertDataList(page){
 await page.evaluate(()=>document.fonts.ready);
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 const problems=await page.evaluate(()=>{
  const issues=[],visible=node=>!!node&&node.getBoundingClientRect().width>0&&getComputedStyle(node).visibility!=='hidden';
  const main=document.querySelector('.workspace-main'),primary=document.querySelector('.app-sidebar'),secondary=document.querySelector('.section-sidebar');
  if(document.documentElement.scrollWidth>innerWidth+1)issues.push('document overflows');
  if(innerWidth>760&&visible(secondary)){
   const p=primary.getBoundingClientRect(),s=secondary.getBoundingClientRect(),m=main.getBoundingClientRect();
   if(Math.abs(p.right-s.left)>1||Math.abs(s.right-m.left)>1)issues.push('navigation overlaps content');
   if(!document.documentElement.classList.contains('sidebar-collapsed')&&Math.abs(p.width-s.width)>1)issues.push('primary and secondary navigation widths differ');
   const title=secondary.querySelector('.section-navigation-heading span'),bounds=title.getBoundingClientRect();
   if(bounds.left-s.left<16||s.right-bounds.right<16)issues.push('secondary title has no breathing room');
   for(const label of secondary.querySelectorAll('.section-navigation a span'))if(label.scrollWidth>label.clientWidth+1)issues.push('navigation label clips');
  }
  const svg=selector=>document.querySelector(selector)?.innerHTML;
  const symbols=[svg('.app-sidebar .primary-navigation-top>a[href="/developer/observability"] svg'),svg('.section-navigation a[href="/developer/observability"] svg'),svg('.section-navigation a[href="/developer/memory"] svg')];
  if(location.pathname.startsWith('/developer/')&&(symbols.some(symbol=>!symbol)||new Set(symbols).size!==3))issues.push('Developer, Traces and Memory reuse an icon');
  const row=document.querySelector('.control-list-table tbody tr:not(:has(.task-table-empty))');
  // Visible ID copy controls retain their 44px touch target plus cell padding.
  if(row&&!row.querySelector('.empty-state')){if(row.getBoundingClientRect().height>(innerWidth<=760?58:38))issues.push('list rows lost compact density');if(parseFloat(getComputedStyle(row.querySelector('td')).fontSize)>13)issues.push('list text became oversized')}
  for(const input of document.querySelectorAll('.control-list-search input')){
   const box=input.closest('.control-search').getBoundingClientRect(),style=getComputedStyle(input);
   if(innerWidth>760&&box.height>36)issues.push('search control became oversized');
   if(innerWidth<=760&&(box.height<44||parseFloat(style.fontSize)<16))issues.push('mobile search lost touch size');
  }
  const filter=document.querySelector('.obs-filter-rail:not([open])>summary:not([hidden])'),tabs=document.querySelector('.memory-list-toolbar .section-tabs');
  if(visible(filter)&&visible(tabs)){const a=filter.getBoundingClientRect(),b=tabs.getBoundingClientRect();if(a.left<b.right&&a.right>b.left&&a.top<b.bottom&&a.bottom>b.top)issues.push('collapsed filters overlap view tabs')}
  if(document.querySelector('.control-list-heading')){
   const refresh=document.querySelector('[data-list-refresh]'),bar=document.querySelector('.workspace-topbar');
   if(!visible(refresh)||!bar?.contains(refresh)||document.querySelectorAll('[data-list-refresh]').length!==1)issues.push('Refresh is not uniquely owned by the global header');
   if(visible(refresh)&&Math.abs(bar.getBoundingClientRect().right-refresh.getBoundingClientRect().right-(innerWidth<=760?12:16))>1)issues.push('Refresh is not right aligned');
   const toggle=document.querySelector('[data-list-filter-toggle]:not([hidden]),[data-task-filter-mount] button');
   if(visible(toggle)){
    const search=document.querySelector('.control-list-search').getBoundingClientRect(),toolbar=toggle.closest('.control-list-toolbar').getBoundingClientRect(),b=toggle.getBoundingClientRect();
    if(Math.abs(search.left-b.left)>1||b.top<toolbar.top||b.bottom>toolbar.bottom)issues.push('Filters is not left aligned below search');
    if(b.height<(innerWidth<=760?44:32))issues.push('Filters lost the shared target size');
   }
  }
  const time=document.querySelector('.obs-list-toolbar .control-disclosure-trigger');
  if(visible(time)&&time.getBoundingClientRect().height>(innerWidth<=760?48:36))issues.push('time-range trigger wrapped');
  return issues;
 });
 const toggle=page.locator('[data-list-filter-toggle]:not([hidden])');
 if(await toggle.count()){
  const before=await toggle.boundingBox(),expanded=await toggle.getAttribute('aria-expanded');
  await toggle.click();await page.waitForFunction(({id,state})=>document.getElementById(id).open===state,{id:await toggle.getAttribute('aria-controls'),state:expanded!=='true'});
  const after=await toggle.boundingBox();
  if(Math.abs(before.x-after.x)>1||Math.abs(before.y-after.y)>1)problems.push('Filters moved while toggling');
  await toggle.click();
 }
 if(problems.length)throw Error('Data list layout: '+JSON.stringify({viewport:page.viewportSize(),problems}));
}
