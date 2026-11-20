// Runs the OM Files upload against stubbed answers of /api/om/preview and prints, per scenario,
// the requests it sent and the form fields of each.
// Usage: node om_files_preview.js <om-files.js>
'use strict';
const fs = require('fs');
const vm = require('vm');

const removed = { clus: [{ id: 'CLU1', change: 'removed' }], breaks: [] };
const same = { clus: [{ id: 'CLU1', change: 'same' }], breaks: [] };
const scenarios = {
    previewError: () => Promise.resolve({ json: () => ({ error: 'invalid ZIP file' }) }),
    previewRejected: () => Promise.reject(new Error('offline')),
    previewNotJSON: () => Promise.resolve({ json: () => Promise.reject(new Error('Unexpected token <')) }),
    nothingToConfirm: () => Promise.resolve({ json: () => same }),
    confirmed: () => Promise.resolve({ json: () => removed }),
    cancelled: () => Promise.resolve({ json: () => removed }),
};

async function run(name) {
    const elements = {};
    const element = (id) => elements[id] || (elements[id] = {
        id: id, disabled: false, innerHTML: '', textContent: '', value: '', style: {},
        files: [{ name: 'house.omp' }], addEventListener() {}
    });
    const requests = [];
    const listeners = {};
    class FormData {
        constructor() { this.fields = []; }
        append(k, v) { this.fields.push(k + '=' + (typeof v === 'string' ? v : '<file>')); }
    }
    const ctx = {
        console, Promise, JSON, FormData,
        VCLU: { esc: (s) => String(s), showToast() {} },
        bootstrap: { Modal: function() {} },
        document: {
            getElementById: element,
            addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); }
        },
        fetch(url, opts) {
            requests.push(((opts && opts.method) || 'GET') + ' ' + url + (opts && opts.body ? ' ' + opts.body.fields.join('&') : ''));
            if (url === '/api/om/preview') return scenarios[name]();
            if (url === '/api/om/list') return Promise.resolve({ json: () => ({ files: [] }) });
            return Promise.resolve({ json: () => ({ success: true, isProject: true, results: [] }) });
        }
    };
    ctx.window = ctx;
    vm.createContext(ctx);
    vm.runInContext(fs.readFileSync(process.argv[2], 'utf8'), ctx);
    const m = new ctx.OmFilesManager();
    m.elements = { uploadBtn: element('uploadBtn'), fileInput: element('omFile'), uploadResult: element('uploadResult'),
                   tbody: element('tbody'), loading: element('l'), empty: element('e'), error: element('x'), table: element('t') };
    m.loadFiles = () => {};
    const settle = () => new Promise((r) => setTimeout(r, 0));
    m.upload();
    await settle();
    if (name === 'confirmed') elements.omPreviewImport.onclick.call(elements.omPreviewImport);
    if (name === 'cancelled') elements.omPreviewCancel.onclick.call(elements.omPreviewCancel);
    await settle();
    return { requests, shown: elements.uploadResult.innerHTML };
}

(async () => {
    const out = {};
    for (const name of Object.keys(scenarios)) out[name] = await run(name);
    process.stdout.write(JSON.stringify(out));
})();
