// Runs web/static/js/dashboard-live.js on a virtual clock with a fake WebSocket and fetch, and
// checks how the page gets its values: pushed while the socket is open, polled otherwise, never
// both. Usage: node dashboard_live_client.js <dashboard-live.js>; prints the failures, exits 1.
'use strict';
const fs = require('fs');
const vm = require('vm');

const source = fs.readFileSync(process.argv[2], 'utf8');
const failures = [];
const INTERVAL = 2000;

function check(scenario, ok, what) {
    if (!ok) failures.push(scenario + ': ' + what);
}

// A page with the script loaded: its clock, sockets, requests and what it showed
function page(opts) {
    opts = opts || {};
    let now = 0;
    let nextId = 1;
    const timers = new Map();
    const sockets = [];
    const polls = [];
    const shown = [];
    const calls = { failed: 0, definition: 0 };
    const latencies = [];
    const pong = { delay: 'pongDelay' in opts ? opts.pongDelay : 30 };
    const listeners = {};

    class FakeSocket {
        constructor(url) {
            this.url = url;
            this.readyState = 0;
            this.closedByPage = false;
            this.sent = [];
            this.at = now;
            sockets.push(this);
        }
        close() {
            this.closedByPage = true;
            this.readyState = 3;
            queueMicrotask(() => this.onclose && this.onclose({ code: 1005 }));
        }
        // what the page sends: vCLU answers a ping after pongDelay ms (or pongDelay(id)), never
        // when it is null
        send(data) {
            const m = JSON.parse(data);
            this.sent.push(m);
            const delay = typeof pong.delay === 'function' ? pong.delay(m.id) : pong.delay;
            if (m.type === 'ping' && delay != null) {
                ctx.setTimeout(() => this.readyState === 1 && this.push({ type: 'pong', id: m.id }), delay);
            }
        }
        // what the server or the network does
        open() { this.readyState = 1; this.onopen && this.onopen(); }
        push(m) { this.onmessage && this.onmessage({ data: JSON.stringify(m) }); }
        drop() { this.readyState = 3; this.onclose && this.onclose({ code: 1006 }); }
    }

    const ctx = {
        JSON, Error, Promise, queueMicrotask,
        // the retries' randomness: none unless the scenario asks
        Math: Object.assign(Object.create(Math), { random: opts.random || (() => 0) }),
        addEventListener(type, fn) { (listeners[type] = listeners[type] || new Set()).add(fn); },
        removeEventListener(type, fn) { listeners[type] && listeners[type].delete(fn); },
        Date: { now: () => now },
        location: { protocol: 'http:', host: 'vclu.test' },
        setTimeout(fn, ms) { const id = nextId++; timers.set(id, { at: now + ms, fn, every: 0 }); return id; },
        setInterval(fn, ms) { const id = nextId++; timers.set(id, { at: now + ms, fn, every: ms }); return id; },
        clearTimeout(id) { timers.delete(id); },
        clearInterval(id) { timers.delete(id); },
        fetch(url) {
            polls.push({ url, at: now });
            const body = opts.pollAnswer || { a: { value: 1 } };
            return Promise.resolve({ status: 200, ok: true, json: () => Promise.resolve(JSON.parse(JSON.stringify(body))) });
        }
    };
    if (!opts.noWebSocket) ctx.WebSocket = FakeSocket;
    ctx.window = ctx;
    vm.createContext(ctx);
    vm.runInContext(source, ctx);

    const live = ctx.VCLU.dashboardLive('/api/dashboard/home', {
        interval: () => INTERVAL,
        show: (v) => shown.push(JSON.parse(JSON.stringify(v))),
        failed: () => calls.failed++,
        definition: () => calls.definition++,
        latency: (l) => latencies.push(l)
    });

    async function settle() {
        for (let i = 0; i < 5; i++) await new Promise((r) => setImmediate(r));
    }
    // Moves the clock by ms, running the timers due on the way
    async function advance(ms) {
        const end = now + ms;
        for (;;) {
            let next = null;
            for (const [id, t] of timers) {
                if (t.at <= end && (!next || t.at < next[1].at)) next = [id, t];
            }
            if (!next) break;
            const [id, t] = next;
            now = t.at;
            if (t.every) t.at += t.every; else timers.delete(id);
            t.fn();
            await settle();
        }
        now = end;
        await settle();
    }
    const socket = () => sockets[sockets.length - 1];
    const open = () => sockets.filter((s) => s.readyState === 1 && !s.closedByPage);
    const fire = (type) => (listeners[type] || []).forEach((fn) => fn());
    const listening = (type) => (listeners[type] ? listeners[type].size : 0);
    return { live, sockets, socket, open, polls, shown, calls, latencies, pong, advance, settle, fire, listening, now: () => now };
}

async function pushed() {
    const p = page();
    p.live.start();
    check('pushed', p.sockets.length === 1 && p.socket().url === 'ws://vclu.test/api/dashboard/home/ws', 'one socket to /ws');
    p.socket().open();
    p.socket().push({ type: 'values', values: { a: { value: 1 }, c: { text: 'x' } } });
    p.socket().push({ type: 'patch', values: { a: { value: 2 } }, removed: ['c'] });
    await p.advance(60000);
    check('pushed', p.polls.length === 0, 'no /values while the socket is open, got ' + p.polls.length);
    check('pushed', JSON.stringify(p.shown) === JSON.stringify([{ a: { value: 1 }, c: { text: 'x' } }, { a: { value: 2 } }]),
        'the whole state each time, patches applied: ' + JSON.stringify(p.shown));
    check('pushed', JSON.stringify(p.live.values()) === JSON.stringify({ a: { value: 2 } }), 'values() is the state');
}

async function neverOpens() {
    const p = page();
    p.live.start();
    await p.advance(4900);
    check('neverOpens', p.polls.length === 0, 'no poll before the socket had its time');
    await p.advance(100 + INTERVAL);
    check('neverOpens', p.polls.length === 1 && p.polls[0].url === '/api/dashboard/home/values', 'polls once the wait is over');
    p.socket().open();
    const n = p.polls.length;
    await p.advance(30000);
    check('neverOpens', p.polls.length === n, 'polling stops when the socket opens late');
}

async function dropped() {
    const p = page();
    p.live.start();
    p.socket().open();
    p.socket().push({ type: 'values', values: { a: { value: 1 } } });
    p.socket().drop();
    check('dropped', p.live.values() === null, 'no state from a closed socket');
    await p.advance(INTERVAL);
    check('dropped', p.polls.length === 1, 'polls at once after a drop, got ' + p.polls.length);
    check('dropped', p.sockets.length === 2, 'tries again after ' + INTERVAL + ' ms');
    p.socket().open();
    const n = p.polls.length;
    await p.advance(30000);
    check('dropped', p.polls.length === n, 'polling stops when the socket is back');
    // a patch before the first values of a socket is not applied to nothing
    p.socket().push({ type: 'patch', values: { b: { value: 1 } }, removed: [] });
    check('dropped', p.shown.length === 2, 'a patch without values is ignored');
}

async function backoff() {
    // each failed open doubles the wait for the next try
    const q = page();
    q.live.start();
    const waits = [];
    for (let i = 0; i < 6; i++) {
        const before = q.sockets.length;
        q.socket().drop();
        const at = q.now();
        while (q.sockets.length === before) await q.advance(500);
        waits.push(q.now() - at);
    }
    check('backoff', JSON.stringify(waits) === JSON.stringify([2000, 4000, 8000, 16000, 30000, 30000]),
        'waits double from 2 s to 30 s: ' + JSON.stringify(waits));
    q.socket().open();
    q.socket().drop();
    const at = q.now();
    const before = q.sockets.length;
    while (q.sockets.length === before) await q.advance(500);
    check('backoff', q.now() - at === 2000, 'an open socket resets the wait, got ' + (q.now() - at));

    // plus up to half of the wait again, so pages do not all come back in the same second
    const j = page({ random: () => 0.99 });
    j.live.start();
    j.socket().drop();
    await j.advance(2900);
    check('backoff', j.sockets.length === 1, 'the randomness waits longer');
    await j.advance(100);
    check('backoff', j.sockets.length === 2, 'by at most half of the wait');
}

async function online() {
    const p = page();
    p.live.start();
    p.socket().drop();
    await p.advance(100);
    p.fire('online');
    check('online', p.sockets.length === 2, 'the network back: tries at once');
    p.fire('online');
    check('online', p.sockets.length === 2, 'not while a socket is trying');
    p.socket().open();
    p.fire('online');
    check('online', p.sockets.length === 2, 'not while one is open');
    p.socket().drop();
    p.live.stop();
    p.fire('online');
    check('online', p.sockets.length === 2, 'not after stop');
    check('online', p.listening('online') === 0, 'stop stops listening');
}

async function stopped() {
    const p = page();
    p.live.start();
    p.live.stop();
    await p.settle();
    check('stopped', p.sockets[0].closedByPage, 'stop closes a connecting socket');
    await p.advance(120000);
    check('stopped', p.sockets.length === 1 && p.polls.length === 0, 'nothing while stopped');
    p.live.start();
    check('stopped', p.sockets.length === 2, 'start connects again');
    // an answer that comes after stop is not shown
    p.socket().drop();
    await p.advance(INTERVAL - 1);
    const shown = p.shown.length;
    p.live.poll();
    p.live.stop();
    await p.settle();
    check('stopped', p.shown.length === shown, 'a poll answered after stop is not shown');
}

async function messages() {
    const p = page();
    p.live.start();
    p.socket().open();
    p.socket().push({ type: 'definition' });
    p.socket().push({ type: 'error', error: 'dashboard unavailable' });
    p.socket().onmessage({ data: 'not json' });
    check('messages', p.calls.definition === 1 && p.calls.failed === 1, 'definition and error reach the page: ' + JSON.stringify(p.calls));
}

async function latency() {
    const p = page({ pongDelay: 40 });
    p.live.start();
    p.socket().open();
    check('latency', p.socket().sent.length === 1 && p.socket().sent[0].type === 'ping', 'a ping as soon as it opens');
    await p.advance(100);
    check('latency', JSON.stringify(p.latencies) === JSON.stringify([{ ms: 40, via: 'ws' }]), 'the round trip: ' + JSON.stringify(p.latencies));
    await p.advance(2440 - 1);
    check('latency', p.socket().sent.length === 1, 'the next ping waits 2.5 s after the pong');
    await p.advance(1 + 40);
    check('latency', p.socket().sent.length === 2 && p.latencies.length === 2, 'then goes: ' + p.socket().sent.length);
    // vCLU stops answering (a dead connection: no close comes); the next ping goes at 5080
    p.pong.delay = null;
    await p.advance(5080 + 9000 - 1 - p.now());
    check('latency', p.polls.length === 0 && !p.sockets[0].closedByPage, 'waits PONG_WAIT for the answer');
    await p.advance(1 + INTERVAL);
    check('latency', p.sockets[0].closedByPage, 'a missing pong ends the socket');
    check('latency', p.latencies.indexOf(null) >= 0, 'no connection is shown: ' + JSON.stringify(p.latencies));
    check('latency', p.polls.length >= 1, 'and the page polls');
    const viaPoll = p.latencies.filter((l) => l && l.via === 'poll');
    check('latency', viaPoll.length >= 1, 'while polling the latency is a poll\'s time: ' + JSON.stringify(p.latencies));
    // an answer to a ping of a closed socket is not counted
    const n = p.latencies.length;
    p.sockets[0].onmessage && p.sockets[0].onmessage({ data: JSON.stringify({ type: 'pong', id: 1 }) });
    check('latency', p.latencies.length === n, 'a late pong is ignored');
    p.live.stop();
    await p.advance(60000);
    check('latency', p.sockets[p.sockets.length - 1].sent.filter((m) => m.type === 'ping').length <= 1, 'no pings after stop');
}

async function average() {
    // four answers in 100 ms, then one in 300: the fifth counts a tenth
    const p = page({ pongDelay: (id) => (id === 5 ? 300 : 100) });
    p.live.start();
    p.socket().open();
    await p.advance(5 * 2600 + 300);
    const ms = p.latencies.slice(0, 5).map((l) => l.ms);
    check('average', JSON.stringify(ms) === JSON.stringify([100, 100, 100, 100, 120]), 'averaged: ' + JSON.stringify(ms));
}

async function noWebSocket() {
    const p = page({ noWebSocket: true });
    p.live.start();
    await p.advance(3 * INTERVAL);
    check('noWebSocket', p.polls.length === 3, 'polls every interval, got ' + p.polls.length);
    check('noWebSocket', p.shown.length === 3, 'shows each answer');
}

(async () => {
    for (const s of [pushed, neverOpens, dropped, backoff, online, stopped, messages, latency, average, noWebSocket]) {
        try {
            await s();
        } catch (e) {
            failures.push(s.name + ': ' + (e && e.stack || e));
        }
    }
    if (failures.length) {
        console.log(failures.join('\n'));
        process.exit(1);
    }
    console.log('ok');
})();
