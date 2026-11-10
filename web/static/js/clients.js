/**
 * API clients: programs using the device API with their own token, profiles and roles
 * (pkg/web/clients.go).
 */
(function() {
    'use strict';

    var clients = [];
    var profiles = [];
    var roles = [];
    var editingId = null;

    function t(key, fallback) {
        return (window.CLIENTS_I18N && CLIENTS_I18N[key]) || fallback || key;
    }

    function api(method, url, body) {
        var opts = { method: method, headers: {} };
        if (body !== undefined) {
            opts.headers['Content-Type'] = 'application/json';
            opts.body = JSON.stringify(body);
        }
        return fetch(url, opts).then(function(r) { return r.json(); });
    }

    function load() {
        api('GET', '/api/clients').then(function(data) {
            if (data.error) { VCLU.showToast(data.error, 'danger'); return; }
            clients = data.clients || [];
            roles = data.roles || [];
            // no panel password: anyone on the network could give themselves a client
            document.getElementById('clientsAuthRequired').style.display = data.authEnabled ? 'none' : '';
            document.getElementById('btnAddClient').disabled = !data.authEnabled;
            render();
        });
        api('GET', '/api/access/profiles').then(function(data) {
            profiles = (data.profiles || []).map(function(p) { return p.name; })
                .filter(function(n) { return n.charAt(0) !== '_'; });
        });
    }

    function badges(list, cls, empty) {
        if (!list || list.length === 0) return '<span class="text-muted">' + VCLU.esc(empty) + '</span>';
        return list.map(function(x) { return '<span class="badge ' + cls + ' me-1">' + VCLU.esc(x) + '</span>'; }).join('');
    }

    function day(unix) {
        return new Date(unix * 1000).toISOString().slice(0, 10);
    }

    function render() {
        var box = document.getElementById('clientsList');
        if (clients.length === 0) {
            box.innerHTML = '<div class="text-center text-muted py-4">' +
                '<i class="bi bi-key fs-1 d-block mb-2 opacity-50"></i>' +
                '<div class="mb-1">' + VCLU.esc(t('none')) + '</div>' +
                '<div class="small">' + VCLU.esc(t('empty_hint')) + '</div></div>';
            return;
        }
        var html = '<div class="list-group">';
        clients.forEach(function(cl) {
            var exp = cl.expired
                ? '<span class="badge bg-danger">' + VCLU.esc(t('expired')) + '</span>'
                : '<span class="text-muted">' + VCLU.esc(t('expires')) + ': ' + VCLU.esc(cl.expiresAt ? day(cl.expiresAt) : t('never')) + '</span>';
            html += '<div class="list-group-item py-3"><div class="d-flex justify-content-between align-items-start gap-3">';
            html += '<div class="flex-grow-1">';
            html += '<div class="d-flex align-items-baseline gap-2 mb-2"><h6 class="mb-0">' + VCLU.esc(cl.name) + '</h6>' +
                '<code class="small text-muted">client:' + VCLU.esc(cl.id) + '</code></div>';
            html += '<div class="small mb-1"><span class="text-muted me-1">' + VCLU.esc(t('profiles')) + ':</span>' + badges(cl.profiles, 'bg-primary', t('no_profiles')) + '</div>';
            html += '<div class="small mb-1"><span class="text-muted me-1">' + VCLU.esc(t('roles')) + ':</span>' + badges(cl.roles, 'border border-info text-info', t('no_roles')) + '</div>';
            html += '<div class="small">' + exp + '</div>';
            html += '</div><div class="btn-group btn-group-sm flex-shrink-0">';
            html += '<button class="btn btn-outline-secondary" data-act="edit" data-id="' + VCLU.esc(cl.id) + '" title="' + VCLU.esc(t('edit')) + '"><i class="bi bi-pencil"></i></button>';
            html += '<button class="btn btn-outline-secondary" data-act="regen" data-id="' + VCLU.esc(cl.id) + '" title="' + VCLU.esc(t('new_token')) + '"><i class="bi bi-qr-code"></i></button>';
            html += '<button class="btn btn-outline-danger" data-act="delete" data-id="' + VCLU.esc(cl.id) + '" title="' + VCLU.esc(t('delete')) + '"><i class="bi bi-trash"></i></button>';
            html += '</div></div></div>';
        });
        box.innerHTML = html + '</div>';
    }

    function checks(id, all, selected, cls) {
        var box = document.getElementById(id);
        var names = all.slice();
        selected.forEach(function(s) { if (names.indexOf(s) === -1) names.push(s); });
        if (names.length === 0) { box.innerHTML = '<p class="text-muted small mb-1">—</p>'; return; }
        box.innerHTML = names.map(function(n, i) {
            var on = selected.indexOf(n) !== -1 ? ' checked' : '';
            return '<div class="form-check form-check-inline"><input class="form-check-input ' + cls + '" type="checkbox" id="' + cls + i + '" value="' + VCLU.esc(n) + '"' + on + '>' +
                '<label class="form-check-label" for="' + cls + i + '">' + VCLU.esc(n) + '</label></div>';
        }).join('');
    }

    function picked(cls) {
        return Array.prototype.map.call(document.querySelectorAll('.' + cls + ':checked'), function(cb) { return cb.value; });
    }

    function open(cl) {
        editingId = cl ? cl.id : null;
        document.getElementById('clientModalTitle').textContent = cl ? t('edit') + ': ' + cl.name : t('add');
        document.getElementById('clientName').value = cl ? cl.name : '';
        document.getElementById('clientExpires').value = cl && cl.expiresAt ? day(cl.expiresAt) : '';
        document.getElementById('clientForm').style.display = '';
        document.getElementById('clientTokenResult').style.display = 'none';
        var save = document.getElementById('btnSaveClient');
        save.textContent = cl ? t('save') : t('create');
        save.style.display = '';
        checks('clientProfiles', profiles, cl ? cl.profiles : [], 'client-prof');
        checks('clientRoles', roles, cl ? cl.roles : [], 'client-role');
        bootstrap.Modal.getOrCreateInstance(document.getElementById('clientModal')).show();
    }

    // The token with where to reach vCLU, as vCLU gave it (clientConnection in clients_panel.go):
    // a QR code for an app and the same JSON to paste. Shown once.
    function showToken(data) {
        var text = data.connection || '';
        try { text = JSON.stringify(JSON.parse(text), null, 2); } catch (e) { /* shown as given */ }
        document.getElementById('clientConnection').value = text;
        var img = document.getElementById('clientQR');
        if (data.qr) {
            img.src = data.qr;
            img.style.display = '';
        } else {
            img.removeAttribute('src');
            img.style.display = 'none';
        }
        document.getElementById('clientForm').style.display = 'none';
        document.getElementById('clientTokenResult').style.display = '';
        document.getElementById('btnSaveClient').style.display = 'none';
        bootstrap.Modal.getOrCreateInstance(document.getElementById('clientModal')).show();
    }

    function save() {
        var name = document.getElementById('clientName').value.trim();
        if (!name) { VCLU.showToast(t('name_required'), 'warning'); return; }
        var exp = document.getElementById('clientExpires').value;
        var body = {
            name: name,
            profiles: picked('client-prof'),
            roles: picked('client-role'),
            // the end of the chosen day, local time
            expiresAt: exp ? Math.floor(new Date(exp + 'T23:59:59').getTime() / 1000) : 0
        };
        var req = editingId ? api('PUT', '/api/clients/' + encodeURIComponent(editingId), body) : api('POST', '/api/clients', body);
        req.then(function(data) {
            if (data.error) { VCLU.showToast(data.error, 'danger'); return; }
            if (data.token) {
                showToken(data);
            } else {
                bootstrap.Modal.getInstance(document.getElementById('clientModal')).hide();
            }
            load();
        });
    }

    function act(e) {
        var b = e.target.closest('button[data-act]');
        if (!b) return;
        var cl = clients.find(function(x) { return x.id === b.dataset.id; });
        if (!cl) return;
        if (b.dataset.act === 'edit') {
            open(cl);
        } else if (b.dataset.act === 'regen') {
            if (!confirm(t('regen_confirm'))) return;
            api('POST', '/api/clients/' + encodeURIComponent(cl.id) + '/regenerate').then(function(data) {
                if (data.error) { VCLU.showToast(data.error, 'danger'); return; }
                showToken(data);
            });
        } else if (b.dataset.act === 'delete') {
            if (!confirm(t('delete_confirm'))) return;
            api('DELETE', '/api/clients/' + encodeURIComponent(cl.id)).then(function(data) {
                if (data.error) { VCLU.showToast(data.error, 'danger'); return; }
                load();
            });
        }
    }

    document.addEventListener('DOMContentLoaded', function() {
        load();
        document.getElementById('btnAddClient').addEventListener('click', function() { open(null); });
        document.getElementById('btnSaveClient').addEventListener('click', save);
        document.getElementById('clientsList').addEventListener('click', act);
        document.getElementById('clientModal').addEventListener('hidden.bs.modal', function() {
            var img = document.getElementById('clientQR');
            img.removeAttribute('src');
            document.getElementById('clientConnection').value = '';
        });
        document.getElementById('btnCopyClient').addEventListener('click', function() {
            var box = document.getElementById('clientConnection');
            box.select();
            document.execCommand('copy');
            this.textContent = t('copied');
            var btn = this;
            setTimeout(function() { btn.textContent = t('copy'); }, 2000);
        });
    });
})();
