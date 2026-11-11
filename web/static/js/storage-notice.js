/**
 * Disk space banner: shown under the top bar when /api/storage says the disk is low or
 * nearly full; it names the largest part and links to the Storage card (#196).
 */
(function() {
    'use strict';

    var script = document.getElementById('storageNoticeScript');
    var t = (script && script.dataset) || {};
    var lang = document.documentElement.lang || undefined;

    function number(n, digits) {
        return Number(n).toLocaleString(lang, { maximumFractionDigits: digits });
    }

    function size(bytes) {
        var units = ['B', 'KB', 'MB', 'GB', 'TB'];
        var i = 0;
        while (bytes >= 1024 && i < units.length - 1) {
            bytes /= 1024;
            i++;
        }
        return number(bytes, i ? 1 : 0) + ' ' + units[i];
    }

    function fill(text, values) {
        return String(text || '').replace(/\{(\w+)\}/g, function(m, k) {
            return k in values ? values[k] : m;
        });
    }

    function partName(name) {
        var key = 'part' + name.charAt(0).toUpperCase() + name.slice(1);
        return t[key] || name;
    }

    function show(data) {
        if (!data || !data.ready || (data.level !== 'low' && data.level !== 'critical')) return;
        var header = document.querySelector('.main-content > .topbar');
        if (!header) return;
        var critical = data.level === 'critical';
        var fs = data.fileSystem || {};
        var box = document.createElement('div');
        box.className = 'storage-banner alert mb-0 rounded-0 ' + (critical ? 'alert-danger' : 'alert-warning');
        box.setAttribute('role', 'alert');
        var icon = document.createElement('i');
        icon.className = 'bi ' + (critical ? 'bi-exclamation-octagon-fill' : 'bi-exclamation-triangle-fill');
        box.appendChild(icon);
        var text = fill(critical ? t.critical : t.low, { free: size(fs.freeBytes || 0), percent: number(fs.freePercent || 0, 1) });
        var part = (data.parts || []).filter(function(p) { return p.name === data.largest; })[0];
        if (part && part.bytes > 0) {
            text += ' ' + fill(t.largest, { part: partName(part.name), size: size(part.bytes) });
        }
        box.appendChild(document.createTextNode(' ' + text + ' '));
        var link = document.createElement('a');
        link.href = '/stats#storage';
        link.className = 'alert-link';
        link.textContent = t.details || 'Details';
        box.appendChild(link);
        header.insertAdjacentElement('afterend', box);
    }

    fetch('/api/storage', { credentials: 'same-origin' })
        .then(function(r) { return r.ok ? r.json() : null; })
        .then(show)
        .catch(function() { /* no banner */ });
})();
