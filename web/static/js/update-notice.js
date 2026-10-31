/**
 * New-version badge: shown in .topbar-actions when /api/update says an update is available.
 * Release notes arrive as notes_html, already escaped and rendered by the server.
 */
(function() {
    'use strict';

    var script = document.getElementById('updateNoticeScript');
    var t = (script && script.dataset) || {};
    var HIDDEN_KEY = 'vclu-update-hidden';
    var DOCS_URL = 'https://docs.vclu.pl/docs/getting-started/upgrade/';
    var CMD_SERVICE = 'curl -sSL https://raw.githubusercontent.com/Domktorymysli/vclu/main/deploy/update.sh | sudo bash';
    var CMD_DOCKER = 'docker pull ghcr.io/domktorymysli/vclu:latest';
    var FULL = 3; // newest releases shown with their summary; older ones as one line each

    var badge = null;
    var modal = null;

    function el(tag, cls, text) {
        var e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text) e.textContent = text;
        return e;
    }

    function httpsLink(url, text) {
        if (typeof url !== 'string' || url.indexOf('https://') !== 0) return null;
        var a = el('a', null, text);
        a.href = url;
        a.target = '_blank';
        a.rel = 'noopener noreferrer';
        return a;
    }

    function hiddenVersion() {
        try { return localStorage.getItem(HIDDEN_KEY); } catch (e) { return null; }
    }

    function formatDate(iso) {
        var d = new Date(iso);
        if (!iso || isNaN(d)) return '';
        return d.toLocaleDateString(document.documentElement.lang || undefined);
    }

    // version, date and the release page link
    function releaseHead(r) {
        var head = el('div', 'update-release-head');
        head.appendChild(el('strong', null, 'vCLU ' + r.version));
        var date = formatDate(r.published_at);
        if (date) head.appendChild(el('span', 'text-muted small', date));
        var link = httpsLink(r.url, t.releasePage || 'Release page');
        if (link) {
            link.className = 'small ms-auto';
            head.appendChild(link);
        }
        return head;
    }

    function command(label, cmd) {
        var box = el('div', 'mb-2');
        box.appendChild(el('div', 'small text-muted', label));
        if (cmd) {
            var pre = el('pre', 'update-cmd mb-0');
            pre.appendChild(el('code', null, cmd));
            box.appendChild(pre);
        }
        return box;
    }

    function buildModal(data) {
        var root = el('div', 'modal fade');
        root.tabIndex = -1;
        root.setAttribute('aria-hidden', 'true');
        var content = el('div', 'modal-content');
        root.appendChild(el('div', 'modal-dialog modal-dialog-scrollable modal-lg')).appendChild(content);

        var header = el('div', 'modal-header');
        header.appendChild(el('h5', 'modal-title', t.title || 'New vCLU version'));
        var x = el('button', 'btn-close');
        x.type = 'button';
        x.setAttribute('data-bs-dismiss', 'modal');
        x.setAttribute('aria-label', t.close || 'Close');
        header.appendChild(x);
        content.appendChild(header);

        var body = el('div', 'modal-body');
        if (data.current) body.appendChild(el('p', 'small text-muted', (t.running || 'Running:') + ' ' + data.current));
        var releases = data.releases || [];
        releases.slice(0, FULL).forEach(function(r) {
            var sec = el('section', 'update-release');
            sec.appendChild(releaseHead(r));
            // *_html fields are rendered and escaped by the server
            if (r.highlights_html) {
                var hl = el('p', 'update-highlights');
                hl.innerHTML = r.highlights_html;
                sec.appendChild(hl);
            }
            if (r.before_update_html && r.before_update_html.length) {
                var warn = el('div', 'update-before');
                warn.appendChild(el('div', 'update-before-title', t.beforeUpdate || 'Before you update'));
                var ul = el('ul');
                r.before_update_html.forEach(function(p) {
                    ul.appendChild(el('li')).innerHTML = p;
                });
                warn.appendChild(ul);
                sec.appendChild(warn);
            }
            if (r.notes_html) {
                var more = el('details', 'update-more');
                more.appendChild(el('summary', null, t.more || 'More'));
                more.appendChild(el('div', 'update-notes')).innerHTML = r.notes_html;
                sec.appendChild(more);
            }
            body.appendChild(sec);
        });
        if (releases.length > FULL) {
            var older = el('section', 'update-release');
            older.appendChild(el('div', 'small text-muted mb-1', t.older || 'Older releases'));
            releases.slice(FULL).forEach(function(r) { older.appendChild(releaseHead(r)); });
            body.appendChild(older);
        }

        body.appendChild(el('h6', 'mt-3', t.howTo || 'How to update'));
        body.appendChild(command(t.cmdService || 'Installed with install.sh (systemd):', CMD_SERVICE));
        body.appendChild(command(t.cmdDocker || 'Docker: pull the new image and create the container again.', CMD_DOCKER));
        var docs = httpsLink(DOCS_URL, t.docs || 'Update guide');
        body.appendChild(el('p', 'small mb-0')).appendChild(docs);
        content.appendChild(body);

        var footer = el('div', 'modal-footer');
        var hide = el('button', 'btn btn-outline-secondary', t.hide || 'Hide until next version');
        hide.type = 'button';
        hide.addEventListener('click', function() {
            try { localStorage.setItem(HIDDEN_KEY, data.latest.version); } catch (e) { /* per browser only */ }
            removeBadge();
            bootstrap.Modal.getOrCreateInstance(root).hide();
        });
        var close = el('button', 'btn btn-primary', t.close || 'Close');
        close.type = 'button';
        close.setAttribute('data-bs-dismiss', 'modal');
        footer.appendChild(hide);
        footer.appendChild(close);
        content.appendChild(footer);

        document.body.appendChild(root);
        return root;
    }

    function removeBadge() {
        if (badge) badge.remove();
        badge = null;
    }

    function show(data) {
        var bar = document.querySelector('.topbar-actions');
        if (!bar || !data.enabled || !data.update_available || !data.latest || !data.latest.version) return;
        if (hiddenVersion() === data.latest.version) return;
        var label = (t.available || 'vCLU {version} is available').replace('{version}', data.latest.version);
        badge = el('button', 'update-badge');
        badge.type = 'button';
        badge.title = label;
        badge.setAttribute('aria-label', label);
        badge.appendChild(el('i', 'bi bi-arrow-up-circle'));
        badge.appendChild(el('span', 'update-badge-label', 'vCLU ' + data.latest.version));
        badge.addEventListener('click', function() {
            if (!modal) modal = buildModal(data);
            bootstrap.Modal.getOrCreateInstance(modal).show();
        });
        bar.insertBefore(badge, bar.firstChild);
    }

    // Settings: turning the check off hides the badge at once
    document.addEventListener('vclu:update-check', function(e) {
        if (!e.detail || !e.detail.enabled) removeBadge();
    });

    fetch('/api/update', { credentials: 'same-origin' })
        .then(function(r) { return r.ok ? r.json() : null; })
        .then(function(data) { if (data) show(data); })
        .catch(function() { /* no badge */ });
})();
