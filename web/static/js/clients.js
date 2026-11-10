/**
 * API clients: programs using the device API with their own token, profiles and roles
 * (pkg/web/clients.go).
 */
(function() {
    'use strict';

    var clients = [];
    var profiles = [];
    var roles = [];
    var lan = null;
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
            lan = data.lan || null;
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
            box.innerHTML = '<p class="text-muted small">' + VCLU.esc(t('none')) + '</p>';
        } else {
            var html = '<div class="list-group">';
            clients.forEach(function(cl) {
                var exp = cl.expiresAt ? day(cl.expiresAt) : t('never');
                html += '<div class="list-group-item"><div class="d-flex justify-content-between align-items-start"><div>';
                html += '<h6 class="mb-1">' + VCLU.esc(cl.name) + (cl.expired ? ' <span class="badge bg-danger">' + VCLU.esc(t('expired')) + '</span>' : '') + '</h6>';
                html += '<small class="text-muted">client:' + VCLU.esc(cl.id) + '</small>';
                html += '<div class="mt-1">' + VCLU.esc(t('profiles')) + ': ' + badges(cl.profiles, 'bg-primary', t('no_profiles')) + '</div>';
                html += '<div class="mt-1">' + VCLU.esc(t('roles')) + ': ' + badges(cl.roles, 'bg-info text-dark', t('no_roles')) + '</div>';
                html += '<small class="text-muted">' + VCLU.esc(t('expires')) + ': ' + VCLU.esc(exp) + ' · ' + VCLU.esc(t('created')) + ': ' + VCLU.esc(cl.createdAt) + '</small>';
                html += '</div><div class="btn-group btn-group-sm">';
                html += '<button class="btn btn-outline-primary" data-act="edit" data-id="' + VCLU.esc(cl.id) + '" title="' + VCLU.esc(t('edit')) + '"><i class="bi bi-pencil"></i></button>';
                html += '<button class="btn btn-outline-warning" data-act="regen" data-id="' + VCLU.esc(cl.id) + '"><i class="bi bi-key"></i></button>';
                html += '<button class="btn btn-outline-danger" data-act="delete" data-id="' + VCLU.esc(cl.id) + '"><i class="bi bi-trash"></i></button>';
                html += '</div></div></div>';
            });
            box.innerHTML = html + '</div>';
        }
        var lanBox = document.getElementById('clientsLan');
        if (lan && lan.addresses && lan.addresses.length) {
            lanBox.textContent = t('lan') + ': https://' + lan.addresses.join(', https://') + ' · pin ' + lan.pin;
        }
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

    // The token with where to reach vCLU, for the client's configuration: shown once.
    function showToken(name, token) {
        var conn = { v: 1, name: name, token: token };
        if (lan) { conn.addresses = lan.addresses; conn.pin = lan.pin; }
        document.getElementById('clientConnection').value = JSON.stringify(conn, null, 2);
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
                showToken(name, data.token);
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
                showToken(cl.name, data.token);
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
