import assert from 'node:assert/strict';
import test from 'node:test';
import {fileURLToPath} from 'node:url';
import {chromium} from 'playwright-core';
import {build} from '../web/node_modules/esbuild/lib/main.js';

test('Chat follows layout clamps while real reading input cancels following',{timeout:45000},async()=>{
  const bundle=await build({entryPoints:[fileURLToPath(new URL('../web/src/chat-scroll.ts',import.meta.url))],bundle:true,write:false,format:'iife',globalName:'chatScroll'});
  const browser=await chromium.launch({headless:true,args:['--disable-smooth-scrolling'],...(process.env.STEWARD_BROWSER_EXECUTABLE?{executablePath:process.env.STEWARD_BROWSER_EXECUTABLE}:{})});
  try{
    const page=await browser.newPage({viewport:{width:1400,height:980}});
    await page.setContent('<style>html{overflow-anchor:none}body{margin:0}#content{display:flow-root}section{height:700px}footer{height:150px}</style><div id="content"><section>First answer</section><section>Interactive answer</section><section>Latest answer</section></div><footer>Composer</footer>');
    await page.addScriptTag({content:bundle.outputFiles[0].text});
    await page.evaluate(()=>{window.reader=chatScroll.createChatScroll(document.getElementById('content'),()=>{});reader.toBottom()});
    const bottom=()=>page.waitForFunction(()=>reader.isFollowing()&&scrollY+innerHeight>=document.documentElement.scrollHeight-1);
    await bottom();
    // Multiple asynchronous frames can shrink and regrow within one rendering
    // turn. Force the real browser clamp before delivering its scroll event.
    for(let step=0;step<10;step++){
      const clamped=await page.evaluate(step=>{
        const before=scrollY,sections=[...document.querySelectorAll('section')];
        for(const section of sections)section.style.height='1px';
        void document.documentElement.offsetHeight;
        const after=scrollY;
        for(const section of sections)section.style.height=(701+step)+'px';
        void document.documentElement.offsetHeight;
        return after<before;
      },step);
      assert.equal(clamped,true,'fixture must actually clamp the browser viewport');
      await bottom();
    }
    await page.mouse.move(20,200);await page.mouse.wheel(0,-40);
    await page.waitForFunction(()=>!reader.isFollowing()&&scrollY+innerHeight<document.documentElement.scrollHeight-1);
    const reading=await page.evaluate(()=>scrollY);
    await page.evaluate(()=>document.querySelector('section').style.height='1000px');
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    assert.equal(await page.evaluate(()=>scrollY),reading,'growth must retain upward reading intent');
    await page.keyboard.press('End');await bottom();
    for(let step=0;step<3;step++){
      const before=await page.evaluate(()=>scrollY);
      if(before===0)break;
      await page.keyboard.press('PageUp');
      await page.waitForFunction(top=>scrollY<top&&!reader.isFollowing(),before);
    }
    await page.waitForFunction(()=>scrollY===0&&!reader.isFollowing());
    await page.evaluate(()=>document.querySelector('section').style.height='1200px');
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    assert.equal(await page.evaluate(()=>scrollY),0,'PageUp reading intent must survive later growth');
    await page.evaluate(()=>reader.dispose());
  }finally{await browser.close()}
});
