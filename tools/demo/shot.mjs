// Screenshot of a vCLU page through headless Chrome (DevTools protocol, no dependencies).
// node tools/demo/shot.mjs <url> <out.png> [width] [height] [js-step ...]
// Each js-step is evaluated in the page, then 1.5 s wait. Env: CHROME (binary), DPR (default 1.25).
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const [url, out, w = '1280', h = '800', ...steps] = process.argv.slice(2);
if (!url || !out) { console.error('usage: shot.mjs <url> <out.png> [w] [h] [js...]'); process.exit(2); }
const bin = process.env.CHROME || (process.platform === 'darwin'
  ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : 'google-chrome');
const port = 9333;
const chrome = spawn(bin, ['--headless=new', `--remote-debugging-port=${port}`,
  `--user-data-dir=${mkdtempSync(join(tmpdir(), 'vclu-shot-'))}`, '--hide-scrollbars', 'about:blank'],
  { stdio: 'ignore' });
const sleep = ms => new Promise(r => setTimeout(r, ms));

try {
  let page;
  for (let i = 0; i < 50 && !page; i++) {
    await sleep(200);
    try { page = (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find(t => t.type === 'page'); } catch {}
  }
  if (!page) throw new Error('Chrome did not start: ' + bin);
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise(r => ws.onopen = r);
  let id = 0; const pending = {};
  ws.onmessage = m => { const d = JSON.parse(m.data); if (pending[d.id]) { pending[d.id](d); delete pending[d.id]; } };
  const send = (method, params = {}) => new Promise(r => { pending[++id] = r; ws.send(JSON.stringify({ id, method, params })); });

  // DPR 1.25 at 1280 px gives a 1600 px PNG that stays small; 2 is sharper but about twice the size.
  await send('Emulation.setDeviceMetricsOverride', { width: +w, height: +h, deviceScaleFactor: +(process.env.DPR || 1.25), mobile: false });
  await send('Page.navigate', { url });
  await sleep(3500);
  for (const s of steps) {
    const r = await send('Runtime.evaluate', { expression: s, awaitPromise: true, returnByValue: true });
    console.log('step:', JSON.stringify(r.result?.result?.value ?? r.result?.exceptionDetails?.text));
    await sleep(1500);
  }
  const shot = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(out, Buffer.from(shot.result.data, 'base64'));
  console.log('saved', out);
  ws.close();
} finally {
  chrome.kill();
}
