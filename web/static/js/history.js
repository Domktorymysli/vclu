/**
 * Tools -> History and audit: browse state changes, show the store status,
 * edit settings and run the admin actions.
 */
(function() {
    'use strict';
    var T = window.historyT || {};
    var esc = VCLU.esc;
    var $ = function(id) { return document.getElementById(id); };
    var cursor = '';
    var skipped = 0;
    var lastParams = null; // filters of the first page; "Older" pages must not pick up edited fields

    function escAttr(v) { return esc(v).replace(/"/g, '&quot;'); }

    function filterParams() {
        var p = new URLSearchParams();
        [['path', 'fPath'], ['origin', 'fOrigin'], ['actor', 'fActor'], ['q', 'fText']].forEach(function(f) {
            var v = $(f[1]).value.trim();
            if (v) p.set(f[0], v);
        });
        [['since', 'fSince'], ['until', 'fUntil']].forEach(function(f) {
            var v = $(f[1]).value;
            if (v) p.set(f[0], String(new Date(v).getTime())); // local time from the picker
        });
        p.set('limit', '100');
        return p;
    }

    function fmtValue(v) {
        if (v === null || v === undefined) return '—';
        if (typeof v === 'object') return JSON.stringify(v);
        return String(v);
    }

    function row(e) {
        return '<tr>' +
            '<td class="text-nowrap">' + esc(new Date(e.atMs).toLocaleString()) + '</td>' +
            '<td><a href="#" class="history-path" data-path="' + escAttr(e.path) + '">' + esc(e.path) + '</a></td>' +
            '<td class="text-nowrap">' + esc(fmtValue(e.old)) + ' → <strong>' + esc(fmtValue(e.value)) + '</strong></td>' +
            '<td><span class="badge text-bg-secondary">' + esc(e.origin) + '</span></td>' +
            '<td>' + esc(e.actor) + (e.correlationId ? ' <span class="text-muted small">' + esc(e.correlationId) + '</span>' : '') + '</td>' +
            '</tr>';
    }

    function load(append) {
        var p;
        if (append && cursor && lastParams) {
            p = new URLSearchParams(lastParams);
            p.set('cursor', cursor);
        } else {
            p = filterParams();
            lastParams = p.toString();
        }
        $('historyError').style.display = 'none';
        VCLU.fetchJSON('/api/history?' + p.toString()).then(function(res) {
            var body = $('historyBody');
            var html = (res.entries || []).map(row).join('');
            body.innerHTML = append ? body.innerHTML + html : html;
            var any = body.children.length > 0;
            $('historyTable').style.display = any ? '' : 'none';
            $('historyEmpty').style.display = any ? 'none' : '';
            cursor = res.next || '';
            // an empty page with next only means the 14-day read budget ran out
            if (cursor && !(res.entries || []).length && (skipped = (append ? skipped : 0) + 1) < 12) {
                load(true);
                return;
            }
            skipped = 0;
            $('historyMore').style.display = cursor ? '' : 'none';
        }).catch(function(err) {
            $('historyError').textContent = err.message;
            $('historyError').style.display = '';
        });
    }

    function fmtBytes(n) {
        if (n > 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
        if (n > 1024) return (n / 1024).toFixed(0) + ' kB';
        return n + ' B';
    }

    function loadStatus() {
        VCLU.fetchJSON('/api/history/status').then(function(st) {
            var lines = [];
            lines.push(T.memory + ': <strong>' + st.memoryEntries + '</strong>' +
                (st.oldestInMemory ? ' (' + T.memory_since + ' ' + esc(new Date(st.oldestInMemory).toLocaleString()) + ')' : ''));
            if (st.diskEnabled) {
                lines.push(T.disk + ': <strong>' + fmtBytes(st.diskUsageBytes) + '</strong>' +
                    (st.diskOldestDay ? ' (' + T.memory_since + ' ' + esc(st.diskOldestDay) + ')' : ''));
                lines.push(T.pending + ': ' + st.pendingWrites +
                    (st.lastFlushAt ? ' · ' + T.last_write + ': ' + esc(new Date(st.lastFlushAt).toLocaleString()) : ''));
                if (st.lostEntries) lines.push('<span class="text-danger">' + T.lost + ': ' + st.lostEntries + '</span>');
                if (st.skippedRecords) lines.push(T.skipped + ': ' + st.skippedRecords);
                if (st.lastFlushError) lines.push('<span class="text-danger">' + T.write_error + ': ' + esc(st.lastFlushError) + '</span>');
                if (st.diskOverLimit) lines.push('<span class="text-warning">' + T.over_limit + '</span>');
            } else {
                lines.push('<span class="text-muted">' + T.disk_off + '</span>');
            }
            $('historyStatus').innerHTML = lines.join('<br>');
        }).catch(function(err) {
            $('historyStatus').textContent = err.message;
        });
    }

    var fields = {
        enabled: ['sEnabled', 'bool'], diskEnabled: ['sDiskEnabled', 'bool'],
        memoryHours: ['sMemoryHours'], maxEntriesPerObject: ['sPerObject'], maxEntriesTotal: ['sTotal'],
        retentionDays: ['sRetention'], maxDiskMB: ['sMaxDisk'], flushIntervalSeconds: ['sFlush']
    };
    var defaults = null;

    function fillSettings(s) {
        Object.keys(fields).forEach(function(k) {
            var el = $(fields[k][0]);
            if (fields[k][1] === 'bool') el.checked = !!s[k]; else el.value = s[k];
        });
    }

    function loadSettings() {
        VCLU.fetchJSON('/api/history/settings').then(function(res) {
            defaults = res.defaults;
            fillSettings(res.settings);
            if (res.restartRequired) VCLU.showToast(T.restart, 'warning');
        });
    }

    function saveSettings() {
        var body = {};
        Object.keys(fields).forEach(function(k) {
            var el = $(fields[k][0]);
            body[k] = fields[k][1] === 'bool' ? el.checked : parseInt(el.value, 10);
        });
        VCLU.fetchJSON('/api/history/settings', {
            method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
        }).then(function(res) {
            fillSettings(res.settings);
            VCLU.showToast(res.restartRequired ? T.restart : T.saved, 'success');
            loadStatus();
        }).catch(function(err) { VCLU.showToast(err.message, 'danger'); });
    }

    function post(url, confirmText) {
        if (confirmText && !window.confirm(confirmText)) return;
        VCLU.fetchJSON(url, {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ confirm: true })
        }).then(function() {
            VCLU.showToast(T.saved, 'success');
            loadStatus();
            load(false);
        }).catch(function(err) { VCLU.showToast(err.message, 'danger'); });
    }

    document.addEventListener('DOMContentLoaded', function() {
        $('historyFilters').addEventListener('submit', function() { load(false); });
        $('historyMore').addEventListener('click', function() { load(true); });
        $('historyBody').addEventListener('click', function(ev) {
            var a = ev.target.closest('.history-path');
            if (!a) return;
            ev.preventDefault();
            $('fPath').value = a.dataset.path;
            load(false);
        });
        $('historySave').addEventListener('click', saveSettings);
        $('historyDefaults').addEventListener('click', function() { if (defaults) fillSettings(defaults); });
        $('historyFlush').addEventListener('click', function() { post('/api/history/flush'); });
        $('historyClearMemory').addEventListener('click', function() { post('/api/history/clear-memory', T.confirm_memory); });
        $('historyClearDisk').addEventListener('click', function() { post('/api/history/clear-disk', T.confirm_disk); });
        load(false);
        loadStatus();
        loadSettings();
    });
})();
