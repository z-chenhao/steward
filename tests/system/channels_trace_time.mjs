import {writeFile} from 'node:fs/promises';

export async function traceActivity(page,context,browser,url,evidence){
 const original=page.url(),root=page.locator('[data-trace-activity]'),plot=root.locator('.obs-activity-chart');
 const buckets=await root.evaluate(node=>JSON.parse(node.dataset.traceActivity));
 const first=buckets.findIndex(bucket=>bucket.count>0),last=buckets.findLastIndex(bucket=>bucket.count>0);
 const bar=root.locator('[data-bucket="'+first+'"]');
 const plotWidth=(await plot.boundingBox()).width,span=Date.parse(buckets.at(-1).until)-Date.parse(buckets[0].at);
 for(const index of [first,last]){
  const rect=await root.locator('[data-bucket="'+index+'"] rect').boundingBox();
  const expected=(Date.parse(buckets[index].until)-Date.parse(buckets[index].at))/span*plotWidth;
  if(Math.abs(rect.width-expected)>1)throw Error('clipped histogram bucket does not use proportional time width');
 }

 if(await bar.locator('rect').evaluate(node=>node.getBoundingClientRect().height)<5)throw Error('nonzero histogram bucket rendered with zero height');await bar.hover();
 await page.getByRole('tooltip').filter({hasText:buckets[first].count+' traces'}).waitFor();
 await page.screenshot({animations:'disabled',path:evidence+'/trace-activity-hover.png',fullPage:true});
 await bar.focus();await page.keyboard.press('Enter');
 await page.waitForURL(target=>target.searchParams.get('since')===buckets[first].at&&target.searchParams.get('until')===buckets[first].until);
 const selected=await (await context.request.get(url+'/api/developer/traces'+new URL(page.url()).search)).json();
 if(selected.total!==buckets[first].count)throw Error('histogram click and table selected different counts');
 await page.goBack();await page.waitForURL(original);await plot.waitFor();
 // Reverse brushing selects the same exclusive interval, independent of the
 // plotted height. Browser Back restores the broader query and live controls.
 const a=await root.locator('[data-bucket="'+last+'"]').boundingBox(),b=await bar.boundingBox();
 await page.mouse.move(a.x+a.width/2,a.y+a.height/2);await page.mouse.down();await page.mouse.move(b.x+b.width/2,b.y+b.height/2,{steps:8});
 await page.screenshot({animations:'disabled',path:evidence+'/trace-activity-brush.png',fullPage:true});await page.mouse.up();
 await page.waitForURL(target=>target.searchParams.get('since')===buckets[first].at&&target.searchParams.get('until')===buckets[last].until);
 const brushed=await (await context.request.get(url+'/api/developer/traces'+new URL(page.url()).search)).json();
 if(brushed.total!==buckets.slice(first,last+1).reduce((sum,bucket)=>sum+bucket.count,0))throw Error('brush omitted or replayed boundary buckets');
 await page.goBack();await page.waitForURL(original);await plot.waitFor();
 await bar.focus();await page.keyboard.press('Shift+ArrowRight');
 if(!await root.locator('[data-activity-selection]').isVisible())throw Error('keyboard range preview missing');
 await page.keyboard.press('ArrowLeft');
 if(await root.locator('[data-activity-selection]').isVisible()||await root.locator('[data-activity-explore]').isVisible()||await root.locator('.obs-activity-brush').isVisible())throw Error('keyboard range cancellation left stale selection evidence');
 await page.keyboard.press('ArrowRight');if(!await root.locator('[data-bucket="'+Math.min(first+1,buckets.length-1)+'"]').evaluate(node=>node===document.activeElement))throw Error('histogram keyboard inspection failed');
 await page.keyboard.press('Escape');await page.mouse.move(0,0);
 // Non-time conditions remain exact after an actual bar navigation.
 await page.goto(url+'/developer/observability?role=main&status=completed&search=fixture');
 const filtered=await root.evaluate(node=>JSON.parse(node.dataset.traceActivity));const index=filtered.findIndex(bucket=>bucket.count>0);
 await root.locator('[data-bucket="'+index+'"]').click();await page.waitForURL(target=>target.searchParams.has('since'));
 const query=new URL(page.url()).searchParams;
 if(query.get('role')!=='main'||query.get('status')!=='completed'||query.get('search')!=='fixture'||query.has('cursor'))throw Error('chart drilldown discarded table filters');
 await page.goto(original);
 const touch=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true});
 try{
  await touch.addCookies(await context.cookies());const mobile=await touch.newPage();await mobile.goto(original);
  const chart=mobile.locator('[data-trace-activity]'),touchBar=chart.locator('[data-bucket="'+first+'"]');await touchBar.tap();
  const explore=mobile.getByRole('link',{name:'Explore this window',exact:true});await explore.waitFor();
  if(mobile.url()!==original)throw Error('touch preview navigated without Explore');
  const selectedText=chart.locator('[data-activity-selection]');await selectedText.waitFor();if(!(await selectedText.innerText()).includes(buckets[first].count+' traces'))throw Error('touch preview omitted its count');
  await mobile.screenshot({animations:'disabled',path:evidence+'/trace-activity-touch-preview.png',fullPage:true});
  await explore.tap();await mobile.waitForURL(target=>target.searchParams.get('since')===buckets[first].at);
  await mobile.goBack();await mobile.waitForURL(original);
  const start=await chart.locator('[data-bucket="'+first+'"]').boundingBox(),end=await chart.locator('[data-bucket="'+last+'"]').boundingBox();
  const cdp=await touch.newCDPSession(mobile),y=start.y+start.height/2;
  await cdp.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:start.x+start.width/2,y}]});
  for(let i=1;i<=8;i++)await cdp.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:start.x+start.width/2+(end.x-start.x)*i/8,y}]});
  await cdp.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});await explore.waitFor();
  if(mobile.url()!==original)throw Error('touch brushing unexpectedly changed filters');
  if(!await selectedText.isVisible()||!(await selectedText.innerText()).includes(buckets.slice(first,last+1).reduce((sum,bucket)=>sum+bucket.count,0)+' traces'))throw Error('touch brush omitted its persistent evidence');
  const selection=new URL(await explore.getAttribute('href'));
  if(selection.searchParams.get('since')!==buckets[first].at||selection.searchParams.get('until')!==buckets[last].until)throw Error('touch brush selected wrong range');
  await mobile.screenshot({animations:'disabled',path:evidence+'/trace-activity-touch-brush.png',fullPage:true});
 }finally{await touch.close()}
}

export async function traceTimeline(page,evidence){
 const root=page.locator('[data-split-view]'),axis=page.locator('.obs-timeline-axis');
 if(await page.locator('[data-trace-timeline]').count())throw Error('timeline still needs a visibility toggle');
 if(!await page.locator('.obs-node-time').first().isVisible())throw Error('execution timeline is not visible by default');
 const original=await root.evaluate(node=>({start:Number(node.dataset.timeWindowStart),end:Number(node.dataset.timeWindowEnd)}));
 await page.getByRole('button',{name:'Zoom in timeline',exact:true}).click();
 const zoomed=await root.evaluate(node=>({start:Number(node.dataset.timeWindowStart),end:Number(node.dataset.timeWindowEnd)}));
 if(zoomed.end-zoomed.start>=original.end-original.start)throw Error('timeline zoom did not narrow the time domain');
 await axis.focus();await page.keyboard.press('ArrowRight');
 if(await root.getAttribute('data-time-window-start')===String(zoomed.start))throw Error('timeline keyboard panning failed');
 const beforeNavigation=await root.getAttribute('data-time-window-start');
 await page.getByRole('link',{name:'Details',exact:true}).click();await page.getByRole('heading',{name:'Model configuration',exact:true}).waitFor();
 if(await root.getAttribute('data-time-window-start')!==beforeNavigation)throw Error('node diagnostic navigation reset the timeline window');
 await page.getByRole('link',{name:'Input / Output',exact:true}).click();await page.locator('.obs-io-input').waitFor();
 await page.getByRole('button',{name:'Fit timeline',exact:true}).click();
 const box=await axis.boundingBox();
 const drag=async()=>{await page.mouse.move(box.x+box.width*.2,box.y+box.height/2);await page.mouse.down();await page.mouse.move(box.x+box.width*.8,box.y+box.height/2,{steps:8})};
 await drag();
 if(!await root.evaluate(node=>{const brushes=Array.from(node.querySelectorAll('.obs-timeline-brush'));return brushes.length===node.querySelectorAll('.observation-node').length&&brushes.every(brush=>brush.closest('.obs-node-time'))}))throw Error('timeline selection was painted outside the time lanes');
 await page.keyboard.press('Escape');await page.mouse.up();
 if(await root.locator('.obs-timeline-brush').count()||Number(await root.getAttribute('data-time-window-end'))-Number(await root.getAttribute('data-time-window-start'))!==original.end-original.start)throw Error('cancelled timeline brush changed its range or left stale paint');
 await drag();await page.mouse.up();
 if(Number(await root.getAttribute('data-time-window-end'))-Number(await root.getAttribute('data-time-window-start'))>=original.end-original.start)throw Error('timeline range selection failed');
 await page.screenshot({animations:'disabled',path:evidence+'/trace-timeline-range.png'});
 await page.getByRole('button',{name:'Fit timeline',exact:true}).click();
 const selected=page.locator('.obs-tree [aria-current=true]');await selected.hover();await page.getByRole('tooltip').filter({hasText:/Duration|End time/}).waitFor();
 await page.screenshot({animations:'disabled',path:evidence+'/trace-timeline-hover.png'});await page.mouse.move(0,0);
 await writeFile(evidence+'/trace-time-window.json',JSON.stringify({original,zoomed},null,2));
}
