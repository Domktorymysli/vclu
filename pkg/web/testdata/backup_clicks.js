// Clicks the Backup page's buttons in a stub DOM and prints the requests each click sent.
// Usage: node backup_clicks.js <backup.js> <buttons.json>; buttons: [{id, onclick}].
'use strict';
const fs = require('fs');
const vm = require('vm');

const buttons = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const elements = {};
const docListeners = {};
const requests = [];
const pending = [];

function element(id) {
    if (!elements[id]) {
        elements[id] = {
            id: id, disabled: false, checked: false, value: '', innerHTML: '', textContent: '',
            style: {}, dataset: {}, listeners: [],
            addEventListener(type, fn) { if (type === 'click') this.listeners.push(fn); },
            appendChild() {},
            querySelectorAll() { return []; }
        };
    }
    return elements[id];
}

const ctx = {
    console: console,
    Promise: Promise,
    JSON: JSON,
    parseInt: parseInt,
    encodeURIComponent: encodeURIComponent,
    confirm: () => true,
    VCLU: { showToast() {}, esc: (s) => String(s) },
    document: {
        getElementById: element,
        createElement: () => element('_tmp'),
        addEventListener(type, fn) { (docListeners[type] = docListeners[type] || []).push(fn); }
    },
    fetch(url, opts) {
        requests.push(((opts && opts.method) || 'GET') + ' ' + url);
        return new Promise((resolve) => {
            pending.push(() => resolve({ json: () => ({ backups: [], filename: 'b.zip', sizeHuman: '1 KB' }) }));
        });
    }
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), ctx);
(docListeners.DOMContentLoaded || []).forEach((fn) => fn());

// A browser runs the inline onclick, then the listeners; a disabled button gets no click.
function click(i) {
    const b = buttons[i];
    const el = element(b.id || ('_button' + i));
    if (el.disabled) return;
    if (b.onclick) vm.runInContext(b.onclick, ctx);
    el.listeners.forEach((fn) => fn.call(el, {}));
}

async function settle() {
    while (pending.length) {
        pending.splice(0).forEach((r) => r());
        await new Promise((r) => setTimeout(r, 0));
    }
}

function find(pred) {
    const i = buttons.findIndex(pred);
    if (i < 0) throw new Error('button not found');
    return i;
}

(async () => {
    const out = {};
    async function scenario(name, fn) {
        await settle();
        requests.length = 0;
        await fn();
        await settle();
        out[name] = requests.slice();
    }
    const create = find((b) => b.id === 'createBackupBtn');
    const save = find((b) => b.id === 'saveBackupSettingsBtn');
    const refresh = find((b) => (b.onclick || '').indexOf('loadBackups') >= 0);

    await scenario('create', () => click(create));
    await scenario('createTwice', () => { click(create); click(create); });
    await scenario('createLegacyTwice', () => { ctx.createBackup(); ctx.createBackup(); });
    await scenario('save', () => click(save));
    await scenario('refresh', () => click(refresh));
    process.stdout.write(JSON.stringify(out));
})().catch((e) => { console.error(e); process.exit(1); });
