// Runs the notifications card of settings.js in a stub DOM: three sources from the settings
// API, then the log switch turned on. Prints the switches and the body of the PUT.
// Usage: node notify_settings.js <settings.js> <texts.json>
'use strict';
const fs = require('fs');
const vm = require('vm');

const texts = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
let sent = null;

function node(tag) {
    return {
        tag: tag, children: [], dataset: {}, listeners: {}, attrs: {}, className: '', textContent: '', checked: false,
        setAttribute(k, v) { this.attrs[k] = String(v); },
        appendChild(c) { this.children.push(c); return c; },
        addEventListener(ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); }
    };
}

const box = node('div');
const muted = node('ul');
const ctx = {
    JSON, Object, Promise, Date,
    document: {
        documentElement: { lang: 'pl' },
        getElementById: (id) => ({ notifySources: box, notifyMuted: muted,
            notifyI18n: { textContent: JSON.stringify(texts) } })[id] || null,
        createElement: node,
        addEventListener() {},
        dispatchEvent() {}
    },
    CustomEvent: function(type) { this.type = type; },
    VCLU: {
        fetchJSON: (url, opts) => {
            if (opts && opts.method === 'PUT') sent = opts.body;
            return Promise.resolve({ sources: [{ id: 'update', enabled: true, default: true },
                { id: 'log', enabled: false, default: false }, { id: 'future', enabled: false, default: false }], muted: [] });
        },
        showToast() {}
    }
};
ctx.window = ctx;
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), ctx);
vm.runInContext('initNotificationSettings()', ctx);

setTimeout(() => {
    const switches = box.children.map((wrap) => ({
        ID: wrap.children[0].dataset.source,
        Label: wrap.children[1].textContent,
        Hint: wrap.children[2] ? wrap.children[2].textContent : ''
    }));
    const checked = {};
    box.children.forEach((wrap) => { checked[wrap.children[0].dataset.source] = wrap.children[0].checked; });
    const log = box.children[1].children[0];
    log.checked = true;
    log.listeners.change.forEach((fn) => fn({}));
    setTimeout(() => process.stdout.write(JSON.stringify({ Switches: switches, Checked: checked, Sent: sent })), 5);
}, 5);
