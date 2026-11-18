// Owned HTTPS edge for the mobile card handoff. Steward's private listener
// still receives its literal loopback Host and verifies its workspace session.
import https from 'node:https';
import http from 'node:http';
import {execFileSync} from 'node:child_process';
import {mkdtempSync, readFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

export async function reviewOnPhone(chromium, options, backend, href, filename, token) {
  const target = new URL(href);
  if (target.origin !== 'https://owner-workspace.example.test') throw Error('Unexpected workspace origin');
  const dir = mkdtempSync(join(tmpdir(),'steward-mobile-edge-'));
  let edge, browser;
  try {
    const key = join(dir,'key'), cert = join(dir,'cert');
    execFileSync('openssl',['req','-new','-x509','-nodes','-newkey','rsa:2048','-days','1',
      '-subj','/CN=owner-workspace.example.test','-keyout',key,'-out',cert],{stdio:'ignore'});
    edge = https.createServer({key:readFileSync(key),cert:readFileSync(cert)},(req,res)=>{
      if (req.headers.host !== target.host || req.headers.origin && req.headers.origin !== target.origin) {
        res.writeHead(403).end(); return;
      }
      const headers = {...req.headers,host:new URL(backend).host};
      if (headers.origin) headers.origin=backend;
      const upstream = http.request(backend+req.url,{method:req.method,headers},reply=>{
        res.writeHead(reply.statusCode,reply.headers);
        reply.pipe(res);
      });
      upstream.on('error',()=>res.destroy());
      res.on('close',()=>upstream.destroy());
      req.pipe(upstream);
    });
    await new Promise((resolve,reject)=>{edge.once('error',reject);edge.listen(0,'127.0.0.1',resolve)});
    browser = await chromium.launch({...options,args:['--no-proxy-server',
      '--host-resolver-rules=MAP '+target.hostname+' 127.0.0.1:'+edge.address().port]});
    const context = await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,ignoreHTTPSErrors:true});
    const page = await context.newPage();
    const response = await page.goto(href);
    if (response.status() !== 401) throw Error('Review opened without authentication');
    const field = page.getByLabel('Workspace access token',{exact:true});
    await field.fill('invalid-fixture-token');
    await page.getByRole('button',{name:'Open workspace'}).click();
    await page.getByRole('alert').waitFor();
    await field.fill(token);
    await page.getByRole('button',{name:'Open workspace'}).click();
    await page.waitForURL(href);
    await page.locator('[id="'+target.hash.slice(1)+'"]').getByText(filename,{exact:true}).first().waitFor();
  } finally {
    await browser?.close();
    if (edge) {edge.closeAllConnections(); await new Promise(resolve=>edge.close(resolve))}
    rmSync(dir,{recursive:true,force:true});
  }
}
