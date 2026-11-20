import {writeFile} from 'node:fs/promises';

export async function sendFeedback(page,context,url,evidence){
 if(await page.evaluate(()=>typeof crypto.randomUUID)!=='undefined')throw Error('HTTP UUID fallback was not exercised');
 const editor=page.getByLabel('Message Ghost',{exact:true}),picker=page.getByLabel('Attach a file');
 let releaseRequest,releaseReply,committed;
 const requestGate=new Promise(resolve=>{releaseRequest=resolve}),replyGate=new Promise(resolve=>{releaseReply=resolve}),commit=new Promise(resolve=>{committed=resolve});
 const requests=[];
 await page.route('**/message',async route=>{
  const body=route.request().postData()??'',key=body.match(/name="source_id"\r\n\r\n([a-f0-9]{32})/)?.[1];
  requests.push({key,firstFile:body.includes('filename="first.txt"'),nextFile:body.includes('filename="next.txt"')});
  if(requests.length===1){await requestGate;await route.fetch();committed();await replyGate;await route.abort('failed')}
  else if(requests.length===2)await route.abort('failed');
  else if(requests.length===4)await route.fulfill({status:413,body:'message too large'});
  else await route.continue();
 });
 await editor.fill('Show this message immediately <script>literal text</script>.');
 await picker.setInputFiles({name:'first.txt',mimeType:'text/plain',buffer:Buffer.from('first attachment')});
 const firstKey=await page.locator('input[name=source_id]').inputValue();
 await page.getByRole('button',{name:'Send message',exact:true}).click();
 const first=page.locator('#conversation-outgoing .outgoing');await first.waitFor();
 if(await editor.inputValue()!==''||await first.locator('script').count()||!(await first.innerText()).includes('<script>literal text</script>')||await picker.evaluate(node=>node.files.length)!==0)throw Error('send did not immediately stage safe text and release the composer');
 if(await page.locator('#agent-processing').isVisible())throw Error('unaccepted upload claimed Ghost was working');
 await editor.fill('My next message while the first upload waits.');
 await picker.setInputFiles({name:'next.txt',mimeType:'text/plain',buffer:Buffer.from('second attachment')});await editor.press('Enter');
 if(requests.length!==1||await editor.inputValue()!=='My next message while the first upload waits.')throw Error('pending send repeated or overwrote a new draft');
 const contrast=[];
 for(const theme of ['light','dark']){
  await page.evaluate(dark=>document.documentElement.classList.toggle('dark',dark),theme==='dark');
  const colors=await first.evaluate(node=>{
   const style=getComputedStyle(node),values=style.backgroundColor.match(/[\d.]+/g).slice(0,3).map(Number);
   const rgb=values.map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4});
   return {color:style.color,background:style.backgroundColor,ratio:1.05/(rgb[0]*.2126+rgb[1]*.7152+rgb[2]*.0722+.05)};
  });
  if(colors.color!=='rgb(255, 255, 255)'||colors.ratio<4.5)throw Error('white message text lost contrast: '+JSON.stringify(colors));
  contrast.push({theme,...colors});
  await page.screenshot({path:evidence+'/sending-'+theme+'.png'});
 }
 releaseRequest();await commit;
 await page.locator(`#conversation-messages [data-client-source="${firstKey}"]`).waitFor();
 await first.waitFor({state:'detached'});releaseReply();
 await page.waitForFunction(()=>!document.querySelector('.composer[data-submitting]'));
 if(await page.locator('[data-send-state=uncertain]').count()||await editor.inputValue()!=='My next message while the first upload waits.'||await picker.evaluate(node=>node.files[0]?.name)!=='next.txt')throw Error('lost acknowledgement repeated a durable message or disturbed the next draft');
 await page.getByRole('button',{name:'Send message',exact:true}).click();
 const failed=page.locator('.outgoing[data-send-state=uncertain]');await failed.waitFor();
 await editor.fill('Keep the third draft and its file.');
 await picker.setInputFiles({name:'third.txt',mimeType:'text/plain',buffer:Buffer.from('third attachment')});
 const failedKey=await failed.getAttribute('data-client-source');
 await failed.getByRole('button',{name:'Retry sending',exact:true}).click();
 await page.locator(`#conversation-messages [data-client-source="${failedKey}"]`).waitFor();
 await page.locator(`#conversation-outgoing [data-client-source="${failedKey}"]`).waitFor({state:'detached'});
 if(requests.length!==3||requests[0].key!==firstKey||requests[1].key!==requests[2].key||!requests[0].firstFile||!requests[1].nextFile||!requests[2].nextFile)throw Error('retry changed the original message or attachment identity');
 if(await editor.inputValue()!=='Keep the third draft and its file.'||await picker.evaluate(node=>node.files[0]?.name)!=='third.txt')throw Error('retry clobbered a later draft');
 if(await page.locator(`#conversation-messages [data-client-source="${firstKey}"]`).count()!==1||await page.locator(`#conversation-messages [data-client-source="${requests[2].key}"]`).count()!==1)throw Error('outgoing reconciliation duplicated messages');
 await page.waitForFunction(()=>!document.querySelector('.composer[data-submitting]'));
 await page.getByRole('button',{name:'Send message',exact:true}).click();
 const rejected=page.locator('.outgoing[data-send-state=rejected]');await rejected.waitFor();
 const rejectedSource=await rejected.getAttribute('data-client-source');
 await editor.fill('Keep this newer unsent draft.');
 await rejected.getByRole('button',{name:'Edit message',exact:true}).click();
 if(await editor.inputValue()!=='Keep this newer unsent draft.')throw Error('edit action overwrote a newer draft');
 await editor.fill('');await rejected.getByRole('button',{name:'Edit message',exact:true}).click();
 if(await editor.inputValue()!=='Keep the third draft and its file.'||await picker.evaluate(node=>node.files[0]?.name)!=='third.txt')throw Error('rejected message could not restore its editable text and attachment');
 if(await page.locator('#composer-root input[name="source_id"]').inputValue()!==rejectedSource)throw Error('editing a rejected message lost its selected input identity');
 if((await page.locator('#agent-processing').innerText()).trim())throw Error('processing indicator retained visible words');
 await page.setViewportSize({width:390,height:844});
 if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('outgoing message layout overflowed mobile');
 await page.screenshot({path:evidence+'/sending-mobile.png'});
 await writeFile(evidence+'/sending.json',JSON.stringify({requests:requests.length,contrast,immediateBubble:true,composerReleased:true,lostAcknowledgementReconciled:true,retryIdentityPreserved:true,httpUUIDFallback:true,followingDraftPreserved:true},null,2));
 await page.unroute('**/message');
}
