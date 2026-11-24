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
            discover: 'Wykryj', discovering: 'Szukam…', addRow: 'Dodaj ręcznie',
            add: 'Dodaj', isNew: 'nowe', remove: 'Usuń', noDevices: 'Brak urządzeń. Wykryj albo dodaj ręcznie.',
            notRunning: 'Plugin nie działa: zapisz ustawienia i włącz plugin.', write: 'Zapisz do modułu i przeładuj',
            unchanged: 'Bez zmian względem pliku.', newFile: 'Nowy plik', loading: 'Ładuję…', dropped: 'Pominięte',
            hostsAdded: 'Dodane do dozwolonych hostów', close: 'Zamknij', back: 'Wstecz', next: 'Dalej',
            required: 'Wymagane', badHost: 'Podaj adres IP, np. 192.168.1.10', min: 'Najmniej', max: 'Najwięcej',
            fixFirst: 'Popraw zaznaczone pola.',
        },
        en: {
            settings: 'Settings', devices: 'Devices', code: 'Code', save: 'Save', saved: 'Saved',
            secretSet: 'set, leave empty to keep', secretUnset: 'not set',
            discover: 'Discover', discovering: 'Searching…', addRow: 'Add by hand',
            add: 'Add', isNew: 'new', remove: 'Remove', noDevices: 'No devices. Discover or add one by hand.',
            notRunning: 'The plugin is not running: save its settings and enable it.', write: 'Write the module and reload',
            unchanged: 'No change from the file.', newFile: 'New file', loading: 'Loading…', dropped: 'Dropped',
            hostsAdded: 'Added to the allowed hosts', close: 'Close', back: 'Back', next: 'Next',
            required: 'Required', badHost: 'Enter an IP address, e.g. 192.168.1.10', min: 'At least', max: 'At most',
            fixFirst: 'Fix the marked fields.',
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
                '<div class="modal-body"><ul class="nav nav-tabs mb-3"></ul><div class="wizard-error"></div><div class="wizard-step"></div></div>' +
                '<div class="modal-footer"><button type="button" class="btn btn-outline-secondary btn-sm me-auto" data-act="back"></button>' +
                '<button type="button" class="btn btn-primary btn-sm" data-act="next"></button></div>' +
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
            // a plugin already running has its settings saved: every step is open
            self.reached = data.running ? self.steps.length - 1 : 0;
            self.current = null;
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
        this.steps = steps.map(function(s) { return s[0]; });
        var el = this.modal();
        var ul = el.querySelector('.nav-tabs');
        ul.innerHTML = steps.map(function(s, i) {
            return '<li class="nav-item"><a class="nav-link" href="#" data-step="' + s[0] + '">' + (i + 1) + '. ' + esc(s[1]) + '</a></li>';
        }).join('');
        ul.querySelectorAll('a').forEach(function(a) {
            a.addEventListener('click', function(e) {
                e.preventDefault();
                if (self.steps.indexOf(a.dataset.step) <= self.reached) self.go(a.dataset.step);
            });
        });
        el.querySelector('[data-act="back"]').onclick = function() {
            self.go(self.steps[self.steps.indexOf(self.current) - 1]);
        };
        el.querySelector('[data-act="next"]').onclick = function() {
            var at = self.steps.indexOf(self.current);
            if (at === self.steps.length - 1) bootstrap.Modal.getOrCreateInstance(el).hide();
            else self.go(self.steps[at + 1]);
        };
    };

    Wizard.prototype.show = function(step) {
        var el = this.modal();
        this.current = step;
        var at = this.steps.indexOf(step);
        var self = this;
        el.querySelectorAll('.nav-link').forEach(function(a) {
            var i = self.steps.indexOf(a.dataset.step);
            a.classList.toggle('active', i === at);
            a.classList.toggle('disabled', i > self.reached);
        });
        var back = el.querySelector('[data-act="back"]');
        back.textContent = t.back;
        back.style.visibility = at > 0 ? '' : 'hidden';
        el.querySelector('[data-act="next"]').textContent = at < this.steps.length - 1 ? t.next : t.close;
        this.error('');
        this.body = el.querySelector('.wizard-step');
        this.body.onchange = null;
        if (step === 'settings') this.renderSettings();
        else if (step === 'devices') this.renderDevices();
        else this.renderCode();
    };

    Wizard.prototype.error = function(msg) {
        this.modal().querySelector('.wizard-error').innerHTML = msg ? '<div class="alert alert-danger py-2 small">' + esc(msg) + '</div>' : '';
    };

    // Leaving a step saves what changed in it; a step that does not check out keeps the user on it
    Wizard.prototype.go = function(step) {
        var self = this;
        if (step === this.current) return;
        var at = this.steps.indexOf(this.current);
        var buttons = this.modal().querySelectorAll('.modal-footer button');
        buttons.forEach(function(b) { b.disabled = true; });
        this.saveStep().then(function() {
            self.reached = Math.max(self.reached, at + 1);
            self.show(step);
        }, function(e) {
            if (e) self.error(e.message);
        }).then(function() { buttons.forEach(function(b) { b.disabled = false; }); });
    };

    Wizard.prototype.saveStep = function() {
        if (this.current === 'settings') {
            if (!this.checkInputs(this.body, this.form.settings || {})) return Promise.reject(null);
            var settings = this.collectSettings(false);
            if (JSON.stringify(settings) === this.pristine) return Promise.resolve();
            return this.save({ settings: settings });
        }
        if (this.current === 'devices') {
            this.readRows();
            if (!this.checkInputs(this.body, this.form.devices.fields)) return Promise.reject(null);
            var devices = this.rows.filter(function(r) { return r.include; }).map(function(r) { return r.values; });
            if (JSON.stringify(devices) === JSON.stringify(this.data.devices || [])) return Promise.resolve();
            return this.save({ devices: devices });
        }
        return Promise.resolve();
    };

    // marks empty required fields, hosts that are not an IP and numbers out of range; vclu checks again
    Wizard.prototype.checkInputs = function(root, fields) {
        var ok = true;
        root.querySelectorAll('[data-field]').forEach(function(input) {
            var f = fields[input.dataset.field] || {};
            var holder = input.closest('[data-row]') || input.closest('td');
            if (holder && holder.style.display === 'none') return;
            var tr = input.closest('tr');
            if (tr && !tr.querySelector('[data-include]').checked) return; // a row left out is not saved
            var v = readField(input);
            var msg = '';
            if (v === null) {
                // a secret already stored may stay empty
                if (f.required && f.type !== 'boolean' && !(f.type === 'secret' && input.dataset.set)) msg = t.required;
            } else if (f.type === 'host' && !isIP(String(v))) {
                msg = t.badHost;
            } else if (f.type === 'number') {
                if (isNaN(v)) msg = t.required;
                else if (f.min !== undefined && v < f.min) msg = t.min + ' ' + f.min;
                else if (f.max !== undefined && v > f.max) msg = t.max + ' ' + f.max;
            }
            input.classList.toggle('is-invalid', !!msg);
            var fb = input.parentNode.querySelector('.invalid-feedback');
            if (msg && !fb) {
                fb = document.createElement('div');
                fb.className = 'invalid-feedback';
                input.insertAdjacentElement('afterend', fb);
            }
            if (fb) fb.textContent = msg;
            if (msg) ok = false;
        });
        if (!ok) this.error(t.fixFirst);
        return ok;
    };

    function isIP(s) {
        if (s.indexOf(':') >= 0) return /^[0-9a-fA-F:.]+$/.test(s);
        var parts = s.split('.');
        return parts.length === 4 && parts.every(function(p) { return /^\d{1,3}$/.test(p) && Number(p) <= 255; });
    }

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
            return '<input type="password" autocomplete="new-password" ' + attrs + (opts.secretSet ? ' data-set="1"' : '') +
                ' placeholder="' + esc(opts.secretSet ? t.secretSet : t.secretUnset) + '" value="' + esc(value || '') + '">';
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
            var v = values[name] !== undefined ? values[name] : (f.type === 'secret' ? undefined : f.default);
            html += '<div class="mb-3" data-row="' + esc(name) + '">' +
                '<label class="form-label">' + esc(text(f.label, name)) + (f.required ? ' <span class="text-danger">*</span>' : '') +
                (f.unit ? ' <span class="text-muted small">(' + esc(f.unit) + ')</span>' : '') + '</label>' +
                fieldInput(name, f, v, { secretSet: (self.data.secrets || {})[name] }) +
                (f.description ? '<div class="form-text">' + esc(text(f.description)) + '</div>' : '') + '</div>';
        });
        this.body.innerHTML = html;
        var refresh = function() {
            var now = self.collectSettings(true);
            Object.keys(fields).forEach(function(name) {
                self.body.querySelector('[data-row="' + CSS.escape(name) + '"]').style.display = shown(fields[name], now) ? '' : 'none';
            });
        };
        this.body.onchange = refresh;
        refresh();
        this.pristine = JSON.stringify(this.collectSettings(false));
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
            if (!body.devices) return;
            // discovered rows not added yet stay on the list
            var left = self.rows.filter(function(r) { return !r.include; });
            self.rows = (data.devices || []).map(function(v) { return { values: v, include: true, saved: true }; }).concat(left);
        });
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
        this.body.innerHTML = html;

        var on = function(sel, fn) { var b = self.body.querySelector(sel); if (b) b.addEventListener('click', fn); };
        on('[data-act="add"]', function() {
            self.readRows();
            self.rows.push({ values: {}, include: true, manual: true });
            self.renderDevices();
        });
        on('[data-act="discover"]', function() { self.discover(); });
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
                }).catch(function(e) { b.disabled = false; self.error(e.message); });
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
