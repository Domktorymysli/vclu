/**
 * Plugin wizard: settings (step 1), devices with discovery (step 2), access to the plugin's
 * dashboards, module code (last). Drawn from the plugin's form file and the dashboards its
 * plugin.json declares; values are checked again by vclu on save.
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
            writeConfirm: 'Zapisać kod do {path} ({changes}) i przeładować wszystkie moduły? Automatyki zatrzymają się na chwilę.',
            writeNew: 'nowy plik', writeChanges: '+{add} / -{del} linii',
            unchanged: 'Bez zmian względem pliku.', newFile: 'Nowy plik', loading: 'Ładuję…', dropped: 'Pominięte',
            hostsAdded: 'Dodane do dozwolonych hostów', close: 'Zamknij', back: 'Wstecz', next: 'Dalej',
            required: 'Wymagane', badHost: 'Podaj adres IP, np. 192.168.1.10', min: 'Najmniej', max: 'Najwięcej',
            fixFirst: 'Popraw zaznaczone pola', row: 'wiersz', cancel: 'Anuluj', done: 'Gotowe', configure: 'Konfiguracja',
            dashboards: 'Dashboardy', dashName: 'Nazwa', who: 'Kto może otworzyć',
            authSession: 'Zalogowani do panelu', authPin: 'Z kodem PIN', authPublic: 'Każdy w sieci domowej', authOff: 'Wyłączony',
            authPublicDesc: 'Bez logowania, np. tablet w kuchni. Poza domem tylko przez aplikację.',
            authOffDesc: 'Dashboard nie jest dostępny ani widoczny na liście.',
            pin: 'Kod PIN', pinDesc: '4 cyfry, wpisywane na stronie dashboardu', badPin: 'Podaj 4 cyfry',
            app: 'W aplikacji vclu (przez chmurę)', appDesc: 'Dashboard w telefonie także poza domem',
            appPin: 'Wymagaj PIN-u w aplikacji', roles: 'Kto widzi w aplikacji',
            rolesDesc: 'Role z chmury vclu po przecinku. Puste: wszyscy domownicy.', rolesKnown: 'Znane role:',
            badRole: 'Role: małe litery, cyfry, - i _', dashCode: 'Dostęp zapisuje się jako kod w module, w ostatnim kroku.',
            login: 'Logowanie', loginDesc: 'Plugin loguje się do swojej usługi przez aplikację w telefonie.',
            signIn: 'Zaloguj', signingIn: 'Czekam na potwierdzenie…', signedIn: 'Zalogowano',
            scanQr: 'Zeskanuj kod w aplikacji', openLink: 'Otwórz link i wpisz kod',
        },
        en: {
            settings: 'Settings', devices: 'Devices', code: 'Code', save: 'Save', saved: 'Saved',
            secretSet: 'set, leave empty to keep', secretUnset: 'not set',
            discover: 'Discover', discovering: 'Searching…', addRow: 'Add by hand',
            add: 'Add', isNew: 'new', remove: 'Remove', noDevices: 'No devices. Discover or add one by hand.',
            notRunning: 'The plugin is not running: save its settings and enable it.', write: 'Write the module and reload',
            writeConfirm: 'Write the code to {path} ({changes}) and reload all modules? Automations stop for a moment.',
            writeNew: 'a new file', writeChanges: '+{add} / -{del} lines',
            unchanged: 'No change from the file.', newFile: 'New file', loading: 'Loading…', dropped: 'Dropped',
            hostsAdded: 'Added to the allowed hosts', close: 'Close', back: 'Back', next: 'Next',
            required: 'Required', badHost: 'Enter an IP address, e.g. 192.168.1.10', min: 'At least', max: 'At most',
            fixFirst: 'Fix the marked fields', row: 'row', cancel: 'Cancel', done: 'Done', configure: 'Setup',
            dashboards: 'Dashboards', dashName: 'Name', who: 'Who can open it',
            authSession: 'Signed in to the panel', authPin: 'With a PIN', authPublic: 'Anyone at home', authOff: 'Off',
            authPublicDesc: 'No sign-in, e.g. a kitchen tablet. Away from home only through the app.',
            authOffDesc: 'The dashboard is not served and not listed.',
            pin: 'PIN', pinDesc: '4 digits, typed on the dashboard page', badPin: 'Enter 4 digits',
            app: 'In the vclu app (through the cloud)', appDesc: 'The dashboard on the phone away from home too',
            appPin: 'Ask for the PIN in the app', roles: 'Who sees it in the app',
            rolesDesc: 'vclu cloud roles, comma separated. Empty: every member of the home.', rolesKnown: 'Known roles:',
            badRole: 'Roles: lower-case letters, digits, - and _', dashCode: 'Access is written as code in a module, in the last step.',
            login: 'Sign-in', loginDesc: 'The plugin signs in to its service through an app on your phone.',
            signIn: 'Sign in', signingIn: 'Waiting for you to confirm…', signedIn: 'Signed in',
            scanQr: 'Scan the code in the app', openLink: 'Open the link and type the code',
        }
    };
    var t = T[lang] || T.en;
    // where a wizard closed with the X was, so opening it again goes back there
    var lastStep = {};

    function text(v, fallback) {
        if (v === null || v === undefined) return fallback || '';
        if (typeof v === 'string') return v;
        return v[lang] || v.en || v.pl || fallback || '';
    }

    function api(path, opts) {
        return fetch(path, opts).then(function(r) {
            return r.json().then(function(body) {
                if (!r.ok) {
                    var e = new Error(body.error || ('HTTP ' + r.status));
                    e.fields = body.fields; // what vclu refused, by field
                    throw e;
                }
                return body;
            });
        });
    }

    function Wizard(id, meta) {
        this.id = id;
        this.meta = meta || {};
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
            el.innerHTML = '<div class="modal-dialog modal-dialog-scrollable pw-dialog"><div class="modal-content pw">' +
                '<div class="modal-header pw-header"><div class="pw-icon"><i class="bi bi-puzzle"></i></div>' +
                '<div class="pw-heading"><div class="pw-kicker"></div><h5 class="modal-title"></h5><div class="pw-sub"></div></div>' +
                '<button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>' +
                '<div class="pw-steps"></div>' +
                '<div class="modal-body"><div class="wizard-error"></div><div class="wizard-step"></div></div>' +
                '<div class="modal-footer pw-footer"><button type="button" class="btn btn-outline-secondary me-auto" data-act="back"></button>' +
                '<button type="button" class="btn btn-primary px-4" data-act="next"></button></div>' +
                '</div></div>';
            document.body.appendChild(el);
        }
        return el;
    };

    Wizard.prototype.open = function() {
        var self = this;
        return api(this.base + '/form').then(function(data) {
            if (data.formError) VCLU.showToast(data.formError, 'warning');
            if (!data.form && !data.dashboards) return false;
            self.data = data;
            self.form = !data.form ? null : typeof data.form === 'string' ? JSON.parse(data.form) : data.form;
            self.rows = (data.devices || []).map(function(v) { return { values: v, include: true, saved: true }; });
            var el = self.modal();
            var m = self.meta;
            el.querySelector('.pw-kicker').textContent = t.configure;
            el.querySelector('.modal-title').textContent = m.name || self.id;
            el.querySelector('.pw-sub').textContent = self.id + (m.version ? ' · v' + m.version : '');
            self.tabs();
            // a plugin already running has its settings saved: every step is open
            self.reached = data.running ? self.steps.length - 1 : 0;
            self.current = null;
            var last = lastStep[self.id];
            if (last) self.reached = Math.max(self.reached, Math.min(last.reached, self.steps.length - 1));
            var start = last && self.steps.indexOf(last.step) >= 0 && self.steps.indexOf(last.step) <= self.reached ? last.step : self.steps[0];
            self.show(start);
            el.addEventListener('hide.bs.modal', function() {
                lastStep[self.id] = self.done ? null : { step: self.current, reached: self.reached };
            }, { once: true });
            bootstrap.Modal.getOrCreateInstance(el).show();
            return true;
        });
    };

    Wizard.prototype.tabs = function() {
        var self = this;
        var form = this.form || {};
        var steps = this.form ? [['settings', t.settings]] : [];
        if (form.devices) steps.push(['devices', t.devices]);
        if (this.data.dashboards) steps.push(['dashboards', t.dashboards]);
        if (form.code || this.data.dashboards) steps.push(['code', t.code]);
        this.steps = steps.map(function(s) { return s[0]; });
        var el = this.modal();
        var ul = el.querySelector('.pw-steps');
        // one step needs no stepper
        ul.style.display = steps.length > 1 ? '' : 'none';
        ul.innerHTML = steps.map(function(s, i) {
            return '<a class="pw-step" href="#" data-step="' + s[0] + '"><span class="pw-dot">' + (i + 1) + '</span>' +
                '<span class="pw-label">' + esc(s[1]) + '</span></a>';
        }).join('<span class="pw-line"></span>');
        ul.querySelectorAll('a').forEach(function(a) {
            a.addEventListener('click', function(e) {
                e.preventDefault();
                if (self.steps.indexOf(a.dataset.step) <= self.reached) self.go(a.dataset.step);
            });
        });
        el.querySelector('[data-act="back"]').onclick = function() {
            var at = self.steps.indexOf(self.current);
            if (at === 0) bootstrap.Modal.getOrCreateInstance(el).hide();
            else self.go(self.steps[at - 1]);
        };
        el.querySelector('[data-act="next"]').onclick = function() {
            var at = self.steps.indexOf(self.current);
            if (at < self.steps.length - 1) { self.go(self.steps[at + 1]); return; }
            var buttons = el.querySelectorAll('.modal-footer button');
            buttons.forEach(function(b) { b.disabled = true; });
            self.saveStep().then(function() {
                self.done = true;
                bootstrap.Modal.getOrCreateInstance(el).hide();
                if (window.pluginsManager && pluginsManager.refreshInstalled) pluginsManager.refreshInstalled();
            }, function(e) {
                if (e) self.error(e.message);
            }).then(function() { buttons.forEach(function(b) { b.disabled = false; }); });
        };
    };

    Wizard.prototype.show = function(step) {
        var el = this.modal();
        this.current = step;
        var at = this.steps.indexOf(step);
        var self = this;
        el.querySelectorAll('.pw-step').forEach(function(a) {
            var i = self.steps.indexOf(a.dataset.step);
            a.classList.toggle('active', i === at);
            a.classList.toggle('done', i < at || (i <= self.reached && i !== at));
            a.classList.toggle('disabled', i > self.reached);
            a.querySelector('.pw-dot').innerHTML = i < at ? '<i class="bi bi-check-lg"></i>' : String(i + 1);
        });
        // the device table needs the width; settings read better narrow
        el.querySelector('.modal-dialog').classList.toggle('modal-xl', step === 'devices');
        el.querySelector('.modal-dialog').classList.toggle('modal-lg', step !== 'devices');
        var back = el.querySelector('[data-act="back"]');
        back.textContent = at > 0 ? t.back : t.cancel;
        var last = at === this.steps.length - 1;
        // the last step saves on the way out: one step means "Save", several end with "Done"
        el.querySelector('[data-act="next"]').textContent = !last ? t.next : (this.steps.length === 1 ? t.save : t.done);
        this.error('');
        if (this.loginCancel) this.loginCancel();
        this.body = el.querySelector('.wizard-step');
        this.body.onchange = null;
        if (step === 'settings') this.renderSettings();
        else if (step === 'devices') this.renderDevices();
        else if (step === 'dashboards') this.renderDashboards();
        else this.renderCode();
    };

    Wizard.prototype.error = function(msg) {
        this.modal().querySelector('.wizard-error').innerHTML = msg ?
            '<div class="pw-error"><i class="bi bi-exclamation-circle"></i><span>' + esc(msg) + '</span></div>' : '';
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
        if (this.current === 'dashboards') {
            if (!this.checkDashboards()) return Promise.reject(null);
            var access = this.collectDashboards();
            if (JSON.stringify(access) === this.pristine) return Promise.resolve();
            var self = this;
            return this.save({ dashboards: access }).catch(function(e) {
                // vclu's answer by field ("lista.pin"): mark them as the check here does
                Object.keys(e.fields || {}).forEach(function(k) {
                    var input = self.body.querySelector('[data-dfield="' + CSS.escape(k) + '"]');
                    if (input) markField(input, e.fields[k]);
                });
                throw e;
            });
        }
        return Promise.resolve();
    };

    // what is wrong with one field, '' when nothing; vclu checks again on save
    function fieldError(input, f) {
        var v = readField(input);
        if (v === null) {
            // a secret already stored may stay empty
            return f.required && f.type !== 'boolean' && !(f.type === 'secret' && input.dataset.set) ? t.required : '';
        }
        if (f.type === 'host' && !isIP(String(v))) return t.badHost;
        if (f.type === 'number') {
            if (isNaN(v)) return t.required;
            if (f.min !== undefined && v < f.min) return t.min + ' ' + f.min;
            if (f.max !== undefined && v > f.max) return t.max + ' ' + f.max;
        }
        return '';
    }

    function markField(input, msg) {
        input.classList.toggle('is-invalid', !!msg);
        // in the device table the message floats under the cell, so rows keep their height
        var cls = input.closest('td') ? 'invalid-tooltip' : 'invalid-feedback';
        var fb = input.parentNode.querySelector('.' + cls);
        if (msg && !fb) {
            fb = document.createElement('div');
            fb.className = cls;
            input.insertAdjacentElement('afterend', fb);
        }
        if (fb) fb.textContent = msg;
    }

    // marks what is wrong and says where; a marked field clears as soon as it is right
    Wizard.prototype.checkInputs = function(root, fields) {
        var self = this;
        var bad = [];
        root.querySelectorAll('[data-field]').forEach(function(input) {
            var f = fields[input.dataset.field] || {};
            var holder = input.closest('[data-row]') || input.closest('td');
            if (holder && holder.style.display === 'none') return;
            var tr = input.closest('tr');
            if (tr && !tr.querySelector('[data-include]').checked) return; // a row left out is not saved
            var msg = fieldError(input, f);
            markField(input, msg);
            if (!msg) return;
            var where = text(f.label, input.dataset.field);
            if (tr) where += ' (' + t.row + ' ' + (Number(tr.dataset.index) + 1) + ')';
            bad.push({ input: input, where: where });
            if (!input.dataset.watch) {
                input.dataset.watch = '1';
                var recheck = function() {
                    if (!input.classList.contains('is-invalid')) return;
                    var m = fieldError(input, f);
                    markField(input, m);
                    if (!root.querySelector('.is-invalid')) self.error('');
                };
                input.addEventListener('input', recheck);
                input.addEventListener('change', recheck);
            }
        });
        if (bad.length) {
            this.error(t.fixFirst + ': ' + bad.map(function(b) { return b.where; }).join(', '));
            bad[0].input.focus();
        }
        return bad.length === 0;
    };

    function isIP(s) {
        if (s.indexOf(':') >= 0) return /^[0-9a-fA-F:.]+$/.test(s);
        var parts = s.split('.');
        return parts.length === 4 && parts.every(function(p) { return /^\d{1,3}$/.test(p) && Number(p) <= 255; });
    }

    // ---- fields ----

    function optionLabel(f, o) {
        return text((f.optionLabels || {})[o], o);
    }

    function fieldInput(name, f, value, opts) {
        opts = opts || {};
        var size = opts.compact ? ' form-control-sm' : '';
        var attrs = 'data-field="' + esc(name) + '" id="pw-' + esc(name) + '" class="' + (f.type === 'boolean' ? 'form-check-input' : 'form-control' + size) + '"';
        if (opts.compact) attrs = attrs.replace(' id="pw-' + esc(name) + '"', '');
        if (opts.readonly) attrs += ' readonly';
        var options = f.options || f.enum;
        if (options) {
            return '<select ' + attrs.replace('form-control', 'form-select') + '>' +
                (f.required ? '' : '<option value=""></option>') +
                options.map(function(o) {
                    return '<option value="' + esc(o) + '"' + (String(value) === o ? ' selected' : '') + '>' + esc(optionLabel(f, o)) + '</option>';
                }).join('') + '</select>';
        }
        if (f.type === 'boolean') {
            return '<input type="checkbox" role="switch" ' + attrs + (value === true || value === 'true' ? ' checked' : '') + '>';
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
            var label = esc(text(f.label, name)) + (f.required ? ' <span class="text-danger">*</span>' : '');
            var desc = f.description ? '<div class="pw-desc">' + esc(text(f.description)) + '</div>' : '';
            // a field shown by a switch sits under it
            var nested = f.showIf && Object.keys(f.showIf).some(function(k) { return fields[k] && fields[k].type === 'boolean'; });
            var cls = 'pw-field' + (nested ? ' pw-nested' : '');
            if (f.type === 'boolean') {
                html += '<div class="' + cls + ' pw-switch" data-row="' + esc(name) + '">' +
                    '<label class="pw-switch-text" for="pw-' + esc(name) + '"><span class="pw-label">' + label + '</span>' + desc + '</label>' +
                    '<div class="form-check form-switch m-0">' + fieldInput(name, f, v) + '</div></div>';
                return;
            }
            var options = f.options || f.enum;
            if (options && f.optionLabels && options.length <= 4 && f.required) {
                // a few labelled options read better as buttons than a list; one is always chosen
                html += '<div class="' + cls + '" data-row="' + esc(name) + '"><div class="pw-label mb-2">' + esc(text(f.label, name)) + '</div>' +
                    '<select class="d-none" data-field="' + esc(name) + '">' + options.map(function(o) {
                        return '<option value="' + esc(o) + '"' + (String(v) === o ? ' selected' : '') + '></option>';
                    }).join('') + '</select><div class="pw-choices">' + options.map(function(o) {
                        return '<button type="button" class="pw-choice' + (String(v) === o ? ' active' : '') + '" data-choice="' + esc(o) + '">' +
                            esc(optionLabel(f, o)) + '</button>';
                    }).join('') + '</div>' + desc + '</div>';
                return;
            }
            var input = fieldInput(name, f, v, { secretSet: (self.data.secrets || {})[name] });
            if (f.unit) input = '<div class="input-group">' + input + '<span class="input-group-text">' + esc(f.unit) + '</span></div>';
            html += '<div class="' + cls + '" data-row="' + esc(name) + '">' +
                '<label class="pw-label form-label" for="pw-' + esc(name) + '">' + label + '</label>' + input + desc + '</div>';
        });
        if (this.data.login) html += this.loginHTML();
        this.body.innerHTML = html;
        this.bindLogin();
        this.body.querySelectorAll('.pw-choice').forEach(function(b) {
            b.addEventListener('click', function() {
                var row = b.closest('[data-row]');
                row.querySelector('select').value = b.dataset.choice;
                row.querySelectorAll('.pw-choice').forEach(function(o) { o.classList.toggle('active', o === b); });
                refresh();
            });
        });
        var refresh = function() {
            var now = self.collectSettings(true);
            // a hidden field counts as unset, so what depends on it hides too, in any order (as in vclu)
            var hidden = {}, changed = true;
            while (changed) {
                changed = false;
                Object.keys(fields).forEach(function(name) {
                    if (hidden[name] || shown(fields[name], now)) return;
                    hidden[name] = changed = true;
                    delete now[name];
                });
            }
            Object.keys(fields).forEach(function(name) {
                self.body.querySelector('[data-row="' + CSS.escape(name) + '"]').style.display = hidden[name] ? 'none' : '';
            });
        };
        this.body.onchange = refresh;
        refresh();
        this.pristine = JSON.stringify(this.collectSettings(false));
    };

    // ---- sign-in (plugin:login) ----

    Wizard.prototype.loginHTML = function() {
        var body = '<button type="button" class="btn btn-primary" data-act="login"><i class="bi bi-qr-code me-1"></i>' + esc(t.signIn) + '</button>';
        return '<div class="pw-field pw-login"><div class="pw-label">' + esc(t.login) + '</div>' +
            '<div class="pw-desc mb-2">' + esc(t.loginDesc) + '</div>' + body + '<div class="pw-login-state mt-2"></div></div>';
    };

    Wizard.prototype.bindLogin = function() {
        var self = this;
        var b = this.body.querySelector('[data-act="login"]');
        if (b) b.addEventListener('click', function() { self.login(); });
    };

    // starts the sign-in and shows what the plugin gives until it is done, fails or is cancelled
    Wizard.prototype.login = function() {
        var self = this;
        var box = this.body.querySelector('.pw-login-state');
        var button = this.body.querySelector('[data-act="login"]');
        var modal = this.modal();
        var req = null, timer = null;
        var stop = function() {
            if (timer) clearTimeout(timer);
            timer = null;
            if (button) button.disabled = false;
        };
        var cancel = function() {
            if (req) fetch(self.base + '/login/' + encodeURIComponent(req), { method: 'DELETE' });
            req = null;
            self.loginCancel = null;
            stop();
            box.innerHTML = '';
        };
        var draw = function(r) {
            var html = '';
            if (r.qr) html += '<div class="small mb-1">' + esc(t.scanQr) + '</div><img class="pw-qr" alt="QR" src="' + esc(r.qr) + '">';
            if (r.code) {
                html += '<div class="small mb-1">' + esc(t.openLink) + '</div>';
                if (r.url) html += '<div><a href="' + esc(r.url) + '" target="_blank" rel="noopener noreferrer">' + esc(r.url) + '</a></div>';
                html += '<div class="pw-login-code fs-4 font-monospace">' + esc(r.code) + '</div>';
            }
            if (r.hint) html += '<div class="pw-desc">' + esc(r.hint) + '</div>';
            html += '<div class="small text-muted mt-1"><span class="spinner-border spinner-border-sm me-1"></span>' + esc(t.signingIn) +
                ' <button type="button" class="btn btn-link btn-sm" data-act="login-cancel">' + esc(t.cancel) + '</button></div>';
            box.innerHTML = html;
            box.querySelector('[data-act="login-cancel"]').addEventListener('click', cancel);
        };
        var poll = function() {
            timer = null;
            var mine = req;
            api(self.base + '/login/' + encodeURIComponent(mine)).then(function(r) {
                if (req !== mine) return; // cancelled while this poll was on its way
                if (r.state === 'pending') {
                    draw(r);
                    timer = setTimeout(poll, 1500);
                    return;
                }
                req = null;
                self.loginCancel = null;
                stop();
                if (r.state === 'done') {
                    box.innerHTML = '<div class="text-success"><i class="bi bi-check-circle me-1"></i>' + esc(t.signedIn) + '</div>';
                } else {
                    box.innerHTML = '';
                    self.error(r.error || r.state);
                }
            }).catch(function(e) {
                if (req !== mine) return;
                req = null;
                self.loginCancel = null;
                stop();
                self.error(e.message);
            });
        };
        if (button) button.disabled = true;
        this.error('');
        box.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
        // leaving the step or closing the wizard ends the sign-in (show() and hide call this)
        this.loginCancel = cancel;
        modal.addEventListener('hide.bs.modal', function() { if (self.loginCancel === cancel) cancel(); }, { once: true });
        api(this.base + '/login', { method: 'POST' }).then(function(r) {
            if (self.loginCancel !== cancel) {
                // the step was left while it was starting
                fetch(self.base + '/login/' + encodeURIComponent(r.req), { method: 'DELETE' });
                return;
            }
            req = r.req;
            poll();
        }).catch(function(e) {
            stop();
            box.innerHTML = '';
            self.error(e.message);
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
            if (!body.devices) return;
            // discovered rows not added yet stay on the list
            var left = self.rows.filter(function(r) { return !r.include; });
            self.rows = (data.devices || []).map(function(v) { return { values: v, include: true, saved: true }; }).concat(left);
        });
    };

    // ---- dashboards: who may open them, written as code (Plugin.getPlugin(id):dashboard(id)) ----

    var AUTHS = ['session', 'pin', 'public', 'off'];
    var authLabel = { session: 'authSession', pin: 'authPin', public: 'authPublic', off: 'authOff' };

    function splitRoles(s) {
        return String(s || '').split(',').map(function(r) { return r.trim().toLowerCase(); }).filter(Boolean);
    }

    Wizard.prototype.renderDashboards = function() {
        var self = this;
        var known = this.data.roles || [];
        var html = '<p class="pw-desc mt-0 mb-3">' + esc(t.dashCode) + '</p>';
        this.data.dashboards.forEach(function(d) {
            var a = (self.data.dashboardAccess || {})[d.id] || {};
            var id = esc(d.id);
            var auth = AUTHS.indexOf(a.auth) >= 0 ? a.auth : 'session';
            html += '<div class="pw-dash" data-dash="' + id + '">' +
                '<div class="pw-dash-head"><i class="bi bi-grid-1x2"></i><div><div class="pw-label">' + esc(text(d.name, d.id)) + '</div>' +
                '<div class="pw-sub">' + esc(d.path) + '</div>' + (d.description ? '<div class="pw-desc">' + esc(text(d.description)) + '</div>' : '') + '</div></div>' +
                '<div class="pw-field"><label class="pw-label form-label" for="pw-d-' + id + '-name">' + esc(t.dashName) + '</label>' +
                '<input type="text" class="form-control" maxlength="60" id="pw-d-' + id + '-name" data-dfield="' + id + '.name" value="' + esc(a.name || text(d.name, d.id)) + '"></div>' +
                '<div class="pw-field"><div class="pw-label mb-2">' + esc(t.who) + '</div><div class="pw-choices">' +
                AUTHS.map(function(o) {
                    return '<button type="button" class="pw-choice' + (auth === o ? ' active' : '') + '" data-auth="' + o + '">' + esc(t[authLabel[o]]) + '</button>';
                }).join('') + '</div><div class="pw-desc" data-auth-desc></div></div>' +
                '<div class="pw-switch" data-show="on">' +
                '<label class="pw-switch-text" for="pw-d-' + id + '-remote"><span class="pw-label">' + esc(t.app) + '</span><div class="pw-desc">' + esc(t.appDesc) + '</div></label>' +
                '<div class="form-check form-switch m-0"><input type="checkbox" role="switch" class="form-check-input" id="pw-d-' + id + '-remote" data-dfield="' + id + '.remote"' + (a.remote ? ' checked' : '') + '></div></div>' +
                '<div class="pw-switch pw-nested" data-show="remote">' +
                '<label class="pw-switch-text" for="pw-d-' + id + '-rpin"><span class="pw-label">' + esc(t.appPin) + '</span></label>' +
                '<div class="form-check form-switch m-0"><input type="checkbox" role="switch" class="form-check-input" id="pw-d-' + id + '-rpin" data-dfield="' + id + '.remotePin"' + (a.remotePin ? ' checked' : '') + '></div></div>' +
                '<div class="pw-field pw-nested" data-show="remote"><label class="pw-label form-label" for="pw-d-' + id + '-roles">' + esc(t.roles) + '</label>' +
                '<input type="text" class="form-control" id="pw-d-' + id + '-roles" data-dfield="' + id + '.roles" placeholder="domownicy, gosia" value="' + esc((a.roles || []).join(', ')) + '">' +
                '<div class="pw-desc">' + esc(t.rolesDesc) + '</div>' +
                (known.length ? '<div class="pw-chips"><span class="pw-desc m-0">' + esc(t.rolesKnown) + '</span>' + known.map(function(r) {
                    return '<button type="button" class="pw-chip" data-role="' + esc(r) + '">' + esc(r) + '</button>';
                }).join('') + '</div>' : '') + '</div>' +
                '<div class="pw-field" data-show="pin"><label class="pw-label form-label" for="pw-d-' + id + '-pin">' + esc(t.pin) + '</label>' +
                '<input type="text" class="form-control pw-pin" inputmode="numeric" maxlength="4" autocomplete="off" id="pw-d-' + id + '-pin" data-dfield="' + id + '.pin" value="' + esc(a.pin || '') + '">' +
                '<div class="pw-desc">' + esc(t.pinDesc) + '</div></div>' +
                '</div>';
        });
        this.body.innerHTML = html;
        this.body.querySelectorAll('.pw-dash').forEach(function(box) {
            box.querySelectorAll('.pw-choice').forEach(function(b) {
                b.addEventListener('click', function() {
                    box.querySelectorAll('.pw-choice').forEach(function(o) { o.classList.toggle('active', o === b); });
                    self.refreshDash(box);
                });
            });
            box.querySelectorAll('.pw-chip').forEach(function(c) {
                c.addEventListener('click', function() {
                    var input = box.querySelector('[data-dfield$=".roles"]');
                    var list = splitRoles(input.value);
                    if (list.indexOf(c.dataset.role) < 0) list.push(c.dataset.role);
                    input.value = list.join(', ');
                    self.refreshDash(box);
                });
            });
            box.addEventListener('input', function() { self.refreshDash(box); });
            box.addEventListener('change', function() { self.refreshDash(box); });
            self.refreshDash(box);
        });
        this.pristine = JSON.stringify(this.collectDashboards());
    };

    // what shows follows the choices; a marked field clears as soon as it is right
    Wizard.prototype.refreshDash = function(box) {
        var auth = box.querySelector('.pw-choice.active').dataset.auth;
        var remote = box.querySelector('[data-dfield$=".remote"]').checked;
        var rpin = box.querySelector('[data-dfield$=".remotePin"]').checked;
        var on = { on: auth !== 'off', remote: auth !== 'off' && remote, pin: auth === 'pin' || (auth !== 'off' && remote && rpin) };
        box.querySelectorAll('[data-show]').forEach(function(el) { el.style.display = on[el.dataset.show] ? '' : 'none'; });
        var desc = auth === 'public' ? t.authPublicDesc : auth === 'off' ? t.authOffDesc : '';
        box.querySelector('[data-auth-desc]').textContent = desc;
        box.querySelector('[data-auth-desc]').style.display = desc ? '' : 'none';
        var roles = splitRoles(box.querySelector('[data-dfield$=".roles"]').value);
        box.querySelectorAll('.pw-chip').forEach(function(c) { c.classList.toggle('active', roles.indexOf(c.dataset.role) >= 0); });
        box.querySelectorAll('.is-invalid').forEach(function(input) { markField(input, dashFieldError(input)); });
        if (!this.body.querySelector('.is-invalid')) this.error('');
    };

    function dashFieldError(input) {
        var name = input.dataset.dfield.split('.').pop();
        var row = input.closest('[data-show]');
        if (row && row.style.display === 'none') return '';
        if (name === 'pin' && !/^\d{4}$/.test(input.value.trim())) return t.badPin;
        if (name === 'roles' && splitRoles(input.value).some(function(r) { return !/^[a-z0-9][a-z0-9_-]{0,31}$/.test(r); })) return t.badRole;
        if (name === 'name' && input.value.trim().length > 60) return t.max + ' 60';
        return '';
    }

    Wizard.prototype.checkDashboards = function() {
        var bad = [];
        this.body.querySelectorAll('[data-dfield]').forEach(function(input) {
            if (input.type === 'checkbox') return;
            var msg = dashFieldError(input);
            markField(input, msg);
            if (msg) bad.push(input);
        });
        if (bad.length) {
            this.error(t.fixFirst);
            bad[0].focus();
        }
        return bad.length === 0;
    };

    Wizard.prototype.collectDashboards = function() {
        var out = {};
        this.body.querySelectorAll('.pw-dash').forEach(function(box) {
            var v = function(name) { return box.querySelector('[data-dfield$=".' + name + '"]'); };
            var a = {
                name: v('name').value.trim(),
                auth: box.querySelector('.pw-choice.active').dataset.auth,
                remote: v('remote').checked,
                remotePin: v('remotePin').checked,
                roles: splitRoles(v('roles').value),
            };
            if (a.auth === 'pin' || (a.remote && a.remotePin)) a.pin = v('pin').value.trim();
            out[box.dataset.dash] = a;
        });
        return out;
    };

    // ---- step 2 ----

    Wizard.prototype.renderDevices = function() {
        var self = this;
        var d = this.form.devices;
        var names = Object.keys(d.fields);
        var html = '<div class="d-flex gap-2 mb-3">';
        if (d.discover) html += '<button class="btn btn-primary" data-act="discover"><i class="bi bi-search me-1"></i>' + esc(t.discover) + '</button>';
        html += '<button class="btn btn-outline-secondary" data-act="add"><i class="bi bi-plus-lg me-1"></i>' + esc(t.addRow) + '</button>' +
            '<span class="wizard-status small text-muted align-self-center"></span></div>';
        if (!this.rows.length) {
            html += '<div class="pw-empty"><i class="bi bi-hdd-network"></i><div>' + esc(t.noDevices) + '</div></div>';
        } else {
            html += '<div class="table-responsive pw-table"><table class="table table-sm align-middle"><thead><tr><th>' + esc(t.add) + '</th>' +
                names.map(function(n) { return '<th>' + esc(text(d.fields[n].label, n)) + '</th>'; }).join('') +
                '<th></th></tr></thead><tbody>';
            this.rows.forEach(function(row, i) {
                html += '<tr data-index="' + i + '"><td class="text-nowrap"><input type="checkbox" class="form-check-input" data-include' +
                    (row.include ? ' checked' : '') + '>' + (row.isNew ? ' <span class="badge text-bg-info">' + esc(t.isNew) + '</span>' : '') + '</td>' +
                    names.map(function(n) {
                        var f = d.fields[n];
                        var v = row.values[n] !== undefined ? row.values[n] : f.default;
                        return '<td>' + fieldInput(n, f, v, { readonly: f.readonly && !row.manual, compact: true }) + '</td>';
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
                html += '<pre class="wizard-diff border rounded p-2 small" style="max-height:50vh;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere">' + diffHTML(r.current, r.proposed) + '</pre>' +
                    '<button class="btn btn-primary btn-sm" data-act="write">' + esc(t.write) + '</button>';
            }
            self.body.innerHTML = html;
            var b = self.body.querySelector('[data-act="write"]');
            if (b) b.addEventListener('click', function() {
                var pre = self.body.querySelector('.wizard-diff');
                var changes = r.current ? t.writeChanges.replace('{add}', pre.querySelectorAll('.text-success').length)
                    .replace('{del}', pre.querySelectorAll('.text-danger').length) : t.writeNew;
                if (!confirm(t.writeConfirm.replace('{path}', r.path).replace('{changes}', changes))) return;
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
        // meta: {name, version} from the plugins page, for the heading
        open: function(id, meta) { return new Wizard(id, meta).open(); }
    };
})();
