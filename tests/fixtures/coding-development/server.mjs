// A reference project for the coding environment, not Steward product UI.
import http from 'node:http';
import {mkdirSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {DatabaseSync} from 'node:sqlite';

const state = process.env.STEWARD_STATE_DIR;
mkdirSync(state, {recursive: true});
const database = new DatabaseSync(join(state, 'notes.sqlite'));
database.exec('PRAGMA journal_mode=WAL; CREATE TABLE IF NOT EXISTS notes(id INTEGER PRIMARY KEY, body TEXT NOT NULL)');
const list = database.prepare('SELECT id, body FROM notes ORDER BY id');
const insert = database.prepare('INSERT INTO notes(body) VALUES (?)');
const html = `<!doctype html><html lang="zh"><meta charset="utf-8"><title>验证记录</title>
<style>body{max-width:36rem;margin:3rem auto;padding:1rem;font:18px system-ui}input,button{font:inherit;padding:.5rem}li{padding:.6rem 0}</style>
<h1>验证记录</h1><form><label>记录 <input name="note" required></label><button>保存</button></form><ul aria-label="已保存记录"></ul>
<script>const form=document.querySelector('form'),rows=document.querySelector('ul');form.elements.note.value=localStorage.getItem('selected-note')||'';
async function load(){const data=await(await fetch('/api/notes')).json();rows.replaceChildren(...data.map(row=>{const item=document.createElement('li');item.textContent=row.body;return item}))}
form.addEventListener('submit',async event=>{event.preventDefault();const response=await fetch('/api/notes',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({body:form.elements.note.value})});if(!response.ok)throw Error('save failed');localStorage.setItem('selected-note',form.elements.note.value);document.cookie='selected-note='+form.elements.note.value+'; Max-Age=300; Path=/';await load()});load();</script></html>`;
const server = http.createServer(async (request, response) => {
  try {
    if (request.method === 'GET' && request.url === '/') {
      response.writeHead(200, {'Content-Type': 'text/html; charset=utf-8'}).end(html);
    } else if (request.method === 'GET' && request.url === '/healthz') {
      database.prepare('SELECT 1').get();
      response.writeHead(200, {'Content-Type': 'application/json'}).end(JSON.stringify({pid: process.pid}));
    } else if (request.method === 'GET' && request.url === '/api/notes') {
      response.writeHead(200, {'Content-Type': 'application/json'}).end(JSON.stringify(list.all()));
    } else if (request.method === 'POST' && request.url === '/api/notes') {
      let raw = '';
      for await (const chunk of request) {
        raw += chunk;
        if (Buffer.byteLength(raw) > 4096) throw Error('request too large');
      }
      const value = JSON.parse(raw);
      if (typeof value.body !== 'string' || !value.body || value.body.length > 1024) throw Error('invalid note');
      insert.run(value.body);
      response.writeHead(201).end();
    } else {
      response.writeHead(404).end();
    }
  } catch {
    if (!response.headersSent) response.writeHead(400);
    response.end();
  }
});
server.listen(Number(process.env.PORT), '127.0.0.1', () => writeFileSync(join(state, 'service.pid'), String(process.pid)));
process.on('SIGTERM', () => server.close(() => {database.close(); process.exit(0);}));
