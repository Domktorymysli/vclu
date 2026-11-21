// Runs notify-bell.js in a stub DOM: renders the first /api/notifications answer, clicks the
// first entry and the 24 h mute of the second, then prints the bell and the requests it sent.
// Usage: node notify_bell.js <notify-bell.js> <answers.json> <texts.json> [mode]
// answers.json is a list: GET /api/notifications answers in turn (the last one repeats).
// mode "open": the list is open while the poll brings the second answer, then closed.
// mode "update": clicks the first entry with no update modal to open.
'use strict';
const fs = require('fs');
const vm = require('vm');

const answers = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const texts = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));
const mode = process.argv[5] || 'basic';
const calls = [];
let gets = 0;
let poll = null;

function node(tag) {
    const n = {
        tag: tag, children: [], attrs: {}, dataset: {}, listeners: {}, href: '', title: '', type: '', _text: '',
        className: '',
        get textContent() { return this._text + this.children.map((c) => c.textContent).join(''); },
        set textContent(s) { this._text = String(s); this.children = []; },
        classList: {
            has: (c) => n.className.split(/\s+/).includes(c),
            contains(c) { return this.has(c); },
            add(c) { if (!this.has(c)) n.className += ' ' + c; },
            remove(c) { n.className = n.className.split(/\s+/).filter((x) => x !== c).join(' '); },
            toggle(c) { if (this.has(c)) { this.remove(c); return false; } this.add(c); return true; }
        },
        setAttribute(k, v) { this.attrs[k] = String(v); },
        getAttribute(k) { return this.attrs[k]; },
        appendChild(c) { this.children.push(c); return c; },
        insertBefore(c) { this.children.unshift(c); return c; },
        addEventListener(ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); },
        click() { (this.listeners.click || []).forEach((fn) => fn({ preventDefault() {}, stopPropagation() {} })); },
        focus() {}
    };
    return n;
}

function find(n, cls, out) {
    out = out || [];
    if (n.classList && n.classList.has(cls)) out.push(n);
    (n.children || []).forEach((c) => find(c, cls, out));
    return out;
}

const bar = node('div');
const location = { href: '/stats' };
const ctx = {
    Promise, Date, Intl, JSON, String, Math, isNaN,
    location,
    setInterval: (fn) => { poll = fn; return 1; }, clearInterval() {},
    document: {
        documentElement: { lang: 'en' },
        visibilityState: 'visible',
        getElementById: (id) => (id === 'notifyBellScript' ? { dataset: texts } : null),
        querySelector: (sel) => (sel === '.topbar-actions' ? bar : null),
        createElement: node,
        addEventListener() {}
    },
    fetch: (url, opts) => {
        opts = opts || {};
        const method = opts.method || 'GET';
        if (method === 'GET') {
            const a = answers[Math.min(gets++, answers.length - 1)];
            return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(a) });
        }
        calls.push({ url, method, body: opts.body ? JSON.parse(opts.body) : null });
        // a POST answers the list as GET does
        const a = answers[Math.min(gets++, answers.length - 1)];
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(a) });
    }
};
ctx.window = ctx;
ctx.CustomEvent = function(type, init) { this.type = type; this.detail = init && init.detail; };
ctx.document.dispatchEvent = () => {};
if (mode === 'update') ctx.VCLU = { openUpdateModal: () => Promise.resolve(false) };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), ctx);

const tick = () => new Promise((r) => setTimeout(r, 5));
const entriesNow = () => find(bar, 'notify-entry').map((li) => find(li, 'notify-text')[0].textContent);
(async () => {
    await tick();
    if (mode === 'open') {
        const btn = find(bar, 'notify-bell-btn')[0];
        const out = { before: entriesNow() };
        btn.setAttribute('aria-expanded', 'true');
        poll();
        await tick();
        out.whileOpen = entriesNow();
        out.badge = find(bar, 'notify-badge')[0].textContent;
        btn.setAttribute('aria-expanded', 'false');
        const root = bar.children[0];
        (root.listeners['hidden.bs.dropdown'] || []).forEach((fn) => fn({}));
        out.afterClose = entriesNow();
        process.stdout.write(JSON.stringify(out));
        return;
    }
    const btn = find(bar, 'notify-bell-btn')[0];
    const badge = find(bar, 'notify-badge')[0];
    const out = {
        label: btn.getAttribute('aria-label'),
        badge: badge.textContent, badgeClass: badge.className,
        entries: find(bar, 'notify-entry').map((li) => ({
            text: find(li, 'notify-text')[0].textContent,
            body: (find(li, 'notify-body')[0] || { textContent: null }).textContent,
            meta: find(li, 'notify-meta')[0].textContent,
            unread: li.classList.has('unread'),
            href: find(li, 'notify-item')[0].href
        })),
        empty: find(bar, 'notify-empty').map((n) => n.textContent)
    };
    const items = find(bar, 'notify-item');
    if (items[0]) items[0].click();
    await tick();
    out.location = location.href;
    const mutes = find(bar, 'notify-mute');
    const row = mutes[1] || mutes[0];
    if (row) row.children.find((b) => b.dataset.mute === '24h').click();
    await tick();
    out.calls = calls;
    out.live = find(bar, 'visually-hidden').map((n) => n.textContent).join('');
    process.stdout.write(JSON.stringify(out));
})();
