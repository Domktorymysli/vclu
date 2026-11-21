/**
 * Notification bell in .topbar-actions (#248): unread count coloured by the worst unread
 * level, a list from /api/notifications, mark read, clear, mute. Polled every 60 s while the
 * page is visible; an open list is not rebuilt by the poll. An entry's text is t[msg] with its
 * params; a link starting with "#" is one of ACTIONS.
 */
(function() {
    'use strict';

    // every "notify.*" text of the page's language, by its full key (_sidebar.html)
    var t = {};
    try {
        t = JSON.parse(document.getElementById('notifyI18n').textContent) || {};
    } catch (err) { /* English fallbacks */ }
    var lang = document.documentElement.lang || undefined;
    var POLL = 60000;
    var LEVELS = {
        error: { icon: 'bi-x-octagon-fill', text: 'text-danger', badge: 'bg-danger' },
        warning: { icon: 'bi-exclamation-triangle-fill', text: 'text-warning', badge: 'bg-warning text-dark' },
        info: { icon: 'bi-info-circle-fill', text: 'text-primary', badge: 'bg-primary' }
    };

    var root, button, badge, list, live;
    var lastUnread = null;
    var timer = null;
    var stopped = false;
    var sent = 0; // requests sent; an answer older than the one shown is dropped
    var shown = 0;
    var pending = null; // an answer that came while the list was open

    function el(tag, cls, text) {
        var e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text) e.textContent = text;
        return e;
    }

    function fill(text, values) {
        return String(text || '').replace(/\{(\w+)\}/g, function(m, k) {
            return values && k in values ? values[k] : m;
        });
    }

    function has(key) {
        return Object.prototype.hasOwnProperty.call(t, key) && !!t[key];
    }

    function tr(key, fallback) {
        return has('notify.' + key) ? t['notify.' + key] : fallback;
    }

    function translated(e) {
        return !!e.msg && has(e.msg);
    }

    function msgText(e) {
        if (!translated(e)) return e.title || e.msg || '';
        var p = {};
        for (var k in e.params || {}) p[k] = e.params[k];
        // "login" -> the source's name in the settings
        if (p.source) p.source = tr('source.' + p.source, p.source);
        // lock end as a local time
        var until = new Date(p.until);
        if (p.until && !isNaN(until)) p.until = until.toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit' });
        return fill(t[e.msg], p);
    }

    function ago(iso) {
        var s = (new Date(iso) - Date.now()) / 1000;
        if (isNaN(s)) return '';
        var units = [['day', 86400], ['hour', 3600], ['minute', 60]];
        try {
            var f = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' });
            for (var i = 0; i < units.length; i++) {
                if (Math.abs(s) >= units[i][1]) return f.format(Math.round(s / units[i][1]), units[i][0]);
            }
            return f.format(0, 'second');
        } catch (err) {
            return new Date(iso).toLocaleString(lang);
        }
    }

    // Only links into the panel are followed ("//host" and "/\\host" lead out of it)
    function localLink(link) {
        if (typeof link !== 'string' || link.charAt(0) !== '/') return '';
        var c = link.charAt(1);
        return c === '/' || c === '\\' ? '' : link;
    }

    function isOpen() {
        return button.getAttribute('aria-expanded') === 'true';
    }

    function hideMenu() {
        if (window.bootstrap && isOpen()) bootstrap.Dropdown.getOrCreateInstance(button).hide();
    }

    function stop() {
        stopped = true;
        clearInterval(timer);
        timer = null;
    }

    // GET or a POST: both answer the list; only the newest answer is shown. A POST is the
    // user's own change, shown at once even in an open list.
    function request(url, body, own) {
        var n = ++sent;
        var opts = { credentials: 'same-origin' };
        if (url !== '/api/notifications') {
            opts.method = 'POST';
            opts.headers = { 'Content-Type': 'application/json' };
            if (body) opts.body = JSON.stringify(body);
        }
        return fetch(url, opts)
            .then(function(r) {
                // logged out: stop asking until the page is shown again
                if (r.status === 401) stop();
                return r.ok ? r.json() : null;
            })
            .then(function(data) {
                if (!data || !data.entries || n < shown) return;
                shown = n;
                render(data, own);
            })
            .catch(function() { /* keep the last list */ });
    }

    // Links starting with "#" are actions of the panel; marked is the read mark sent
    var ACTIONS = {
        '#update': function(marked) {
            var modal = window.VCLU && VCLU.openUpdateModal ? Promise.resolve(VCLU.openUpdateModal()) : Promise.resolve(false);
            modal.then(function(opened) {
                if (opened) hideMenu();
                // no release list (not checked yet, or the check is off): the setting
                else marked.then(function() { location.href = '/settings'; });
            });
        }
    };

    function open(e) {
        var link = localLink(e.link);
        var marked = e.read ? Promise.resolve() : request('/api/notifications/read', { ids: [e.id] }, true);
        if (Object.prototype.hasOwnProperty.call(ACTIONS, e.link)) ACTIONS[e.link](marked);
        else if (link) marked.then(function() { location.href = link; });
    }

    function muteRow(e) {
        var row = el('div', 'notify-mute d-none');
        [['24h', tr('mute_24h', '24 h')], ['7d', tr('mute_7d', '7 days')], ['forever', tr('mute_forever', 'Forever')]].forEach(function(m) {
            var b = el('button', 'btn btn-sm btn-outline-secondary', m[1]);
            b.type = 'button';
            b.dataset.mute = m[0];
            b.addEventListener('click', function(ev) {
                ev.stopPropagation();
                request('/api/notifications/mute', { key: e.key, for: m[0] }, true).then(function() {
                    button.focus(); // the entry is gone
                    document.dispatchEvent(new CustomEvent('vclu:notifications-changed', { detail: { from: 'bell' } }));
                });
            });
            row.appendChild(b);
        });
        return row;
    }

    function item(e) {
        var lv = LEVELS[e.level] || LEVELS.info;
        var li = el('li', 'notify-entry' + (e.read ? '' : ' unread'));
        var a = el('a', 'dropdown-item notify-item');
        a.href = localLink(e.link) || '#';
        a.appendChild(el('i', 'bi ' + lv.icon + ' ' + lv.text)).setAttribute('aria-hidden', 'true');
        var main = el('span', 'notify-main');
        main.appendChild(el('span', 'notify-text', msgText(e)));
        // raw text (an error) when there is one; the English body only without a translation
        var sub = e.detail || (translated(e) ? '' : e.body);
        if (sub) main.appendChild(el('span', 'notify-body', sub));
        var meta = el('span', 'notify-meta', ago(e.last || e.first));
        if (e.count > 1) meta.appendChild(el('span', 'notify-count', ' ×' + e.count));
        main.appendChild(meta);
        a.appendChild(main);
        a.addEventListener('click', function(ev) {
            ev.preventDefault();
            open(e);
        });
        li.appendChild(a);

        if (e.key) {
            var rows = muteRow(e);
            var m = el('button', 'btn-icon notify-mute-btn');
            m.type = 'button';
            m.title = tr('mute', 'Mute');
            m.setAttribute('aria-label', m.title);
            m.setAttribute('aria-expanded', 'false');
            m.appendChild(el('i', 'bi bi-bell-slash'));
            m.addEventListener('click', function(ev) {
                ev.stopPropagation();
                var shown = rows.classList.toggle('d-none') === false;
                m.setAttribute('aria-expanded', String(shown));
            });
            li.appendChild(m);
            li.appendChild(rows);
        }
        return li;
    }

    function render(data, own) {
        var unread = data.unread || 0;
        var label = fill(tr('label', 'Notifications, {count} unread'), { count: unread });
        button.setAttribute('aria-label', label);
        button.title = label;
        badge.textContent = unread > 99 ? '99+' : String(unread);
        badge.className = 'notify-badge badge rounded-pill ' + (LEVELS[data.worst] || LEVELS.info).badge + (unread ? '' : ' d-none');
        if (lastUnread !== null && unread > lastUnread) live.textContent = label;
        lastUnread = unread;

        // the poll does not move what the user is looking at (or the keyboard focus)
        if (isOpen() && !own) {
            pending = data;
            return;
        }
        pending = null;
        renderList(data.entries || []);
    }

    function renderList(entries) {
        list.textContent = '';
        if (!entries.length) list.appendChild(el('li', 'notify-empty', tr('empty', 'No notifications')));
        entries.forEach(function(e) { list.appendChild(item(e)); });
    }

    function refresh() {
        if (stopped) return;
        request('/api/notifications');
    }

    function start() {
        if (!timer && !stopped) timer = setInterval(refresh, POLL);
    }

    function headButton(text, onClick) {
        var b = el('button', 'btn btn-link btn-sm', text);
        b.type = 'button';
        b.addEventListener('click', function(ev) {
            ev.stopPropagation();
            onClick();
        });
        return b;
    }

    function build(bar) {
        root = el('div', 'dropdown notify-bell');
        button = el('button', 'btn-icon notify-bell-btn');
        button.type = 'button';
        button.setAttribute('data-bs-toggle', 'dropdown');
        button.setAttribute('data-bs-auto-close', 'outside');
        button.setAttribute('aria-expanded', 'false');
        button.appendChild(el('i', 'bi bi-bell')).setAttribute('aria-hidden', 'true');
        badge = el('span', 'notify-badge badge rounded-pill d-none');
        button.appendChild(badge);
        root.appendChild(button);
        live = el('span', 'visually-hidden');
        live.setAttribute('aria-live', 'polite');
        root.appendChild(live);

        var menu = el('div', 'dropdown-menu dropdown-menu-end notify-menu');
        var head = el('div', 'notify-head');
        head.appendChild(el('strong', 'me-auto', tr('title', 'Notifications')));
        var markAll = headButton(tr('mark_all_short', tr('mark_all', 'Mark all as read')), function() {
            request('/api/notifications/read', { all: true }, true);
        });
        markAll.title = tr('mark_all', 'Mark all as read');
        markAll.setAttribute('aria-label', markAll.title);
        head.appendChild(markAll);
        head.appendChild(headButton(tr('clear', 'Clear'), function() {
            request('/api/notifications/clear', null, true);
        }));
        var close = el('button', 'btn-close notify-close');
        close.type = 'button';
        close.setAttribute('aria-label', tr('close', 'Close'));
        close.addEventListener('click', function() {
            if (window.bootstrap) bootstrap.Dropdown.getOrCreateInstance(button).hide();
            button.focus();
        });
        head.appendChild(close);
        menu.appendChild(head);
        list = el('ul', 'notify-list list-unstyled mb-0');
        menu.appendChild(list);
        root.appendChild(menu);
        // what the poll brought while the list was open
        root.addEventListener('hidden.bs.dropdown', function() {
            if (pending) {
                renderList(pending.entries || []);
                pending = null;
            }
        });
        bar.insertBefore(root, bar.firstChild);
    }

    var bar = document.querySelector('.topbar-actions');
    if (!bar) return;
    build(bar);
    refresh();
    if (document.visibilityState !== 'hidden') start();
    document.addEventListener('visibilitychange', function() {
        if (document.visibilityState === 'visible') {
            stopped = false; // after a 401, one more try: the user may have logged in again
            refresh();
            start();
        } else {
            clearInterval(timer);
            timer = null;
        }
    });
    // update check switched in settings, or the update hidden in its modal
    document.addEventListener('vclu:update-check', refresh);
    document.addEventListener('vclu:notifications-changed', function(ev) {
        if (!(ev.detail && ev.detail.from === 'bell')) refresh();
    });
})();
