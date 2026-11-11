// Runs storage-notice.js in a stub DOM on one /api/storage answer and prints the banner.
// Usage: node storage_notice.js <storage-notice.js> <answer.json> <texts.json>
'use strict';
const fs = require('fs');
const vm = require('vm');

const answer = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const texts = JSON.parse(fs.readFileSync(process.argv[4], 'utf8'));
let banner = null;

function node(tag) {
    return {
        tag: tag, className: '', textContent: '', href: '', children: [],
        setAttribute() {},
        appendChild(c) { this.children.push(c); return c; }
    };
}

function text(n) {
    return n.textContent + n.children.map(text).join('');
}

const ctx = {
    Promise: Promise, Number: Number, String: String,
    document: {
        documentElement: { lang: 'en' },
        getElementById: (id) => (id === 'storageNoticeScript' ? { dataset: texts } : null),
        querySelector: (sel) => (sel === '.main-content > .topbar'
            ? { insertAdjacentElement(where, el) { banner = el; } } : null),
        createElement: node,
        createTextNode: (s) => ({ textContent: s, children: [] })
    },
    fetch: () => Promise.resolve({ ok: true, json: () => answer })
};
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), ctx);
setTimeout(() => {
    const out = banner ? {
        shown: true, className: banner.className, text: text(banner),
        link: (banner.children.find((c) => c.tag === 'a') || {}).href
    } : { shown: false };
    process.stdout.write(JSON.stringify(out));
}, 0);
