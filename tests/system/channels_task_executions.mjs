// Exercise the shared Trace-style sheet against actual execution read models.
export async function executionDetails(page,context,browser,url,evidence,facts){
 const list=url+'/tasks?view=closed&q=report';
 await page.goto(list);
 const row=page.locator('#execution-'+facts.root),opener=row.locator('[data-execution-open]');
 if(await page.locator('[data-execution-open]').count()<2)throw Error('execution navigation needs two real Roots');
 const navigationCount=await page.evaluate(()=>performance.getEntriesByType('navigation').length);
 const update=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/updates'&&new URL(response.url()).searchParams.get('view')==='/tasks/executions/'+facts.root);
 await row.locator('td').nth(2).click();
 const sheet=page.getByRole('dialog',{name:'Execution details',exact:true});
 await sheet.waitFor();await update;
 if(await sheet.evaluate(node=>node.matches(':modal'))||Math.abs((await sheet.boundingBox()).width-700)>2)throw Error('execution did not use the half-width non-modal Trace shell');
 if(await page.evaluate(()=>performance.getEntriesByType('navigation').length)!==navigationCount)throw Error('row inspection navigated away from its list');
 await page.getByRole('heading',{name:'Execution result',exact:true}).waitFor();
 if(!await sheet.locator('#task-messages').innerText().then(text=>text.includes('Main Task follow-up through Pi.')))throw Error('execution omitted its accepted Main delivery');
 if(!await sheet.getByRole('link',{name:'View trace →',exact:true}).getAttribute('href').then(value=>value.includes(facts.root)))throw Error('execution trace escaped its Root');
 // The background poller retains the original list filters after the URL
 // changes to the selected execution. Both read regions remain independent.
 await page.waitForResponse(response=>{const query=new URL(response.url()).searchParams;return new URL(response.url()).pathname==='/api/updates'&&query.get('view')==='/tasks'&&query.get('q')==='report'&&query.get('task_view')==='closed'});
 if(await page.locator('[data-execution-open]').count()!==2||await sheet.locator('#execution-detail').getAttribute('data-execution-id')!==facts.root)throw Error('list and execution refresh crossed scopes');
 const delivery=await sheet.locator('#task-messages [data-ui-spec]').elementHandle();
 await page.waitForResponse(response=>new URL(response.url()).pathname==='/api/updates'&&new URL(response.url()).searchParams.get('view')==='/tasks/executions/'+facts.root);
 if(!await delivery.evaluate(node=>node.isConnected&&node===document.querySelector('#task-messages [data-ui-spec]')))throw Error('unchanged execution refresh remounted its delivered content');
 const instructions=sheet.locator('.execution-instructions');
 if(await instructions.evaluate(node=>node.open))throw Error('task instructions start expanded');
 if(await sheet.locator('.execution-history').count())throw Error('one execution repeated itself in recent history');
 await instructions.locator('summary').click();
 await page.waitForResponse(response=>new URL(response.url()).pathname==='/api/updates'&&new URL(response.url()).searchParams.get('view')==='/tasks/executions/'+facts.root);
 if(!await instructions.evaluate(node=>node.open))throw Error('live refresh closed the task instructions');
 await sheet.locator('.control-sheet-body').evaluate(node=>node.scrollTop=0);
 await page.setViewportSize({width:1400,height:1800});
 await page.screenshot({path:`${evidence}/execution-instructions-open.png`,fullPage:true,animations:'disabled'});
 await page.setViewportSize({width:1400,height:980});
 await instructions.locator('summary').click();
 const previous=sheet.getByRole('button',{name:'Previous execution',exact:true}),next=sheet.getByRole('button',{name:'Next execution',exact:true});
 await (await previous.isEnabled()?previous:next).click();
 await page.waitForFunction(id=>document.querySelector('#execution-detail')?.getAttribute('data-execution-id')!==id,facts.root);
 await page.goBack();await page.waitForFunction(id=>document.querySelector('#execution-detail')?.getAttribute('data-execution-id')===id,facts.root);
 const resize=sheet.getByRole('separator',{name:'Resize details',exact:true});
 await resize.focus();await resize.press('ArrowRight');
 if((await sheet.boundingBox()).width<=700)throw Error('execution sheet keyboard resizing failed');
 await resize.press('ArrowLeft');
 await sheet.getByRole('button',{name:'Expand details',exact:true}).click();
 if((await sheet.boundingBox()).width<1398)throw Error('execution sheet did not expand');
 await sheet.getByRole('button',{name:'Expand details',exact:true}).click();
 for(const width of [1400,1920,390])for(const theme of ['light','dark']){
  await page.setViewportSize({width,height:980});
  await page.evaluate(value=>document.documentElement.classList.toggle('dark',value==='dark'),theme);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)||await sheet.locator('.control-sheet-body').evaluate(node=>node.scrollWidth>node.clientWidth))throw Error('execution sheet overflowed');
  if(width===390&&Math.abs((await sheet.boundingBox()).width-width)>2)throw Error('mobile execution sheet is not full-width');
  const heading=await sheet.locator('.execution-heading').boundingBox();
  if(width===1400&&heading.height>170)throw Error('execution heading crowded out its delivery');
  if(await sheet.locator('.execution-heading h1').evaluate(node=>parseFloat(getComputedStyle(node).fontSize))>18)throw Error('execution title left the compact type scale');
  for(const body of await sheet.locator('.task-delivery .semantic').all()){
   const type=await body.evaluate(node=>({size:getComputedStyle(node).fontSize,family:getComputedStyle(node).fontFamily}));
   if(type.size!=='14px'||type.family.includes('Source Serif'))throw Error('task delivery does not use the shared UI type scale');
  }
  const result=await sheet.locator('.execution-result').boundingBox(),details=await sheet.locator('.execution-side').boundingBox();
  if(width===390&&result.y>=details.y)throw Error('mobile execution metadata precedes the result');
  await page.screenshot({path:`${evidence}/execution-drawer-${theme}-${width}.png`,fullPage:width>760,animations:'disabled'});
 }
 await page.setViewportSize({width:1400,height:980});await page.evaluate(()=>document.documentElement.classList.remove('dark'));
 await sheet.getByRole('button',{name:'Close details',exact:true}).click();
 await page.waitForURL(list);await sheet.waitFor({state:'detached'});await opener.and(page.locator(':focus')).waitFor();
 await page.goForward();await sheet.waitFor();
 await sheet.getByRole('button',{name:'Close details',exact:true}).focus();await page.keyboard.press('Escape');
 await page.waitForURL(list);await sheet.waitFor({state:'detached'});
 await opener.click();await sheet.waitFor();
 await page.getByRole('button',{name:'Filters',exact:true}).click();
 await page.getByRole('menuitemradio',{name:'All statuses',exact:true}).click();
 await page.waitForURL(target=>target.pathname==='/tasks'&&target.searchParams.get('q')==='report'&&target.searchParams.get('view')==='all');
 if(await page.locator('.control-sheet').count())throw Error('changing background filters retained a stale execution');
 await page.goto(list);await opener.click();await sheet.waitFor();
 await sheet.getByRole('link',{name:'Open full page',exact:true}).click();
 await page.locator('#execution-detail').waitFor();
 if(await page.locator('.control-sheet').count())throw Error('full-page execution retained its sheet');
 const plain=await browser.newContext({javaScriptEnabled:false});
 try{
  await plain.addCookies(await context.cookies());const fallback=await plain.newPage();await fallback.goto(list);
  await fallback.locator('#execution-'+facts.root+' [data-execution-open]').click();
  await fallback.getByRole('heading',{name:'Execution result',exact:true}).waitFor();
  if(!await fallback.locator('#task-messages').innerText().then(text=>text.includes('Main Task follow-up through Pi.')))throw Error('native execution lost its delivery');
 }finally{await plain.close()}
}
