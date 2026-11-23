/**
 * Plugin wizard: settings (step 1), devices with discovery (step 2), module code (step 3).
 * Drawn from the plugin's form file; values are checked again by vclu on save.
 */
(function() {
    'use strict';

    var esc = VCLU.esc;
    var lang = (document.documentElement.lang || 'en').slice(0, 2);
    var T = {
        pl: {
            settings: 'Ustawienia', devices: 'Urządzenia', code: 'Kod', save: 'Zapisz', saved: 'Zapisano',
            secretSet: 'ustawione, zostaw puste, żeby nie zmieniać', secretUnset: 'nie ustawione',
            discover: 'Wykryj', discovering: 'Szukam…', addRow: 'Dodaj ręcznie', saveDevices: 'Zapisz urządzenia',
            add: 'Dodaj', isNew: 'nowe', remove: 'Usuń', noDevices: 'Brak urządzeń. Wykryj albo dodaj ręcznie.',
            notRunning: 'Plugin nie działa: zapisz ustawienia i włącz plugin.', write: 'Zapisz do modułu i przeładuj',
            unchanged: 'Bez zmian względem pliku.', newFile: 'Nowy plik', loading: 'Ładuję…', dropped: 'Pominięte',
            hostsAdded: 'Dodane do dozwolonych hostów', close: 'Zamknij',
        },
        en: {
            settings: 'Settings', devices: 'Devices', code: 'Code', save: 'Save', saved: 'Saved',
            secretSet: 'set, leave empty to keep', secretUnset: 'not set',
            discover: 'Discover', discovering: 'Searching…', addRow: 'Add by hand', saveDevices: 'Save devices',
            add: 'Add', isNew: 'new', remove: 'Remove', noDevices: 'No devices. Discover or add one by hand.',
            notRunning: 'The plugin is not running: save its settings and enable it.', write: 'Write the module and reload',
            unchanged: 'No change from the file.', newFile: 'New file', loading: 'Loading…', dropped: 'Dropped',
            hostsAdded: 'Added to the allowed hosts', close: 'Close',
        }
    };
    var t = T[lang] || T.en;

    function text(v, fallback) {
        if (v === null || v === undefined) return fallback || '';
        if (typeof v === 'string') return v;
        return v[lang] || v.en || v.pl || fallback || '';
    }

    function api(path, opts) {
        return fetch(path, opts).then(function(r) {
            return r.json().then(function(body) {
                if (!r.ok) throw new Error(body.error || ('HTTP ' + r.status));
                return body;
            });
        });
    }

    function Wizard(id) {
        this.id = id;
        this.base = '/api/plugins/' + encodeURIComponent(id);
        this.rows = [];
    }

    Wizard.prototype.modal = function() {
        var el = document.getElementById('pluginWizardModal');
        if (!el) {
            el = document.createElement('div');
            el.className = 'modal fade';
            el.id = 'pluginWizardModal';
            el.tabIndex = -1;
            el.innerHTML = '<div class="modal-dialog modal-xl modal-dialog-scrollable"><div class="modal-content">' +
                '<div class="modal-header"><h5 class="modal-title"></h5>' +
                '<button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>' +
                '<div class="modal-body"><ul class="nav nav-tabs mb-3"></ul><div class="wizard-step"></div></div>' +
                '</div></div>';
            document.body.appendChild(el);
        }
        return el;
    };

    Wizard.prototype.open = function() {
        var self = this;
        return api(this.base + '/form').then(function(data) {
            if (data.formError) VCLU.showToast(data.formError, 'warning');
            if (!data.form) return false;
            self.data = data;
            self.form = typeof data.form === 'string' ? JSON.parse(data.form) : data.form;
            self.rows = (data.devices || []).map(function(v) { return { values: v, include: true, saved: true }; });
            var el = self.modal();
            el.querySelector('.modal-title').textContent = self.id;
            self.tabs();
            self.show('settings');
            bootstrap.Modal.getOrCreateInstance(el).show();
            return true;
        });
    };

    Wizard.prototype.tabs = function() {
        var self = this;
        var steps = [['settings', t.settings]];
        if (this.form.devices) steps.push(['devices', t.devices]);
        steps.push(['code', t.code]);
        var ul = this.modal().querySelector('.nav-tabs');
        ul.innerHTML = steps.map(function(s, i) {
            return '<li class="nav-item"><a class="nav-link" href="#" data-step="' + s[0] + '">' + (i + 1) + '. ' + esc(s[1]) + '</a></li>';
        }).join('');
        ul.querySelectorAll('a').forEach(function(a) {
            a.addEventListener('click', function(e) { e.preventDefault(); self.show(a.dataset.step); });
        });
    };

    Wizard.prototype.show = function(step) {
        var el = this.modal();
        el.querySelectorAll('.nav-link').forEach(function(a) { a.classList.toggle('active', a.dataset.step === step); });
        this.body = el.querySelector('.wizard-step');
        this.body.onchange = null;
        if (step === 'settings') this.renderSettings();
        else if (step === 'devices') this.renderDevices();
        else this.renderCode();
    };

    // ---- fields ----

    function fieldInput(name, f, value, opts) {
        opts = opts || {};
        var attrs = 'data-field="' + esc(name) + '" class="' + (f.type === 'boolean' ? 'form-check-input' : 'form-control form-control-sm') + '"';
        if (opts.readonly) attrs += ' readonly';
        var options = f.options || f.enum;
        if (options) {
            return '<select ' + attrs.replace('form-control', 'form-select') + '>' +
                (f.required ? '' : '<option value=""></option>') +
                options.map(function(o) {
                    return '<option value="' + esc(o) + '"' + (String(value) === o ? ' selected' : '') + '>' + esc(o) + '</option>';
                }).join('') + '</select>';
        }
        if (f.type === 'boolean') {
            return '<input type="checkbox" ' + attrs + (value === true || value === 'true' ? ' checked' : '') + '>';
        }
        if (f.type === 'secret') {
            return '<input type="password" autocomplete="new-password" ' + attrs + ' placeholder="' +
                esc(opts.secretSet ? t.secretSet : t.secretUnset) + '">';
        }
        var type = f.type === 'number' ? 'number' : 'text';
        // the older config marks a password with "secret": true, kept in the config
        if (f.secret === true || /pass|token|apikey/i.test(name)) type = 'password';
        var extra = '';
        if (f.type === 'number') {
            if (f.min !== undefined) extra += ' min="' + f.min + '"';
            if (f.max !== undefined) extra += ' max="' + f.max + '"';
            if (f.step !== undefined) extra += ' step="' + f.step + '"';
        }
        if (f.type === 'host') extra += ' placeholder="192.168.1.10" inputmode="decimal"';
        if (value === undefined || value === null) value = '';
        return '<input type="' + type + '" ' + attrs + extra + ' value="' + esc(value) + '">';
    }

    function readField(input) {
        if (input.type === 'checkbox') return input.checked;
        if (input.value === '') return null;
        if (input.type === 'number') return Number(input.value);
        return input.value;
    }

    function shown(f, values) {
        if (!f.showIf) return true;
        return Object.keys(f.showIf).every(function(k) { return String(values[k]) === String(f.showIf[k]); });
    }

    // ---- step 1 ----

    Wizard.prototype.renderSettings = function() {
        var self = this;
        var fields = this.form.settings || {};
        var values = this.data.settings || {};
        var html = '';
        Object.keys(fields).forEach(function(name) {
            var f = fields[name];
            var v = values[name] !== undefined ? values[name] : f.default;
            html += '<div class="mb-3" data-row="' + esc(name) + '">' +
                '<label class="form-label">' + esc(text(f.label, name)) + (f.required ? ' <span class="text-danger">*</span>' : '') +
                (f.unit ? ' <span class="text-muted small">(' + esc(f.unit) + ')</span>' : '') + '</label>' +
                fieldInput(name, f, v, { secretSet: (self.data.secrets || {})[name] }) +
                (f.description ? '<div class="form-text">' + esc(text(f.description)) + '</div>' : '') + '</div>';
        });
        html += '<button class="btn btn-primary btn-sm" data-act="save">' + esc(t.save) + '</button>';
        this.body.innerHTML = html;
        var refresh = function() {
            var now = self.collectSettings(true);
            Object.keys(fields).forEach(function(name) {
                self.body.querySelector('[data-row="' + CSS.escape(name) + '"]').style.display = shown(fields[name], now) ? '' : 'none';
            });
        };
        this.body.onchange = refresh;
        refresh();
        this.body.querySelector('[data-act="save"]').addEventListener('click', function() {
            self.save({ settings: self.collectSettings(false) });
        });
    };

    Wizard.prototype.collectSettings = function(forShowIf) {
        var out = {};
        var fields = this.form.settings || {};
        this.body.querySelectorAll('[data-field]').forEach(function(input) {
            var name = input.dataset.field;
            var v = readField(input);
            if (fields[name] && fields[name].type === 'secret' && !forShowIf) {
                if (v !== null) out[name] = v; // left empty: the stored secret stays
                return;
            }
            if (v !== null) out[name] = v;
        });
        return out;
    };

    Wizard.prototype.save = function(body) {
        var self = this;
        return api(this.base + '/form', {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
        }).then(function(r) {
            var msg = t.saved;
            if (r.hostsAdded && r.hostsAdded.length) msg += '. ' + t.hostsAdded + ': ' + r.hostsAdded.join(', ');
            VCLU.showToast(r.warning || msg, r.warning ? 'warning' : 'success');
            return api(self.base + '/form');
        }).then(function(data) {
            self.data = data;
            self.rows = (data.devices || []).map(function(v) { return { values: v, include: true, saved: true }; });
        }).catch(function(e) { VCLU.showToast(e.message, 'danger'); });
    };

    // ---- step 2 ----

    Wizard.prototype.renderDevices = function() {
        var self = this;
        var d = this.form.devices;
        var names = Object.keys(d.fields);
        var html = '<div class="d-flex gap-2 mb-3">';
        if (d.discover) html += '<button class="btn btn-outline-primary btn-sm" data-act="discover">' + esc(t.discover) + '</button>';
        html += '<button class="btn btn-outline-secondary btn-sm" data-act="add">' + esc(t.addRow) + '</button>' +
            '<span class="wizard-status small text-muted align-self-center"></span></div>';
        if (!this.rows.length) {
            html += '<p class="text-muted">' + esc(t.noDevices) + '</p>';
        } else {
            html += '<div class="table-responsive"><table class="table table-sm align-middle"><thead><tr><th>' + esc(t.add) + '</th>' +
                names.map(function(n) { return '<th>' + esc(text(d.fields[n].label, n)) + '</th>'; }).join('') +
                '<th></th></tr></thead><tbody>';
            this.rows.forEach(function(row, i) {
                html += '<tr data-index="' + i + '"><td class="text-nowrap"><input type="checkbox" class="form-check-input" data-include' +
                    (row.include ? ' checked' : '') + '>' + (row.isNew ? ' <span class="badge text-bg-info">' + esc(t.isNew) + '</span>' : '') + '</td>' +
                    names.map(function(n) {
                        var f = d.fields[n];
                        var v = row.values[n] !== undefined ? row.values[n] : f.default;
                        return '<td>' + fieldInput(n, f, v, { readonly: f.readonly && !row.manual }) + '</td>';
                    }).join('') +
                    '<td><button class="btn btn-link btn-sm text-danger" data-remove>' + esc(t.remove) + '</button></td></tr>';
            });
            html += '</tbody></table></div>';
        }
        html += '<button class="btn btn-primary btn-sm" data-act="save">' + esc(t.saveDevices) + '</button>';
        this.body.innerHTML = html;

        var on = function(sel, fn) { var b = self.body.querySelector(sel); if (b) b.addEventListener('click', fn); };
        on('[data-act="add"]', function() {
            self.readRows();
            self.rows.push({ values: {}, include: true, manual: true });
            self.renderDevices();
        });
        on('[data-act="discover"]', function() { self.discover(); });
        on('[data-act="save"]', function() {
            self.readRows();
            var devices = self.rows.filter(function(r) { return r.include; }).map(function(r) { return r.values; });
            self.save({ devices: devices }).then(function() { self.renderDevices(); });
        });
        this.body.querySelectorAll('[data-remove]').forEach(function(b) {
            b.addEventListener('click', function() {
                self.readRows();
                self.rows.splice(Number(b.closest('tr').dataset.index), 1);
                self.renderDevices();
            });
        });
    };

    Wizard.prototype.readRows = function() {
        var self = this;
        this.body.querySelectorAll('tr[data-index]').forEach(function(tr) {
            var row = self.rows[Number(tr.dataset.index)];
            row.include = tr.querySelector('[data-include]').checked;
            tr.querySelectorAll('[data-field]').forEach(function(input) {
                var v = readField(input);
                if (v === null) delete row.values[input.dataset.field];
                else row.values[input.dataset.field] = v;
            });
        });
    };

    // a new discovery keeps what the user changed and marks the rows it did not know
    Wizard.prototype.discover = function() {
        var self = this;
        this.readRows();
        var status = this.body.querySelector('.wizard-status');
        status.textContent = t.discovering;
        api(this.base + '/discover', { method: 'POST' }).then(function(r) {
            var key = self.data.rowKey;
            var known = {};
            self.rows.forEach(function(row) { if (key && row.values[key] !== undefined) known[String(row.values[key])] = true; });
            r.rows.forEach(function(found) {
                if (key && known[String(found[key])]) return;
                self.rows.push({ values: found, include: false, isNew: true });
            });
            self.renderDevices();
            if (r.dropped && r.dropped.length) {
                self.body.querySelector('.wizard-status').textContent = t.dropped + ': ' + r.dropped.join('; ');
            }
        }).catch(function(e) {
            status.textContent = '';
            VCLU.showToast(e.message, 'danger');
        });
    };

    // ---- step 3 ----

    Wizard.prototype.renderCode = function() {
        var self = this;
        this.body.innerHTML = '<p class="text-muted">' + esc(t.loading) + '</p>';
        api(this.base + '/module-code').then(function(r) {
            var html = '<p class="small"><code>' + esc(r.path) + '</code>' + (r.current ? '' : ' (' + esc(t.newFile) + ')') + '</p>';
            if (r.current === r.proposed) {
                html += '<p class="text-muted">' + esc(t.unchanged) + '</p>';
            } else {
                html += '<pre class="wizard-diff border rounded p-2 small" style="max-height:50vh;overflow:auto">' + diffHTML(r.current, r.proposed) + '</pre>' +
                    '<button class="btn btn-primary btn-sm" data-act="write">' + esc(t.write) + '</button>';
            }
            self.body.innerHTML = html;
            var b = self.body.querySelector('[data-act="write"]');
            if (b) b.addEventListener('click', function() {
                b.disabled = true;
                api(self.base + '/module-code', {
                    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ hash: r.hash })
                }).then(function(w) {
                    VCLU.showToast(w.warning || (t.saved + ': ' + w.path), w.warning ? 'warning' : 'success');
                    self.renderCode();
                }).catch(function(e) { b.disabled = false; VCLU.showToast(e.message, 'danger'); });
            });
        }).catch(function(e) {
            self.body.innerHTML = '<div class="alert alert-secondary">' + esc(self.data.running ? e.message : t.notRunning) + '</div>';
        });
    };

    // a line diff (longest common subsequence), enough for a module of a few hundred lines
    function diffHTML(a, b) {
        var x = a ? a.replace(/\n$/, '').split('\n') : [];
        var y = b.replace(/\n$/, '').split('\n');
        var n = x.length, m = y.length, i, j;
        var L = [];
        for (i = 0; i <= n; i++) { L.push(new Array(m + 1).fill(0)); }
        for (i = n - 1; i >= 0; i--) {
            for (j = m - 1; j >= 0; j--) {
                L[i][j] = x[i] === y[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
            }
        }
        var out = [];
        i = 0; j = 0;
        var line = function(cls, sign, s) { out.push('<span class="' + cls + '">' + sign + ' ' + esc(s) + '</span>'); };
        while (i < n && j < m) {
            if (x[i] === y[j]) { line('text-body-secondary', ' ', x[i]); i++; j++; }
            else if (L[i + 1][j] >= L[i][j + 1]) { line('text-danger', '-', x[i]); i++; }
            else { line('text-success', '+', y[j]); j++; }
        }
        while (i < n) { line('text-danger', '-', x[i++]); }
        while (j < m) { line('text-success', '+', y[j++]); }
        return out.join('\n');
    }

    // Opens the wizard; resolves false when the plugin has no form
    window.PluginWizard = {
        open: function(id) { return new Wizard(id).open(); }
    };
})();
