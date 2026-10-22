/**
 * Dashboard renderer - fetches definition, renders widgets, polls values, handles actions.
 * Vanilla JS, no dependencies beyond Bootstrap CSS.
 */
(function() {
    'use strict';

    var appEl = document.getElementById('dashboardApp');
    var contentEl = document.getElementById('dashboardContent');
    var overlayEl = document.getElementById('dashboardOverlay');
    var dashId = appEl.dataset.id;
    var pollInterval = 2000;
    var pollTimer = null;
    var definition = null;
    var widgetEls = {}; // widgetId -> DOM element
    var sectionCoverEls = {}; // section index -> its header's cover counts
    var actionGrace = {}; // widgetId -> timestamp (ignore polls until this time)
    var chartInstances = {}; // widgetId -> Chart.js instance
    var chartTimers = {};   // widgetId -> per-chart refresh timer

    // Texts of the page, Polish or English; #85 moves them to web/locales.
    var TEXTS = {
        pl: { on: 'włączone', off: 'wyłączone', open: 'otwarta', closed: 'zamknięta', opening: 'otwiera się',
              closing: 'zamyka się', unknown: 'brak odczytu', run: 'uruchom', sent: 'wysłano', updated: 'odświeżono',
              nothingToday: 'nic więcej dziś', holidays: 'Święta', namedays: 'Imieniny', sun: 'Słońce', show: 'Pokaż',
              polarDay: 'dzień polarny', polarNight: 'noc polarna', noConnection: 'Brak połączenia',
              openN: ['otwarta', 'otwarte', 'otwartych'], closedN: ['zamknięta', 'zamknięte', 'zamkniętych'],
              moving: 'w ruchu', allOpen: 'wszystkie otwarte', allClosed: 'wszystkie zamknięte', noCovers: 'brak rolet',
              up: 'W górę', stop: 'Stop', down: 'W dół', position: 'Pozycja', tilt: 'Lamele',
              fullscreen: 'Pełny ekran', exitFullscreen: 'Zamknij pełny ekran',
              playing: 'odtwarza', paused: 'pauza', playerOff: 'wyłączony', offline: 'offline',
              play: 'Odtwarzaj', pause: 'Pauza', previous: 'Poprzedni', next: 'Następny',
              mute: 'Wycisz', unmute: 'Włącz dźwięk', power: 'Włącz / wyłącz', volume: 'Głośność' },
        en: { on: 'on', off: 'off', open: 'open', closed: 'closed', opening: 'opening',
              closing: 'closing', unknown: 'no reading', run: 'run', sent: 'sent', updated: 'updated',
              nothingToday: 'nothing more today', holidays: 'Holidays', namedays: 'Name days', sun: 'Sun', show: 'Show',
              polarDay: 'polar day', polarNight: 'polar night', noConnection: 'No connection',
              openN: ['open', 'open', 'open'], closedN: ['closed', 'closed', 'closed'],
              moving: 'moving', allOpen: 'all open', allClosed: 'all closed', noCovers: 'no covers',
              up: 'Up', stop: 'Stop', down: 'Down', position: 'Position', tilt: 'Slats',
              fullscreen: 'Full screen', exitFullscreen: 'Exit full screen',
              playing: 'playing', paused: 'paused', playerOff: 'off', offline: 'offline',
              play: 'Play', pause: 'Pause', previous: 'Previous', next: 'Next',
              mute: 'Mute', unmute: 'Unmute', power: 'Power', volume: 'Volume' }
    };
    // ?lang= as on the other pages, else the browser's language
    var lang = (function() {
        var m = /[?&]lang=([a-z]{2})/i.exec(location.search);
        return /^pl/i.test((m && m[1]) || document.documentElement.lang || navigator.language || '') ? 'pl' : 'en';
    })();
    var T = TEXTS[lang];

    // A tile without its own icon gets one by type
    var typeIcons = {
        button: 'play-fill', toggle: 'power', slider: 'sliders', status: 'info-circle', link: 'link-45deg',
        chart: 'graph-up', select: 'list-ul', image: 'image', cover: 'arrows-expand', covers: 'window', dimmer: 'lightbulb',
        color: 'palette', calendar: 'calendar3', player: 'music-note-beamed'
    };

    // 23.9 -> "23,9" in Polish; text as it is
    function fmtNum(v) {
        return typeof v === 'number' && lang === 'pl' ? String(v).replace('.', ',') : String(v);
    }
    function withUnit(v, unit) {
        return fmtNum(v) + (unit ? ' ' + unit : '');
    }

    // ---- Time-of-day period tracking ----
    var currentPeriod = null;
    var periodCheckTimer = null;

    function calcPeriod() {
        var h = new Date().getHours();
        if (h >= 6 && h < 12) return 'morning';
        if (h >= 12 && h < 17) return 'afternoon';
        if (h >= 17 && h < 22) return 'evening';
        return 'night';
    }

    function applyPeriod(period) {
        if (period === currentPeriod) return;
        currentPeriod = period;
        document.body.setAttribute('data-period', period);
    }

    function startPeriodCheck() {
        if (periodCheckTimer) return;
        periodCheckTimer = setInterval(function() {
            var p = calcPeriod();
            if (p !== currentPeriod) {
                // Period changed — reload definition from server (widgets may differ)
                applyPeriod(p);
                fetchJSON('/api/dashboard/' + dashId).then(function(def) {
                    if (!def._notFound && !def.error) {
                        renderDashboard(def);
                    }
                });
            }
        }, 60000); // check every minute
    }

    var defaultRefreshByRange = {
        '30m': 30000, '2h': 60000, '24h': 300000, '7d': 900000
    };
    function chartRefreshMs(w) {
        if (w.refresh) return w.refresh * 1000;
        return defaultRefreshByRange[w.range || '2h'] || 60000;
    }

    // Color mapping for named colors
    var colorMap = {
        accent:  'var(--dash-accent)',
        success: '#198754',
        warning: '#ffc107',
        danger:  '#dc3545',
        info:    '#0dcaf0',
        primary: '#0d6efd',
    };

    function resolveColor(c) {
        if (!c) return null;
        return colorMap[c] || c;
    }

    // Apply a polled colour; a falsy one drops it
    function applyColor(el, c) {
        var color = resolveColor(c);
        if (color) {
            el.style.setProperty('--widget-color', color);
        } else {
            el.style.removeProperty('--widget-color');
        }
    }

    // danger and warning make a status an alarm
    function isAlarm(c) {
        return c === 'danger' || c === 'warning';
    }

    // 1 otwarta, 2-4 otwarte, 5 otwartych (12-14 too); English has one form
    function plural(n, forms) {
        var f = n === 1 ? 0 : (n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14)) ? 1 : 2;
        return n + ' ' + forms[f];
    }

    // "3 otwarte · 1 w ruchu", "wszystkie zamknięte": counts of a cover group or a section
    // (null without covers; "brak odczytu" when none has a reading)
    function coverCountsText(c) {
        var open = c.open || 0, moving = c.moving || 0, closed = c.closed || 0, unknown = c.unknown || 0;
        var n = open + moving + closed + unknown;
        if (n === 0) return null;
        if (unknown === n) return T.unknown;
        if (n === open) return n > 1 ? T.allOpen : T.open;
        if (n === closed) return n > 1 ? T.allClosed : T.closed;
        var parts = [];
        if (open) parts.push(plural(open, T.openN));
        if (moving) parts.push(moving + ' ' + T.moving);
        if (closed) parts.push(plural(closed, T.closedN));
        if (unknown) parts.push(unknown + ' ' + T.unknown);
        return parts.join(' · ');
    }

    // The circle and the second line: state is on, off, moving, alarm, unknown or value
    function tileState(type, w, d) {
        var on;
        switch (type) {
        case 'toggle':
            if (d.value == null) return ['unknown', T.unknown];
            return d.value ? ['on', T.on] : ['off', T.off];
        case 'slider':
            return d.value == null ? ['unknown', T.unknown] : ['value', fmtNum(d.value)];
        case 'status':
            if (d.value == null) return ['unknown', T.unknown];
            return [isAlarm(d.color) ? 'alarm' : 'value', withUnit(d.value, w.unit)];
        case 'select':
            if (d.value == null) return ['unknown', T.unknown];
            var label = String(d.value);
            (w.options || []).forEach(function(o) { if (String(o.value) === String(d.value)) label = o.label; });
            return ['value', label];
        case 'dimmer':
        case 'color':
            var level = type === 'dimmer' ? d.value : d.brightness;
            if (level == null && d.on == null) return ['unknown', T.unknown];
            on = d.on != null ? d.on : level > 0;
            if (!on) return ['off', T.off];
            return ['on', level > 0 ? withUnit(level, '%') : T.on];
        case 'cover':
            if (d.state === 1) return ['moving', T.opening];
            if (d.state === 2) return ['moving', T.closing];
            if (d.position == null) return ['unknown', T.unknown];
            if (d.position <= 0) return ['off', T.closed];
            return ['on', d.position >= 100 ? T.open : T.open + ' ' + withUnit(d.position, '%')];
        case 'covers':
            var text = coverCountsText(d);
            if (text == null) return ['unknown', T.noCovers];
            if (d.moving > 0) return ['moving', text];
            if (d.open > 0) return ['on', text];
            return [d.closed > 0 ? 'off' : 'unknown', text];
        case 'player':
            if (d.error) return ['unknown', T.unknown];
            if (d.online === false) return ['unknown', T.offline];
            if (d.power === false) return ['off', T.playerOff];
            return d.playing ? ['on', T.playing] : ['value', T.paused];
        case 'button':
            return ['value', T.run];
        case 'link':
            var m = /^[a-z]+:\/\/([^\/?#:]+)/i.exec(w.url || '');
            return ['value', m ? m[1] : String(w.url || '')];
        }
        return ['value', ''];
    }

    function setState(el, state, text) {
        el.dataset.state = state;
        var s = el.querySelector('.dash-tile-state');
        if (s && text != null) s.textContent = text;
    }

    // Tile state from what is known of the widget (definition, then polls and replies)
    function refreshTile(el, w, data) {
        var d = el._data || (el._data = {});
        for (var k in data) if (data.hasOwnProperty(k)) d[k] = data[k];
        var type = el.dataset.type;
        if (type === 'chart' || type === 'image' || type === 'calendar') return;
        var st = tileState(type, w, d);
        setState(el, st[0], st[1]);
        if (type === 'player') updatePlayer(el, d);
        if (el.classList.contains('dash-header-item')) el.title = (w.label || '') + ': ' + st[1];
        if (el.getAttribute('role') === 'switch') el.setAttribute('aria-checked', d.value ? 'true' : 'false');
    }

    // Icon in a circle, the name, the state line
    function tileHead(w) {
        var head = calEl('div', 'dash-tile-head');
        var icon = calEl('div', 'dash-widget-icon');
        icon.innerHTML = '<i class="bi bi-' + esc(w.icon || typeIcons[w.type] || 'circle') + '"></i>';
        var text = calEl('div', 'dash-tile-text');
        text.appendChild(calEl('div', 'dash-widget-label', w.label || ''));
        text.appendChild(calEl('div', 'dash-tile-state', ' '));
        head.appendChild(icon);
        head.appendChild(text);
        return head;
    }

    // ---- Fetch helpers ----

    function fetchJSON(url, opts) {
        return fetch(url, opts).then(function(r) {
            if (r.status === 404) return { _notFound: true };
            if (!r.ok) throw new Error('HTTP ' + r.status);
            return r.json();
        });
    }

    // ---- Render ----

    function renderGroupTabs(def) {
        // Remove existing elements
        var old = document.querySelectorAll('.dash-group-tabs, .dash-nav-burger, .dash-nav-menu');
        old.forEach(function(el) { el.remove(); });

        if (!def.groupTabs || !def.groupTabs.length) return;

        var header = document.querySelector('.dash-header');
        if (!header) return;

        // Collect all tabs across groups
        var allTabs = [];
        for (var g = 0; g < def.groupTabs.length; g++) {
            var group = def.groupTabs[g];
            for (var t = 0; t < group.tabs.length; t++) {
                allTabs.push(group.tabs[t]);
            }
        }

        // Desktop/tablet: horizontal tab bar (unchanged)
        for (var g = def.groupTabs.length - 1; g >= 0; g--) {
            var group = def.groupTabs[g];
            var bar = document.createElement('div');
            bar.className = 'dash-group-tabs';

            for (var t = 0; t < group.tabs.length; t++) {
                var tab = group.tabs[t];
                var a = document.createElement('a');
                a.className = 'dash-tab' + (tab.active ? ' active' : '');
                a.href = '/d/' + tab.id;
                a.innerHTML = '<i class="bi bi-' + esc(tab.icon) + '"></i> ' + esc(tab.name);
                bar.appendChild(a);
            }

            header.after(bar);
        }

        // Mobile: burger button in header + dropdown menu
        var burger = document.createElement('button');
        burger.className = 'dash-nav-burger';
        burger.innerHTML = '<i class="bi bi-list"></i>';
        header.appendChild(burger);
        header.classList.add('dash-header-btns');

        var menu = document.createElement('div');
        menu.className = 'dash-nav-menu';
        for (var i = 0; i < allTabs.length; i++) {
            var tab = allTabs[i];
            var a = document.createElement('a');
            a.className = 'dash-nav-item' + (tab.active ? ' active' : '');
            a.href = '/d/' + tab.id;
            a.innerHTML = '<i class="bi bi-' + esc(tab.icon) + '"></i> ' + esc(tab.name);
            menu.appendChild(a);
        }
        header.after(menu);

        burger.addEventListener('click', function(e) {
            e.stopPropagation();
            menu.classList.toggle('open');
        });
        document.addEventListener('click', function() {
            menu.classList.remove('open');
        });
    }

    // A full-screen button where the Fullscreen API works and the page is not already an app
    // from the home screen; iPhone and iPad get the home screen instead (Add to Home Screen).
    function setupFullscreen() {
        var header = document.querySelector('.dash-header');
        var standalone = navigator.standalone === true ||
            (window.matchMedia && (matchMedia('(display-mode: standalone)').matches || matchMedia('(display-mode: fullscreen)').matches));
        var ios = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
            (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
        if (!header || !document.fullscreenEnabled || standalone || ios) return;
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'dash-fullscreen-btn';
        btn.appendChild(calEl('i', 'bi'));
        function sync() {
            var on = !!document.fullscreenElement;
            btn.firstChild.className = 'bi bi-' + (on ? 'fullscreen-exit' : 'arrows-fullscreen');
            btn.title = on ? T.exitFullscreen : T.fullscreen;
            btn.setAttribute('aria-label', btn.title);
        }
        btn.addEventListener('click', function() {
            var p = document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen();
            if (p && p.catch) p.catch(function() {});
        });
        document.addEventListener('fullscreenchange', sync);
        sync();
        header.appendChild(btn);
        header.classList.add('dash-header-btns');
    }

    function renderDashboard(def) {
        definition = def;
        document.body.classList.toggle('dash-full', def.width === 'full');
        pollInterval = (def.poll || 2) * 1000;
        stopAllChartTimers();
        destroyAllCharts();
        contentEl.className = '';
        contentEl.innerHTML = '';
        contentEl.style.cssText = '--dash-columns:' + (def.columns || 3);
        widgetEls = {};
        sectionCoverEls = {};

        // Apply time-of-day period (from server or local fallback)
        applyPeriod(def.period || calcPeriod());
        startPeriodCheck();

        // Render group navigation tabs
        renderGroupTabs(def);

        // Apply theme CSS variables from definition (allows Lua to drive themes)
        if (def.theme) {
            var themeMap = {accent:'--dash-accent', background:'--dash-bg', cardBg:'--dash-card-bg', text:'--dash-text'};
            for (var key in themeMap) {
                if (def.theme[key]) document.documentElement.style.setProperty(themeMap[key], def.theme[key]);
            }
            if (def.theme.wallpaper) {
                document.documentElement.style.setProperty('--dash-wallpaper', 'url(' + def.theme.wallpaper + ')');
            } else {
                document.documentElement.style.removeProperty('--dash-wallpaper');
            }
        }

        // Group widgets by section index
        var sectionWidgets = {};
        for (var i = 0; i < def.widgets.length; i++) {
            var w = def.widgets[i];
            var si = w.section || 0;
            if (!sectionWidgets[si]) sectionWidgets[si] = [];
            sectionWidgets[si].push(w);
        }

        for (var si = 0; si < def.sections.length; si++) {
            var sec = def.sections[si];
            var widgets = sectionWidgets[si] || [];

            if (sec.isHeader) {
                // Header bar — compact flex row
                var bar = document.createElement('div');
                bar.className = 'dash-header-bar';
                for (var wi = 0; wi < widgets.length; wi++) {
                    bar.appendChild(renderHeaderWidget(widgets[wi]));
                }
                contentEl.appendChild(bar);
                syncHeaderBars();
                continue;
            }

            var secEl = document.createElement('div');
            secEl.className = 'dash-section';

            // Section header
            var hdr = document.createElement('div');
            hdr.className = 'dash-section-header';
            if (sec.icon) hdr.innerHTML = '<i class="bi bi-' + esc(sec.icon) + '"></i>';
            hdr.innerHTML += esc(sec.name);
            if (sec.covers) {
                sectionCoverEls[si] = hdr.appendChild(calEl('span', 'dash-section-covers'));
                updateSectionCovers(si, sec.covers);
            }
            secEl.appendChild(hdr);

            // Grid
            var grid = document.createElement('div');
            grid.className = 'dash-grid';
            var cols = sec.columns || def.columns || 3;
            // --dash-columns-mid: a full-width page on a portrait tablet keeps up to 3 columns
            grid.style.cssText = '--dash-columns:' + cols + ';--dash-columns-mid:' + Math.min(cols, 3);

            for (var wi = 0; wi < widgets.length; wi++) {
                var wEl = renderWidget(widgets[wi], cols);
                grid.appendChild(wEl);
            }

            secEl.appendChild(grid);
            contentEl.appendChild(secEl);
        }
    }

    function renderWidget(w, cols) {
        if (w.type === 'group') return renderGroup(w);
        var el = document.createElement('div');
        el.className = 'dash-widget dash-widget-' + w.type;
        el.dataset.widgetId = w.id;
        el.dataset.type = w.type;

        // Span (stored as data-attr; CSS responsive rules can override); a player is two wide
        var span = w.span || (w.type === 'player' && cols > 1 ? 2 : 0);
        if (span > 1) {
            el.style.gridColumn = 'span ' + span;
            el.dataset.span = span;
        }

        // Size
        if (w.size === 'large') el.classList.add('size-large');

        // Color
        var color = resolveColor(w.color);
        if (color) {
            el.style.setProperty('--widget-color', color);
            el.dataset.ownColor = '1';
        }

        // Disabled
        if (w.disabled) el.classList.add('disabled');

        // Icon, name and state; an image has its caption under the picture, a player its own layout
        var head = tileHead(w);
        if (w.type !== 'image' && w.type !== 'player') el.appendChild(head);

        // Error indicator: a badge on the icon
        if (w.error) {
            el.classList.add('has-error');
            var errIcon = document.createElement('span');
            errIcon.className = 'dash-widget-error';
            errIcon.title = w.error;
            errIcon.innerHTML = '<i class="bi bi-exclamation-triangle-fill"></i>';
            head.querySelector('.dash-widget-icon').appendChild(errIcon);
        }

        // Type-specific rendering
        if (w.type === 'button') {
            renderButton(el, w);
        } else if (w.type === 'toggle') {
            renderToggle(el, w);
        } else if (w.type === 'slider') {
            renderSlider(el, w);
        } else if (w.type === 'status') {
            renderStatus(el, w);
        } else if (w.type === 'link') {
            renderLink(el, w);
        } else if (w.type === 'chart') {
            renderChart(el, w);
        } else if (w.type === 'select') {
            renderSelect(el, w);
        } else if (w.type === 'cover') {
            renderCover(el, w);
        } else if (w.type === 'covers') {
            renderCovers(el, w);
        } else if (w.type === 'dimmer') {
            renderDimmer(el, w);
        } else if (w.type === 'color') {
            renderColor(el, w);
        } else if (w.type === 'image') {
            renderImage(el, w, head);
        } else if (w.type === 'calendar') {
            renderCalendar(el, w, head);
        } else if (w.type === 'player') {
            renderPlayer(el, w, head);
        }

        refreshTile(el, w, w);
        widgetEls[w.id] = el;
        return el;
    }

    // Brief text on the state line, then the state again
    function flashState(el, text) {
        var s = el.querySelector('.dash-tile-state');
        if (!s) return;
        var prev = s.textContent;
        s.textContent = text;
        setTimeout(function() { if (s.textContent === text) s.textContent = prev; }, 1500);
    }

    function renderButton(el, w) {
        el.addEventListener('click', function() {
            if (w.confirm && !confirm(w.confirm)) return;
            el.classList.add('flash');
            setTimeout(function() { el.classList.remove('flash'); }, 300);
            flashState(el, T.sent);
            executeAction(w.id, 'tap');
        });
    }

    // An on/off switch at the end of the head; a tap on the head (or the whole tile) flips it
    function headSwitch(el, cls, checked, onChange, target) {
        var sw = document.createElement('div');
        sw.className = cls + ' form-check form-switch';
        var input = document.createElement('input');
        input.type = 'checkbox';
        input.className = 'form-check-input';
        input.setAttribute('role', 'switch');
        input.setAttribute('aria-label', el.querySelector('.dash-widget-label').textContent);
        input.checked = checked;
        input.addEventListener('change', function(e) {
            e.stopPropagation();
            if (el.classList.contains('disabled')) { input.checked = !input.checked; return; }
            onChange(input.checked);
        });
        sw.appendChild(input);
        el.querySelector('.dash-tile-head').appendChild(sw);
        (target || el).addEventListener('click', function(e) {
            if (sw.contains(e.target) || el.classList.contains('disabled')) return;
            input.checked = !input.checked;
            onChange(input.checked);
        });
        return input;
    }

    function renderToggle(el, w) {
        if (w.value) el.classList.add('active');
        var input = headSwitch(el, 'dash-toggle-switch', !!w.value, function(on) {
            el.classList.toggle('active', on);
            refreshTile(el, w, { value: on });
            executeAction(w.id, 'toggle', on);
        });
        input.dataset.widgetId = w.id;
    }

    function renderSlider(el, w) {
        var row = document.createElement('div');
        row.className = 'dash-slider-row';

        var input = document.createElement('input');
        input.type = 'range';
        input.className = 'dash-slider-input';
        input.min = w.min != null ? w.min : 0;
        input.max = w.max != null ? w.max : 100;
        input.value = w.value != null ? w.value : 0;
        input.dataset.widgetId = w.id;

        bindSlider(input, function(v) { setState(el, 'value', fmtNum(v)); },
            function(v) { executeAction(w.id, 'set', v); }, 250);

        row.appendChild(input);
        el.appendChild(row);
    }

    function renderStatus(el, w) {
        if (w.spark) {
            var sparkWrap = document.createElement('div');
            sparkWrap.className = 'dash-sparkline';
            var sparkCanvas = document.createElement('canvas');
            sparkCanvas.id = 'spark-' + w.id;
            sparkWrap.appendChild(sparkCanvas);
            el.appendChild(sparkWrap);
            fetchSparkline(w, sparkCanvas);
            startChartTimer('spark-' + w.id, function() {
                var c = document.getElementById('spark-' + w.id);
                if (c) fetchSparkline(w, c);
            }, chartRefreshMs({range: w.sparkRange || '2h'}));
        }
    }

    function renderLink(el, w) {
        var ext = document.createElement('span');
        ext.className = 'dash-link-ext';
        ext.innerHTML = '<i class="bi bi-box-arrow-up-right"></i>';
        el.querySelector('.dash-tile-head').appendChild(ext);
        el.addEventListener('click', function() {
            window.open(w.url, '_blank');
        });
    }

    // The native list lies over the whole tile: a tap anywhere opens it
    function renderSelect(el, w) {
        var chev = calEl('span', 'dash-select-chevron');
        chev.innerHTML = '<i class="bi bi-chevron-expand"></i>';
        el.querySelector('.dash-tile-head').appendChild(chev);
        var wrap = document.createElement('div');
        wrap.className = 'dash-select-wrap';

        var sel = document.createElement('select');
        sel.className = 'dash-select-input';
        sel.dataset.widgetId = w.id;
        for (var i = 0; i < (w.options || []).length; i++) {
            var opt = w.options[i];
            var option = document.createElement('option');
            option.value = opt.value;
            option.textContent = opt.label;
            if (w.value != null && String(w.value) === String(opt.value)) {
                option.selected = true;
            }
            sel.appendChild(option);
        }
        sel.setAttribute('aria-label', w.label || '');
        sel.addEventListener('change', function() {
            refreshTile(el, w, { value: sel.value });
            executeAction(w.id, 'select', sel.value);
        });
        wrap.appendChild(sel);
        el.appendChild(wrap);
    }

    function renderImage(el, w, head) {
        el.classList.add('dash-widget-image');
        el.style.cursor = 'default';

        // Image first
        var img = document.createElement('img');
        img.className = 'dash-image-img';
        img.src = w.url || '';
        img.alt = w.label || '';
        if (w.height) img.style.height = w.height + 'px';
        // the state line: when the picture came, or that it did not
        img.onload = function() {
            img.style.opacity = '';
            setState(el, 'value', T.updated + ' ' + new Date().toLocaleTimeString(lang, {hour: '2-digit', minute: '2-digit'}));
        };
        img.onerror = function() { img.style.opacity = '0.2'; setState(el, 'unknown', T.unknown); };
        el.appendChild(img);

        // Caption below image
        head.classList.add('dash-image-label');
        if (w.align) head.classList.add('align-' + w.align);
        el.appendChild(head);
        setState(el, 'unknown', ' ');

        // Auto-refresh image
        if (w.refresh) {
            startChartTimer('img-' + w.id, function() {
                var sep = w.url.indexOf('?') >= 0 ? '&' : '?';
                img.src = w.url + sep + '_t=' + Date.now();
            }, w.refresh * 1000);
        }
        return el;
    }

    // ---- Calendar widget: a month grid and the day of a tapped date ----
    var calendars = {}; // widgetId -> {refresh}

    function pad2(n) { return (n < 10 ? '0' : '') + n; }
    function ymd(d) { return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()); }
    function parseYmd(s) { var p = s.split('-'); return new Date(+p[0], +p[1] - 1, +p[2]); }
    function addDays(d, n) { return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n); }
    // times in the house's zone (sent by the server), so a viewer abroad sees the house's clock
    function hhmm(ts, zone) {
        var o = {hour: '2-digit', minute: '2-digit'};
        if (zone) o.timeZone = zone;
        try { return new Date(ts * 1000).toLocaleTimeString([], o); }
        catch (e) { return new Date(ts * 1000).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'}); }
    }
    function zoneHour(ts, zone) {
        try { return +new Date(ts * 1000).toLocaleString('en-GB', {hour: '2-digit', hour12: false, timeZone: zone || undefined}) % 24; }
        catch (e) { return new Date(ts * 1000).getHours(); }
    }

    var calHues = [168, 215, 32, 280, 340, 95];
    function calHue(id) {
        var h = 0;
        for (var i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) % 9973;
        return calHues[h % calHues.length];
    }

    function calEl(tag, cls, text) {
        var e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text != null) e.textContent = text;
        return e;
    }

    function calIconBtn(icon, title, onClick) {
        var b = calEl('button', 'dash-cal-btn');
        b.type = 'button';
        b.title = title;
        b.setAttribute('aria-label', title);
        b.innerHTML = '<i class="bi bi-' + icon + '"></i>';
        b.addEventListener('click', function(ev) { ev.stopPropagation(); onClick(); });
        return b;
    }

    // One listener for all calendars: closes an open options menu on a click outside it
    // (a listener per render would keep every replaced widget alive).
    var calMenusListening = false;
    function closeCalMenus(ev) {
        document.querySelectorAll('.dash-cal-menu-wrap').forEach(function(wrap) {
            var menu = wrap.querySelector('.dash-cal-menu');
            if (menu && !menu.hidden && !wrap.contains(ev.target)) {
                menu.hidden = true;
                wrap.querySelector('.dash-cal-btn').setAttribute('aria-expanded', 'false');
            }
        });
    }

    function renderCalendar(el, w, head) {
        el.classList.add('dash-widget-calendar');
        el.style.cursor = 'default';
        if (!w.span) el.style.gridColumn = '1 / -1';

        // the browser's date until the first answer brings the house's (another zone, a wrong clock)
        var today = ymd(new Date());
        var st = { view: w.view === 'day' ? 'day' : 'month', day: today, month: today.slice(0, 7) + '-01', data: null, today: today, seq: 0 };
        function tm(ts) { return hhmm(ts, st.data && st.data.zone); }
        // What to show besides events: what Lua allows (the server sends nothing else),
        // the viewer's choice kept per device
        var showKey = 'vclu.cal.' + dashId + '.' + w.id;
        var allowed = { holidays: w.holidays !== false, namedays: !!w.namedays, sun: w.sun !== false };
        st.show = { holidays: allowed.holidays, namedays: allowed.namedays, sun: allowed.sun };
        try {
            var saved = JSON.parse(localStorage.getItem(showKey) || 'null');
            if (saved) {
                ['holidays', 'namedays', 'sun'].forEach(function(k) {
                    if (allowed[k] && typeof saved[k] === 'boolean') st.show[k] = saved[k];
                });
            }
        } catch (e) { /* no storage: defaults */ }
        var rel = (window.Intl && Intl.RelativeTimeFormat) ? new Intl.RelativeTimeFormat([], {numeric: 'auto'}) : null;
        var cap = function(t) { return t.charAt(0).toUpperCase() + t.slice(1); };

        var bar = calEl('div', 'dash-cal-bar');
        var nav = calEl('div', 'dash-cal-nav');
        var title = calEl('div', 'dash-cal-title');
        nav.appendChild(calIconBtn('chevron-left', '-', function() { step(-1); }));
        nav.appendChild(title);
        nav.appendChild(calIconBtn('chevron-right', '+', function() { step(1); }));
        var tools = calEl('div', 'dash-cal-tools');
        var todayBtn = calEl('button', 'dash-cal-today', rel ? cap(rel.format(0, 'day')) : 'Today');
        todayBtn.type = 'button';
        todayBtn.addEventListener('click', function() { st.day = st.today; st.month = st.day.slice(0, 7) + '-01'; load(); });
        var seg = calEl('div', 'dash-cal-seg');
        var segMonth = calIconBtn('calendar3', 'Month', function() { st.view = 'month'; st.month = st.day.slice(0, 7) + '-01'; load(); });
        var segDay = calIconBtn('calendar-day', 'Day', function() { st.view = 'day'; load(); });
        seg.appendChild(segMonth);
        seg.appendChild(segDay);
        var menuWrap = calEl('div', 'dash-cal-menu-wrap');
        var menu = calEl('div', 'dash-cal-menu');
        menu.hidden = true;
        function optionRow(key) {
            var b = calEl('button', 'dash-cal-opt');
            b.type = 'button';
            b.setAttribute('role', 'switch');
            b.appendChild(calEl('span', 'dash-cal-opt-label', T[key]));
            b.appendChild(calEl('span', 'dash-cal-opt-switch'));
            function sync() { b.setAttribute('aria-checked', st.show[key] ? 'true' : 'false'); }
            sync();
            b.addEventListener('click', function(ev) {
                ev.stopPropagation();
                st.show[key] = !st.show[key];
                sync();
                try { localStorage.setItem(showKey, JSON.stringify(st.show)); } catch (e) { /* per session only */ }
                if (st.data) draw();
            });
            return b;
        }
        ['holidays', 'namedays', 'sun'].forEach(function(k) { if (allowed[k]) menu.appendChild(optionRow(k)); });
        var menuBtn = calIconBtn('sliders', T.show, function() {
            menu.hidden = !menu.hidden;
            menuBtn.setAttribute('aria-expanded', menu.hidden ? 'false' : 'true');
        });
        menuBtn.setAttribute('aria-haspopup', 'true');
        menuBtn.setAttribute('aria-expanded', 'false');
        if (!calMenusListening) {
            document.addEventListener('click', closeCalMenus);
            calMenusListening = true;
        }
        menuWrap.appendChild(menuBtn);
        menuWrap.appendChild(menu);
        if (menu.childNodes.length) tools.appendChild(menuWrap);
        tools.appendChild(todayBtn);
        tools.appendChild(seg);
        bar.appendChild(head);
        bar.appendChild(nav);
        bar.appendChild(tools);
        var body = calEl('div', 'dash-cal-body');
        el.appendChild(bar);
        el.appendChild(body);

        function step(n) {
            if (st.view === 'day') {
                st.day = ymd(addDays(parseYmd(st.day), n));
            } else {
                var m = parseYmd(st.month);
                st.month = ymd(new Date(m.getFullYear(), m.getMonth() + n, 1));
            }
            load();
        }

        function range() {
            if (st.view === 'day') return [st.day, st.day];
            var first = parseYmd(st.month);
            var start = addDays(first, -((first.getDay() + 6) % 7)); // Monday
            return [ymd(start), ymd(addDays(start, 41))];
        }

        function load() {
            var r = range();
            var my = ++st.seq;
            segMonth.classList.toggle('active', st.view === 'month');
            segDay.classList.toggle('active', st.view === 'day');
            var url = '/api/dashboard/' + dashId + '/calendar?widget=' + encodeURIComponent(w.id) + '&from=' + r[0] + '&to=' + r[1];
            // a refused request (400, 403, 404) carries {error}: shown in the widget
            fetch(url).then(function(resp) {
                return resp.json().catch(function() { return {}; }).then(function(data) {
                    if (!resp.ok) return { error: (data && data.error) || ('HTTP ' + resp.status) };
                    return data;
                });
            }).then(function(data) {
                if (my !== st.seq) return; // a later request (navigation, refresh) supersedes it
                // the house's today: the first answer, or midnight on a wall tablet; a view left
                // on today follows it
                if (data.today && data.today !== st.today) {
                    var prev = st.today;
                    st.today = data.today;
                    var moved = false;
                    if (st.day === prev) { st.day = data.today; moved = st.view === 'day'; }
                    if (st.month === prev.slice(0, 7) + '-01') {
                        st.month = data.today.slice(0, 7) + '-01';
                        moved = moved || (st.view === 'month' && st.month !== prev.slice(0, 7) + '-01');
                    }
                    if (moved) { load(); return; }
                }
                st.data = data;
                draw();
            }).catch(function() {
                if (my !== st.seq) return;
                el.classList.add('has-error');
                body.textContent = '';
                var e = calEl('div', 'dash-cal-empty', '!');
                e.title = T.noConnection;
                body.appendChild(e);
            });
        }

        function itemsOn(date) {
            var out = [];
            var items = (st.data && st.data.items) || [];
            for (var i = 0; i < items.length; i++) {
                var it = items[i];
                if (it.date <= date && (it.last || it.date) >= date) out.push(it);
            }
            return out;
        }

        function dayInfo(date) {
            var days = (st.data && st.data.days) || [];
            for (var i = 0; i < days.length; i++) if (days[i].date === date) return days[i];
            return null;
        }

        function draw() {
            body.textContent = '';
            el.classList.toggle('has-error', !!(st.data && st.data.error));
            if (st.data && st.data.error) {
                body.appendChild(calEl('div', 'dash-cal-empty', st.data.error));
                setState(el, 'unknown', T.unknown);
                return;
            }
            if (st.view === 'day') drawDay(); else drawMonth();
            drawState();
        }

        // The state line: today's next event, when the shown range has today
        function drawState() {
            var r = range();
            if (st.today < r[0] || st.today > r[1]) return;
            var now = Date.now() / 1000, next = null, allDay = null;
            itemsOn(st.today).forEach(function(it) {
                if (it.allDay) { allDay = allDay || it; return; }
                if ((it.ends || it.at) >= now && (!next || it.at < next.at)) next = it;
            });
            if (next) setState(el, 'value', tm(next.at) + ' ' + next.title);
            else if (allDay) setState(el, 'value', allDay.title);
            else setState(el, 'off', T.nothingToday);
        }

        function drawMonth() {
            var m = parseYmd(st.month);
            title.textContent = cap(m.toLocaleDateString([], {month: 'long', year: 'numeric'}));
            var now = st.today;
            var grid = calEl('div', 'dash-cal-month');
            var start = parseYmd(range()[0]);
            for (var i = 0; i < 7; i++) {
                grid.appendChild(calEl('div', 'dash-cal-wd', addDays(start, i).toLocaleDateString([], {weekday: 'short'})));
            }
            for (var c = 0; c < 42; c++) {
                var d = addDays(start, c);
                var date = ymd(d);
                var info = dayInfo(date) || {};
                var cell = calEl('button', 'dash-cal-cell');
                cell.type = 'button';
                if (d.getMonth() !== m.getMonth()) cell.classList.add('other');
                if (date === now) cell.classList.add('today');
                if (info.holiday && st.show.holidays) cell.classList.add('holiday');
                else if (d.getDay() === 0 || d.getDay() === 6) cell.classList.add('weekend');
                cell.appendChild(calEl('span', 'dash-cal-num', String(d.getDate())));
                if (info.holidayName && st.show.holidays) cell.appendChild(calEl('span', 'dash-cal-holname', info.holidayName));
                var its = itemsOn(date);
                var list = calEl('div', 'dash-cal-chips');
                for (var k = 0; k < its.length && k < 3; k++) {
                    var chip = calEl('div', 'dash-cal-chip' + (its[k].paused ? ' paused' : ''), its[k].title);
                    chip.style.setProperty('--hue', calHue(its[k].id));
                    if (!its[k].allDay) chip.title = tm(its[k].at) + ' ' + its[k].title;
                    list.appendChild(chip);
                }
                // the server sends three events a day and the day's count
                var total = info.count != null ? Math.max(info.count, its.length) : its.length;
                var shown = Math.min(its.length, 3);
                if (total > shown) list.appendChild(calEl('div', 'dash-cal-more', '+' + (total - shown)));
                cell.appendChild(list);
                if (its.length) cell.setAttribute('aria-label', d.toLocaleDateString() + ': ' + its.map(function(x) { return x.title; }).join(', '));
                cell.addEventListener('click', (function(dt) {
                    return function() { st.day = dt; st.view = 'day'; load(); };
                })(date));
                grid.appendChild(cell);
            }
            body.appendChild(grid);
        }

        function drawDay() {
            var d = parseYmd(st.day);
            var info = dayInfo(st.day) || {};
            title.textContent = cap(d.toLocaleDateString([], {weekday: 'long', day: 'numeric', month: 'long'}));
            if (rel && st.data.today) {
                var diff = Math.round((d - parseYmd(st.data.today)) / 86400000);
                if (Math.abs(diff) <= 1) title.textContent = cap(rel.format(diff, 'day')) + ', ' + d.toLocaleDateString([], {day: 'numeric', month: 'long'});
            }
            var wrap = calEl('div', 'dash-cal-day');
            var notes = calEl('div', 'dash-cal-notes');
            if (info.holidayName && st.show.holidays) {
                var hol = calEl('div', 'dash-cal-note holiday');
                hol.appendChild(calEl('i', 'bi bi-flag-fill'));
                hol.appendChild(document.createTextNode(info.holidayName));
                notes.appendChild(hol);
            }
            if ((info.sunrise || info.polar) && st.show.sun) {
                var sn = calEl('div', 'dash-cal-note sun');
                if (info.polar) {
                    sn.appendChild(calEl('i', 'bi bi-sun'));
                    sn.appendChild(document.createTextNode(info.polar === 'day' ? T.polarDay : T.polarNight));
                } else {
                    sn.appendChild(calEl('i', 'bi bi-sunrise'));
                    sn.appendChild(document.createTextNode(tm(info.sunrise)));
                    sn.appendChild(calEl('i', 'bi bi-sunset dash-cal-note-gap'));
                    sn.appendChild(document.createTextNode(tm(info.sunset)));
                }
                notes.appendChild(sn);
            }
            if (info.namedays && info.namedays.length && st.show.namedays) {
                var nd = calEl('div', 'dash-cal-note');
                nd.appendChild(calEl('i', 'bi bi-balloon'));
                nd.appendChild(calEl('span', 'dash-cal-note-label', T.namedays + ':'));
                nd.appendChild(document.createTextNode(' ' + info.namedays.join(', ')));
                notes.appendChild(nd);
            }
            if (notes.childNodes.length) wrap.appendChild(notes);
            if (st.data.schedules && st.data.schedules.length) wrap.appendChild(drawSchedules(st.data.schedules, st.day === st.data.today));
            var its = itemsOn(st.day);
            var allDay = its.filter(function(x) { return x.allDay; });
            var timed = its.filter(function(x) { return !x.allDay; });
            if (allDay.length) {
                var row = calEl('div', 'dash-cal-allday');
                allDay.forEach(function(it) {
                    var chip = calEl('div', 'dash-cal-chip' + (it.paused ? ' paused' : ''), it.title);
                    chip.style.setProperty('--hue', calHue(it.id));
                    row.appendChild(chip);
                });
                wrap.appendChild(row);
            }
            var nowTs = Date.now() / 1000;
            var isToday = st.day === st.data.today;
            var lineDrawn = false;
            var listEl = calEl('div', 'dash-cal-list');
            function nowLine() {
                var l = calEl('div', 'dash-cal-now');
                l.appendChild(calEl('span', null, tm(nowTs)));
                listEl.appendChild(l);
                lineDrawn = true;
            }
            timed.forEach(function(it) {
                if (isToday && !lineDrawn && it.at > nowTs) nowLine();
                var row = calEl('div', 'dash-cal-item');
                row.style.setProperty('--hue', calHue(it.id));
                if (it.ends ? it.ends <= nowTs : it.at < nowTs) row.classList.add('past');
                if (it.at <= nowTs && it.ends && it.ends > nowTs) row.classList.add('now');
                if (it.paused) row.classList.add('paused');
                var time = tm(it.at) + (it.ends ? '–' + tm(it.ends) : '');
                row.appendChild(calEl('div', 'dash-cal-time', time));
                var name = calEl('div', 'dash-cal-name');
                if (it.icon) {
                    var ic = calEl('i', 'bi bi-' + String(it.icon).replace(/[^a-z0-9-]/gi, ''));
                    name.appendChild(ic);
                }
                name.appendChild(document.createTextNode(it.title));
                row.appendChild(name);
                listEl.appendChild(row);
            });
            if (isToday && !lineDrawn && timed.length) nowLine();
            if (!its.length && !(st.data.schedules && st.data.schedules.length)) {
                var empty = calEl('div', 'dash-cal-empty');
                empty.innerHTML = '<i class="bi bi-calendar-check"></i>';
                wrap.appendChild(empty);
            }
            wrap.appendChild(listEl);
            body.appendChild(wrap);
        }

        // A schedule as a 24 h bar: one segment per value, the current time marked
        function drawSchedules(list, isToday) {
            var box = calEl('div', 'dash-cal-sched');
            var nowTs = Date.now() / 1000;
            list.forEach(function(sc) {
                var segs = sc.segments || [];
                if (!segs.length) return;
                var start = segs[0].from, span = segs[segs.length - 1].to - start;
                var row = calEl('div', 'dash-cal-sched-row');
                row.appendChild(calEl('div', 'dash-cal-sched-title', sc.title || sc.id));
                var bar = calEl('div', 'dash-cal-sched-bar');
                // with colours set, a value without one is neutral (e.g. "closed")
                var anyColor = segs.some(function(x) { return !!x.color; });
                segs.forEach(function(sg) {
                    var seg = calEl('div', 'dash-cal-seg-v', sg.value);
                    seg.style.left = ((sg.from - start) / span * 100) + '%';
                    seg.style.width = ((sg.to - sg.from) / span * 100) + '%';
                    var c = resolveColor(sg.color);
                    if (c) seg.style.setProperty('--seg', c);
                    else if (anyColor) seg.classList.add('neutral');
                    else seg.style.setProperty('--seg', 'hsl(' + calHue(sg.value) + ', 55%, 50%)');
                    seg.title = sg.value + '  ' + tm(sg.from) + '\u2013' + tm(sg.to);
                    if (isToday && sg.from <= nowTs && sg.to > nowTs) seg.classList.add('now');
                    bar.appendChild(seg);
                });
                if (isToday && nowTs >= start && nowTs < start + span) {
                    var mark = calEl('div', 'dash-cal-sched-now');
                    mark.style.left = ((nowTs - start) / span * 100) + '%';
                    bar.appendChild(mark);
                }
                row.appendChild(bar);
                box.appendChild(row);
            });
            var ticks = calEl('div', 'dash-cal-sched-ticks');
            ticks.appendChild(calEl('div'));
            var axis = calEl('div', 'dash-cal-sched-axis');
            // ticks at 0, 6, 12, 18 and 24 on the house's clock: a DST day has 23 or 25 hours
            var first = list.filter(function(sc) { return sc.segments && sc.segments.length; })[0];
            if (first) {
                var segs0 = first.segments, d0 = segs0[0].from, span0 = segs0[segs0.length - 1].to - d0;
                var zone = st.data && st.data.zone;
                var addTick = function(label, at) {
                    var t = calEl('span', null, label);
                    t.style.left = ((at - d0) / span0 * 100) + '%';
                    axis.appendChild(t);
                };
                for (var at = d0; at < d0 + span0; at += 3600) {
                    var h = zoneHour(at, zone);
                    if (h % 6 === 0) addTick(pad2(h), at);
                }
                addTick('24', d0 + span0);
            }
            ticks.appendChild(axis);
            box.appendChild(ticks);
            return box;
        }

        calendars[w.id] = { refresh: load };
        load();
        startChartTimer('cal-' + w.id, load, 60000);
    }

    function renderGroup(w) {
        var el = document.createElement('div');
        el.className = 'dash-group-label';
        if (w.icon) {
            el.innerHTML = '<span class="dash-group-icon"><i class="bi bi-' + esc(w.icon) + '"></i></span>';
        }
        el.innerHTML += esc(w.label);
        return el;
    }

    // ---- Cover widget ----

    function renderCover(el, w) {

        var controls = document.createElement('div');
        controls.className = 'dash-cover-controls';

        var btnOpen = document.createElement('button');
        btnOpen.className = 'dash-cover-btn' + (w.state === 1 ? ' active' : '');
        btnOpen.innerHTML = '<i class="bi bi-chevron-up"></i>';
        btnOpen.addEventListener('click', function(e) {
            e.stopPropagation();
            executeAction(w.id, 'open');
        });

        var btnStop = document.createElement('button');
        btnStop.className = 'dash-cover-btn' + (w.state === 0 || w.state == null ? ' active' : '');
        btnStop.innerHTML = '<i class="bi bi-stop-fill"></i>';
        btnStop.addEventListener('click', function(e) {
            e.stopPropagation();
            executeAction(w.id, 'stop');
        });

        var btnClose = document.createElement('button');
        btnClose.className = 'dash-cover-btn' + (w.state === 2 ? ' active' : '');
        btnClose.innerHTML = '<i class="bi bi-chevron-down"></i>';
        btnClose.addEventListener('click', function(e) {
            e.stopPropagation();
            executeAction(w.id, 'close');
        });

        controls.appendChild(btnOpen);
        controls.appendChild(btnStop);
        controls.appendChild(btnClose);
        el.appendChild(controls);

        if (w.capabilities && w.capabilities.set) {
            var row = document.createElement('div');
            row.className = 'dash-slider-row';

            var input = document.createElement('input');
            input.type = 'range';
            input.className = 'dash-slider-input';
            input.min = 0;
            input.max = 100;
            input.value = w.position != null ? w.position : 0;
            input.dataset.widgetId = w.id;

            bindSlider(input, function(v) { refreshTile(el, w, { position: v, state: 0 }); },
                function(v) { executeAction(w.id, 'set', v); }, 500);

            row.appendChild(input);
            el.appendChild(row);
        }
    }

    // ---- Cover group ----

    // Up, stop, down for every cover; a slider with the average position when a cover can be
    // set, a slat slider while one reports a tilt
    function renderCovers(el, w) {
        var controls = calEl('div', 'dash-cover-controls');
        [['open', 'chevron-up', T.up], ['stop', 'stop-fill', T.stop], ['close', 'chevron-down', T.down]].forEach(function(b) {
            var btn = calEl('button', 'dash-cover-btn');
            btn.type = 'button';
            btn.title = b[2];
            btn.setAttribute('aria-label', b[2]);
            btn.appendChild(calEl('i', 'bi bi-' + b[1]));
            btn.addEventListener('click', function(e) {
                e.stopPropagation();
                if (el.classList.contains('disabled')) return;
                btn.classList.add('active');
                setTimeout(function() { btn.classList.remove('active'); }, 300);
                executeAction(w.id, b[0]);
            });
            controls.appendChild(btn);
        });
        el.appendChild(controls);

        coversSlider(el, w, 'position', 'arrows-vertical', T.position, 100, ' %', 'set');
        coversSlider(el, w, 'tilt', 'justify', T.tilt, 90, '°', 'tilt');
        updateCovers(el, w);
    }

    function coversSlider(el, w, role, icon, title, max, unit, action) {
        var row = calEl('div', 'dash-slider-row dash-color-row');
        row.title = title;
        row.dataset.role = role;
        row.appendChild(calEl('i', 'bi bi-' + icon + ' dash-color-row-icon'));
        var slider = document.createElement('input');
        slider.type = 'range';
        slider.className = 'dash-slider-input';
        slider.min = 0;
        slider.max = max;
        slider.dataset.role = role;
        slider.setAttribute('aria-label', title);
        var valEl = calEl('span', 'dash-slider-value');
        valEl.dataset.role = role;
        bindSlider(slider, function(v) { valEl.textContent = v + unit; },
            function(v) { executeAction(w.id, action, v); }, 500);
        row.appendChild(slider);
        row.appendChild(valEl);
        el.appendChild(row);
    }

    // Sliders and the average from a definition, a poll or a reply
    // (a whole entry has the counts; a set's reply only its position)
    function updateCovers(el, d) {
        var whole = d.open !== undefined;
        [['position', ' %'], ['tilt', '°']].forEach(function(r) {
            var row = el.querySelector('.dash-slider-row[data-role="' + r[0] + '"]');
            if (whole) row.hidden = r[0] === 'position' ? !d.set : d.tilt == null;
            var sl = row.querySelector('.dash-slider-input');
            var v = d[r[0]];
            if (v == null || userHolds(sl)) return;
            sl.value = v;
            row.querySelector('.dash-slider-value').textContent = v + r[1];
        });
    }

    // A section header's "2 otwarte · 1 w ruchu"; nothing while it has no covers with a reading
    function updateSectionCovers(si, c) {
        var el = sectionCoverEls[si];
        if (!el) return;
        var text = c ? coverCountsText(c) : null;
        el.textContent = text || '';
        el.hidden = !text;
    }

    // ---- Media player ----

    var playerKeys = ['playing', 'title', 'artist', 'album', 'artwork', 'volume', 'muted', 'power', 'online', 'error'];

    function playerBtn(el, cls, icon, title, onClick) {
        var b = calEl('button', 'dash-player-btn ' + cls);
        b.type = 'button';
        b.appendChild(calEl('i', 'bi bi-' + icon));
        b.title = title;
        b.setAttribute('aria-label', title);
        b.addEventListener('click', function(e) {
            e.stopPropagation();
            if (el.classList.contains('disabled')) return;
            onClick();
        });
        return b;
    }

    // Pause while playing (when it can), play otherwise
    function playerPauses(el, d) {
        var caps = el._caps;
        return !!d.playing && caps.pause || !caps.play;
    }

    // Artwork on the left (a note while there is none or it does not load), title and artist,
    // the buttons the definition has, the volume
    function renderPlayer(el, w, head) {
        var caps = el._caps = w.capabilities || {};
        el.style.cursor = 'default';
        var box = calEl('div', 'dash-player');
        var art = head.querySelector('.dash-widget-icon');
        art.classList.add('dash-player-art');
        var img = document.createElement('img');
        img.alt = '';
        img.hidden = true;
        img.referrerPolicy = 'no-referrer';
        // a URL that failed is not asked for again on every poll; a new track brings a new one
        img.addEventListener('load', function() {
            if (!img.dataset.src) return;
            img.hidden = false;
            art.classList.add('has-art');
        });
        img.addEventListener('error', function() {
            if (!img.dataset.src) return;
            el._artFailed = img.dataset.src;
            img.hidden = true;
            art.classList.remove('has-art');
        });
        art.insertBefore(img, art.firstChild);
        box.appendChild(art);

        var info = calEl('div', 'dash-player-info');
        info.appendChild(head.querySelector('.dash-tile-text'));
        info.appendChild(calEl('div', 'dash-player-title'));
        info.appendChild(calEl('div', 'dash-player-artist'));
        box.appendChild(info);

        var ctl = calEl('div', 'dash-player-controls');
        if (caps.previous) ctl.appendChild(playerBtn(el, 'dash-player-prev', 'skip-start-fill', T.previous, function() {
            executeAction(w.id, 'previous');
        }));
        if (caps.play || caps.pause) ctl.appendChild(playerBtn(el, 'dash-player-play', 'play-fill', T.play, function() {
            var pause = playerPauses(el, el._data);
            refreshTile(el, w, { playing: !pause });
            executeAction(w.id, pause ? 'pause' : 'play');
        }));
        if (caps.next) ctl.appendChild(playerBtn(el, 'dash-player-next', 'skip-end-fill', T.next, function() {
            executeAction(w.id, 'next');
        }));
        var side = calEl('div', 'dash-player-side');
        if (caps.mute) side.appendChild(playerBtn(el, 'dash-player-mute', 'volume-up-fill', T.mute, function() {
            var m = !el._data.muted;
            refreshTile(el, w, { muted: m });
            executeAction(w.id, 'mute', m);
        }));
        if (caps.power) side.appendChild(playerBtn(el, 'dash-player-power', 'power', T.power, function() {
            var on = el._data.power === false;
            refreshTile(el, w, { power: on });
            executeAction(w.id, 'power', on);
        }));
        if (side.firstChild) ctl.appendChild(side);
        if (ctl.firstChild) box.appendChild(ctl);

        if (caps.volume) {
            var row = calEl('div', 'dash-slider-row dash-color-row dash-player-volume');
            row.title = T.volume;
            row.appendChild(calEl('i', 'bi bi-volume-down dash-color-row-icon'));
            var slider = document.createElement('input');
            slider.type = 'range';
            slider.className = 'dash-slider-input';
            slider.min = 0;
            slider.max = 100;
            slider.setAttribute('aria-label', T.volume);
            var valEl = calEl('span', 'dash-slider-value', '--');
            bindSlider(slider, function(v) { valEl.textContent = v + ' %'; },
                function(v) { executeAction(w.id, 'volume', v); }, 500);
            row.appendChild(slider);
            row.appendChild(valEl);
            box.appendChild(row);
        }
        el.appendChild(box);
    }

    // Texts, artwork, buttons and volume from what is known (definition, polls, replies)
    function updatePlayer(el, d) {
        var title = el.querySelector('.dash-player-title');
        var artist = el.querySelector('.dash-player-artist');
        title.textContent = d.title || '';
        title.hidden = !d.title;
        artist.textContent = [d.artist, d.album].filter(function(s) { return !!s; }).join(' · ');
        artist.hidden = !artist.textContent;

        var art = el.querySelector('.dash-player-art');
        var img = art.querySelector('img');
        var url = typeof d.artwork === 'string' && /^https?:\/\//i.test(d.artwork) ? d.artwork : '';
        if (!url || url === el._artFailed) {
            img.hidden = true;
            art.classList.remove('has-art');
            if (!url && img.dataset.src) { delete img.dataset.src; img.removeAttribute('src'); }
        } else if (img.dataset.src !== url) {
            img.hidden = true;
            art.classList.remove('has-art');
            img.dataset.src = url;
            img.src = url;
        }

        var play = el.querySelector('.dash-player-play');
        if (play) {
            var pause = playerPauses(el, d);
            play.firstChild.className = 'bi bi-' + (pause ? 'pause-fill' : 'play-fill');
            play.title = pause ? T.pause : T.play;
            play.setAttribute('aria-label', play.title);
        }
        var mute = el.querySelector('.dash-player-mute');
        if (mute) {
            mute.firstChild.className = 'bi bi-' + (d.muted ? 'volume-mute-fill' : 'volume-up-fill');
            mute.classList.toggle('active', !!d.muted);
            mute.title = d.muted ? T.unmute : T.mute;
            mute.setAttribute('aria-label', mute.title);
            mute.setAttribute('aria-pressed', d.muted ? 'true' : 'false');
        }
        var power = el.querySelector('.dash-player-power');
        if (power) power.setAttribute('aria-pressed', d.power !== false ? 'true' : 'false');
        // switched off or unreachable: the transport and the volume dim, power stays
        el.classList.toggle('dash-player-idle', d.power === false || d.online === false);

        var vol = el.querySelector('.dash-player-volume');
        if (vol) {
            var sl = vol.querySelector('.dash-slider-input');
            if (!userHolds(sl)) {
                if (d.volume != null) sl.value = d.volume;
                vol.querySelector('.dash-slider-value').textContent = d.volume != null ? d.volume + ' %' : '--';
            }
        }
    }

    // ---- Dimmer widget ----

    // A slider sends the value it was moved to: after a pause, or at once when let go, so
    // a page reload right after does not lose it. Until then a poll leaves it alone
    // (on a phone a dragged slider has no focus).
    function bindSlider(input, show, send, delay) {
        var timer = null, pending = null;
        function flush() {
            clearTimeout(timer);
            timer = null;
            if (pending === null) return;
            var v = pending;
            pending = null;
            delete input.dataset.pending;
            send(v);
        }
        input.addEventListener('input', function() {
            pending = parseFloat(input.value);
            input.dataset.pending = '1';
            show(pending);
            clearTimeout(timer);
            timer = setTimeout(flush, delay);
        });
        input.addEventListener('change', flush);
    }

    // The user is moving it, or it has a value not sent yet: a poll must not overwrite it
    function userHolds(input) {
        return document.activeElement === input || input.dataset.pending === '1';
    }

    function renderDimmer(el, w) {
        el.style.cursor = 'default';

        var caps = w.capabilities || {};
        var isOn = w.on !== false;

        if (caps.toggle) {
            var head = el.querySelector('.dash-tile-head');
            head.classList.add('dash-tile-tap');
            headSwitch(el, 'dash-dimmer-toggle', isOn, function(on) {
                refreshTile(el, w, { on: on });
                el.querySelector('.dash-slider-input').classList.toggle('dimmed', !on);
                executeAction(w.id, on ? 'switchOn' : 'switchOff');
            }, head);
        }

        var row = document.createElement('div');
        row.className = 'dash-slider-row';

        var slider = document.createElement('input');
        slider.type = 'range';
        slider.className = 'dash-slider-input';
        slider.min = 0;
        slider.max = 100;
        slider.value = w.value != null ? w.value : 0;
        slider.dataset.widgetId = w.id;
        if (!isOn) slider.classList.add('dimmed');

        bindSlider(slider, function(v) { refreshTile(el, w, { value: v, on: v > 0 }); },
            function(v) { executeAction(w.id, 'set', v); }, 500);

        row.appendChild(slider);
        el.appendChild(row);
    }

    // ---- Color picker widget ----

    // The light's own colour tints the tile (icon, switch, slider thumbs) while it is on,
    // unless the dashboard gave the widget a colour.
    function applyLightColor(el, rgb, isOn) {
        if (el.dataset.ownColor) return;
        if (rgb && isOn) {
            el.style.setProperty('--widget-color', 'rgb(' + rgb.r + ',' + rgb.g + ',' + rgb.b + ')');
        } else {
            el.style.removeProperty('--widget-color');
        }
    }

    function markPreset(el, rgb) {
        var dots = el.querySelectorAll('.dash-color-preset:not(.dash-color-custom)');
        for (var i = 0; i < dots.length; i++) {
            var c = dots[i].style.backgroundColor.match(/(\d+)/g);
            var near = false;
            if (rgb && c) {
                var dr = (rgb.r || 0) - parseInt(c[0]);
                var dg = (rgb.g || 0) - parseInt(c[1]);
                var db = (rgb.b || 0) - parseInt(c[2]);
                near = Math.sqrt(dr*dr + dg*dg + db*db) < 30;
            }
            dots[i].classList.toggle('selected', near);
        }
    }

    function toHex(rgb) {
        function h(v) { v = Math.max(0, Math.min(255, Math.round(v || 0))); return (v < 16 ? '0' : '') + v.toString(16); }
        return '#' + h(rgb.r) + h(rgb.g) + h(rgb.b);
    }

    // A compact slider row: small icon, range 0..100, value in %.
    function colorSlider(el, w, role, icon, title, value, action) {
        var row = document.createElement('div');
        row.className = 'dash-slider-row dash-color-row';
        row.title = title;

        var ic = document.createElement('i');
        ic.className = 'bi bi-' + icon + ' dash-color-row-icon';
        row.appendChild(ic);

        var slider = document.createElement('input');
        slider.type = 'range';
        slider.className = 'dash-slider-input';
        slider.min = 0;
        slider.max = 100;
        slider.value = value != null ? value : 0;
        slider.dataset.role = role;

        var valEl = document.createElement('span');
        valEl.className = 'dash-slider-value';
        valEl.dataset.role = role;
        valEl.textContent = value != null ? value + '%' : '--';

        bindSlider(slider, function(v) {
            valEl.textContent = v + '%';
            if (role === 'brightness') refreshTile(el, w, { brightness: v, on: v > 0 });
        }, function(v) { executeAction(w.id, action, v); }, 500);

        row.appendChild(slider);
        row.appendChild(valEl);
        el.appendChild(row);
    }

    function renderColor(el, w) {
        el.style.cursor = 'default';

        var caps = w.capabilities || {};
        var isOn = w.on !== false;
        applyLightColor(el, w.rgb, isOn);

        if (caps.toggle) {
            var head = el.querySelector('.dash-tile-head');
            head.classList.add('dash-tile-tap');
            headSwitch(el, 'dash-color-toggle', isOn, function(on) {
                refreshTile(el, w, { on: on });
                executeAction(w.id, on ? 'switchOn' : 'switchOff');
                if (!on) applyLightColor(el, null, false);
            }, head);
        }

        var presetsRow = document.createElement('div');
        presetsRow.className = 'dash-color-presets';

        (w.presets || []).forEach(function(preset) {
            var dot = document.createElement('button');
            dot.className = 'dash-color-preset';
            dot.style.backgroundColor = 'rgb(' + preset.r + ',' + preset.g + ',' + preset.b + ')';
            dot.title = preset.label || '';
            dot.addEventListener('click', function(e) {
                e.stopPropagation();
                executeAction(w.id, 'setColor', { r: preset.r, g: preset.g, b: preset.b });
                var rgb = { r: preset.r, g: preset.g, b: preset.b };
                markPreset(el, rgb);
                applyLightColor(el, rgb, true);
            });
            presetsRow.appendChild(dot);
        });

        // Any other colour: the last dot opens the system colour picker.
        var custom = document.createElement('label');
        custom.className = 'dash-color-preset dash-color-custom';
        custom.title = 'Colour';
        var picker = document.createElement('input');
        picker.type = 'color';
        picker.value = w.rgb ? toHex(w.rgb) : '#ffffff';
        picker.addEventListener('change', function(e) {
            e.stopPropagation();
            var v = picker.value;
            var rgb = { r: parseInt(v.substr(1, 2), 16), g: parseInt(v.substr(3, 2), 16), b: parseInt(v.substr(5, 2), 16) };
            executeAction(w.id, 'setColor', rgb);
            markPreset(el, rgb);
            applyLightColor(el, rgb, true);
        });
        custom.appendChild(picker);
        presetsRow.appendChild(custom);

        el.appendChild(presetsRow);
        markPreset(el, w.rgb);

        if (caps.brightness) {
            colorSlider(el, w, 'brightness', 'brightness-high', 'Brightness', w.brightness, 'set');
        }
        if (caps.white) {
            colorSlider(el, w, 'white', 'lightbulb', 'White', w.white, 'setWhite');
        }
    }

    // ---- Chart data helpers ----

    // Moving average smoothing (window=3 by default)
    function smoothValues(vals, win) {
        if (!win) win = 3;
        if (vals.length < win) return vals;
        var out = [];
        var half = Math.floor(win / 2);
        for (var i = 0; i < vals.length; i++) {
            var sum = 0, count = 0;
            for (var j = Math.max(0, i - half); j <= Math.min(vals.length - 1, i + half); j++) {
                sum += vals[j]; count++;
            }
            out.push(Math.round(sum / count * 100) / 100);
        }
        return out;
    }

    // Downsample to ~maxPts points (every-Nth, keeping first and last)
    function downsample(data, maxPts) {
        if (!maxPts) maxPts = 300;
        if (data.length <= maxPts) return data;
        var step = data.length / maxPts;
        var out = [data[0]];
        for (var i = 1; i < maxPts - 1; i++) {
            out.push(data[Math.round(i * step)]);
        }
        out.push(data[data.length - 1]);
        return out;
    }

    // Auto-pad Y axis: ensure at least minRange visible (avoids noise-zoom)
    function calcYBounds(values, wMin, wMax, minRange) {
        if (wMin != null && wMax != null) return {min: wMin, max: wMax};
        if (!minRange) minRange = 2;
        if (!values.length) return {min: wMin, max: wMax};
        var lo = values[0], hi = values[0];
        for (var i = 1; i < values.length; i++) {
            if (values[i] < lo) lo = values[i];
            if (values[i] > hi) hi = values[i];
        }
        var range = hi - lo;
        if (range < minRange) {
            var mid = (lo + hi) / 2;
            lo = mid - minRange / 2;
            hi = mid + minRange / 2;
        } else {
            // Small padding (10%)
            var pad = range * 0.1;
            lo -= pad;
            hi += pad;
        }
        return {
            min: wMin != null ? wMin : Math.floor(lo * 10) / 10,
            max: wMax != null ? wMax : Math.ceil(hi * 10) / 10
        };
    }

    // The theme's accent as a value a canvas can draw with
    function themeAccent() {
        var c = getComputedStyle(document.documentElement).getPropertyValue('--dash-accent').trim();
        return /^#[0-9a-f]{3}([0-9a-f]{3})?$/i.test(c) ? c : 'rgba(39,194,168,1)';
    }

    // Resolve color to an rgba string (for fill backgrounds)
    function colorToRGBA(c, alpha) {
        // Already rgba
        if (c.indexOf('rgba') === 0) return c.replace(/[\d.]+\)$/, alpha + ')');
        // Hex
        if (c[0] === '#') {
            var hex = c.slice(1);
            if (hex.length === 3) hex = hex[0]+hex[0]+hex[1]+hex[1]+hex[2]+hex[2];
            var r = parseInt(hex.slice(0,2),16), g = parseInt(hex.slice(2,4),16), b = parseInt(hex.slice(4,6),16);
            return 'rgba('+r+','+g+','+b+','+alpha+')';
        }
        // CSS var or named — can't convert, return transparent
        return 'rgba(39,194,168,' + alpha + ')';
    }

    // ---- Chart / Sparkline rendering ----

    function renderChart(el, w) {
        el.classList.add('dash-widget-chart-container');
        el.style.cursor = 'default';

        var wrap = document.createElement('div');
        wrap.className = 'dash-chart-wrap';
        var canvas = document.createElement('canvas');
        canvas.id = 'chart-' + w.id;
        wrap.appendChild(canvas);
        el.appendChild(wrap);
        setState(el, 'unknown', T.unknown);

        fetchChartData(w, canvas);
        startChartTimer(w.id, function() {
            var c = document.getElementById('chart-' + w.id);
            if (c) fetchChartData(w, c);
        }, chartRefreshMs(w));
    }

    function fetchChartData(w, canvas) {
        var range = w.range || '2h';
        fetch('/api/dashboard/' + dashId + '/metrics?path=' + encodeURIComponent(w.metricPath) + '&range=' + range)
            .then(function(r) { return r.json(); })
            .then(function(data) {
                // Downsample long ranges
                data = downsample(data, 300);

                var labels = data.map(function(d) {
                    var dt = new Date(d[0] * 1000);
                    if (range === '7d') {
                        return dt.toLocaleDateString([], {weekday:'short', day:'numeric'});
                    }
                    return dt.toLocaleTimeString([], {hour:'2-digit', minute:'2-digit'});
                });
                var rawValues = data.map(function(d) { return d[1]; });
                var tile = canvas.closest('.dash-widget');
                if (tile && rawValues.length) {
                    setState(tile, 'value', withUnit(Math.round(rawValues[rawValues.length - 1] * 10) / 10, w.unit));
                }

                // Smooth to reduce sensor jitter
                var values = smoothValues(rawValues, 3);

                var color = resolveColor(w.color) || themeAccent();
                var fillBg = colorToRGBA(color, 0.08);
                var yBounds = calcYBounds(values, w.min, w.max, 2);

                if (chartInstances[w.id]) {
                    var chart = chartInstances[w.id];
                    chart.data.labels = labels;
                    chart.data.datasets[0].data = values;
                    chart.options.scales.y.min = yBounds.min;
                    chart.options.scales.y.max = yBounds.max;
                    chart.update('none');
                } else {
                    chartInstances[w.id] = new Chart(canvas, {
                        type: 'line',
                        data: {
                            labels: labels,
                            datasets: [{
                                data: values,
                                borderColor: color,
                                backgroundColor: w.fill === false ? 'transparent' : fillBg,
                                fill: w.fill !== false,
                                borderWidth: 2,
                                pointRadius: 0,
                                tension: 0.3,
                            }]
                        },
                        options: {
                            responsive: true,
                            maintainAspectRatio: false,
                            scales: {
                                x: {
                                    ticks: { color: 'rgba(128,128,128,0.9)', maxTicksLimit: 6, font: {size: 10} },
                                    grid: { color: 'rgba(128,128,128,0.12)' },
                                },
                                y: {
                                    min: yBounds.min,
                                    max: yBounds.max,
                                    ticks: {
                                        color: 'rgba(128,128,128,0.9)', font: {size: 10},
                                        callback: function(v) { return v + (w.unit ? ' ' + w.unit : ''); }
                                    },
                                    grid: { color: 'rgba(128,128,128,0.12)' },
                                }
                            },
                            interaction: { intersect: false, mode: 'index' },
                            plugins: {
                                legend: { display: false },
                                tooltip: {
                                    callbacks: {
                                        title: function(items) {
                                            var idx = items[0].dataIndex;
                                            var ts = data[idx][0] * 1000;
                                            var d = new Date(ts);
                                            if (range === '7d') {
                                                return d.toLocaleDateString([], {weekday:'short', month:'short', day:'numeric'})
                                                    + ' ' + d.toLocaleTimeString([], {hour:'2-digit', minute:'2-digit'});
                                            } else if (range === '24h') {
                                                return d.toLocaleDateString([], {weekday:'short'})
                                                    + ' ' + d.toLocaleTimeString([], {hour:'2-digit', minute:'2-digit'});
                                            }
                                            return d.toLocaleTimeString([], {hour:'2-digit', minute:'2-digit', second:'2-digit'});
                                        },
                                        label: function(item) {
                                            var v = parseFloat(item.raw);
                                            var formatted = isNaN(v) ? item.formattedValue : v.toFixed(1);
                                            return formatted + (w.unit ? ' ' + w.unit : '');
                                        }
                                    }
                                }
                            },
                            animation: { duration: 0 },
                        }
                    });
                }
            });
    }

    function fetchSparkline(w, canvas) {
        var range = w.sparkRange || '2h';
        fetch('/api/dashboard/' + dashId + '/metrics?path=' + encodeURIComponent(w.spark) + '&range=' + range)
            .then(function(r) { return r.json(); })
            .then(function(data) {
                data = downsample(data, 60);
                var values = smoothValues(data.map(function(d) { return d[1]; }), 3);
                var color = resolveColor(w.color) || themeAccent();
                var key = 'spark-' + w.id;

                if (chartInstances[key]) {
                    var chart = chartInstances[key];
                    chart.data.labels = values.map(function(_, i) { return i; });
                    chart.data.datasets[0].data = values;
                    chart.update('none');
                } else {
                    chartInstances[key] = new Chart(canvas, {
                        type: 'line',
                        data: {
                            labels: values.map(function(_, i) { return i; }),
                            datasets: [{
                                data: values,
                                borderColor: color,
                                backgroundColor: function(ctx) {
                                    var chart = ctx.chart;
                                    var area = chart.chartArea;
                                    if (!area) return colorToRGBA(color, 0.15);
                                    var grad = chart.ctx.createLinearGradient(0, area.top, 0, area.bottom);
                                    grad.addColorStop(0, colorToRGBA(color, 0.2));
                                    grad.addColorStop(1, colorToRGBA(color, 0.02));
                                    return grad;
                                },
                                fill: true,
                                borderWidth: 1.5,
                                pointRadius: 0,
                                tension: 0.35,
                                borderCapStyle: 'round',
                                borderJoinStyle: 'round',
                            }]
                        },
                        options: {
                            responsive: true,
                            maintainAspectRatio: false,
                            plugins: { legend: { display: false }, tooltip: { enabled: false } },
                            scales: { x: { display: false }, y: { display: false } },
                            animation: { duration: 0 },
                            interaction: { enabled: false },
                        }
                    });
                }
            });
    }

    function startChartTimer(id, fn, ms) {
        stopChartTimer(id);
        chartTimers[id] = setInterval(fn, ms);
    }

    function stopChartTimer(id) {
        if (chartTimers[id]) { clearInterval(chartTimers[id]); delete chartTimers[id]; }
    }

    function stopAllChartTimers() {
        for (var id in chartTimers) stopChartTimer(id);
    }

    function destroyAllCharts() {
        for (var id in chartInstances) {
            chartInstances[id].destroy();
            delete chartInstances[id];
        }
    }

    // A tap or Enter/Space runs it, unless the chip is disabled
    function bindHeaderTap(el, role, label, tap) {
        el.classList.add('dash-header-tap');
        el.setAttribute('role', role);
        el.setAttribute('aria-label', label);
        el.tabIndex = 0;
        var run = function() { if (!el.classList.contains('disabled')) tap(); };
        el.addEventListener('click', run);
        // one action per key press
        el.addEventListener('keydown', function(e) {
            if (e.key !== 'Enter' && e.key !== ' ') return;
            e.preventDefault();
            if (!e.repeat) run();
        });
    }

    // A header chip (#93): text, icon and colour from its function; no state hides it
    function renderChip(w) {
        var el = document.createElement('div');
        el.className = 'dash-header-item dash-header-chip';
        el.dataset.widgetId = w.id;
        el.dataset.type = 'chip';
        var icon = calEl('span', 'dash-header-icon');
        icon.appendChild(document.createElement('i'));
        el.appendChild(icon);
        el.appendChild(calEl('span', 'dash-header-value'));
        if (w.link) {
            bindHeaderTap(el, 'link', '', function() {
                if (w.link.charAt(0) === '/') location.href = w.link;
                else window.open(w.link, '_blank', 'noopener');
            });
        }
        if (w.disabled) {
            el.classList.add('disabled');
            el.setAttribute('aria-disabled', 'true');
        }
        widgetEls[w.id] = el;
        updateChip(el, w, w.chip);
        return el;
    }

    function updateChip(el, w, d) {
        el.hidden = !d;
        if (!d) return;
        var text = d.error ? '--' : (d.text != null ? fmtNum(d.text) : '');
        var ic = String(d.icon || w.icon || 'circle').replace(/[^a-z0-9-]/gi, '');
        el.querySelector('.dash-header-icon i').className = 'bi bi-' + ic;
        el.querySelector('.dash-header-value').textContent = text;
        el.classList.toggle('dash-chip-icon-only', text === '');
        el.dataset.state = d.error ? 'unknown' : (isAlarm(d.color) ? 'alarm' : 'value');
        applyColor(el, d.color);
        if (d.disabled !== undefined) {
            el.classList.toggle('disabled', !!d.disabled);
            if (d.disabled) el.setAttribute('aria-disabled', 'true');
            else el.removeAttribute('aria-disabled');
        }
        var errEl = el.querySelector('.dash-widget-error');
        if (d.error && !errEl) {
            errEl = calEl('span', 'dash-widget-error');
            errEl.innerHTML = '<i class="bi bi-exclamation-triangle-fill"></i>';
            el.querySelector('.dash-header-icon').appendChild(errEl);
        } else if (!d.error && errEl) {
            errEl.remove();
        }
        if (errEl) errEl.title = d.error || '';
        el.classList.toggle('has-error', !!d.error);
        if (w.link) el.setAttribute('aria-label', d.error || text);
        el.title = d.error || text;
    }

    // A bar whose chips are all hidden takes no room
    function syncHeaderBars() {
        var bars = contentEl.querySelectorAll('.dash-header-bar');
        for (var i = 0; i < bars.length; i++) {
            bars[i].hidden = !bars[i].querySelector('.dash-header-item:not([hidden])');
        }
    }

    // A chip: the icon in a small circle coloured by state, the value (status) or the name
    function renderHeaderWidget(w) {
        if (w.type === 'chip') return renderChip(w);
        var el = document.createElement('div');
        el.className = 'dash-header-item';
        el.dataset.widgetId = w.id;
        el.dataset.type = w.type;

        var color = resolveColor(w.color);
        if (color) el.style.setProperty('--widget-color', color);

        var icon = calEl('span', 'dash-header-icon');
        icon.innerHTML = '<i class="bi bi-' + esc(w.icon || typeIcons[w.type] || 'circle') + '"></i>';
        el.appendChild(icon);

        if (w.type === 'status') {
            el.appendChild(calEl('span', 'dash-header-value', w.value != null ? fmtNum(w.value) : '--'));
            if (w.unit) el.appendChild(calEl('span', 'dash-header-unit', w.unit));
        } else {
            el.appendChild(calEl('span', 'dash-header-value', w.label));
        }

        var tap = null;
        if (w.type === 'link') {
            tap = function() { window.open(w.url, '_blank'); };
        } else if (w.type === 'toggle') {
            tap = function() {
                var on = !el._data.value;
                refreshTile(el, w, { value: on });
                executeAction(w.id, 'toggle', on);
            };
        } else if (w.type === 'button') {
            tap = function() {
                if (w.confirm && !confirm(w.confirm)) return;
                executeAction(w.id, 'tap');
            };
        }
        if (tap) {
            bindHeaderTap(el, w.type === 'toggle' ? 'switch' : (w.type === 'link' ? 'link' : 'button'), w.label || '', tap);
        }
        if (w.disabled) {
            el.classList.add('disabled');
            el.setAttribute('aria-disabled', 'true');
        }

        refreshTile(el, w, w);
        widgetEls[w.id] = el;
        return el;
    }

    // ---- Update widgets from poll ----

    // Widgets read with getValue: no value in a poll means the function gave nil
    var valueTypes = { toggle: true, slider: true, status: true, select: true };

    function updateWidget(wid, data, force) {
        var el = widgetEls[wid];
        if (!el) return;

        // Skip stale poll data if widget was recently acted upon
        if (!force && actionGrace[wid] && Date.now() < actionGrace[wid]) return;

        // Find widget definition
        var wDef = {};
        if (definition) {
            for (var i = 0; i < definition.widgets.length; i++) {
                if (definition.widgets[i].id === wid) {
                    wDef = definition.widgets[i];
                    break;
                }
            }
        }

        if (el.dataset.type === 'chip') return updateChip(el, wDef, data);

        // Disabled state
        if (data.disabled !== undefined) {
            el.classList.toggle('disabled', !!data.disabled);
            if (data.disabled) el.setAttribute('aria-disabled', 'true');
            else el.removeAttribute('aria-disabled');
        }

        // Colour from a function (false: none)
        if (data.color !== undefined) applyColor(el, data.color);

        // Error state: a badge on the icon
        var errEl = el.querySelector('.dash-widget-error');
        if (data.error) {
            el.classList.add('has-error');
            if (!errEl) {
                errEl = document.createElement('span');
                errEl.className = 'dash-widget-error';
                errEl.innerHTML = '<i class="bi bi-exclamation-triangle-fill"></i>';
                (el.querySelector('.dash-widget-icon, .dash-header-icon') || el).appendChild(errEl);
            }
            errEl.title = data.error;
        } else if (errEl) {
            errEl.remove();
            el.classList.remove('has-error');
        }

        var type = el.dataset.type;
        if (!type) return;
        var d = {};
        for (var k in data) if (data.hasOwnProperty(k)) d[k] = data[k];
        if (!force && valueTypes[type] && d.value === undefined) d.value = null;
        if ((type === 'cover' || type === 'covers') && d.value !== undefined) d.position = d.value; // a set's reply
        if (type === 'player') {
            if (d.value !== undefined) { d.volume = d.value; delete d.value; } // a volume reply
            // a poll is the whole state: what it leaves out is gone (a track without an album)
            if (!force) playerKeys.forEach(function(k) { if (!(k in d)) d[k] = null; });
        }
        if (type === 'color') delete d.value; // brightness or white: the reply does not say
        var slider = el.querySelector('.dash-slider-input');
        var held = slider && userHolds(slider);
        var toggle;

        if (type === 'cover') {
            if (slider && !held && d.position != null) slider.value = d.position;
            var btns = el.querySelectorAll('.dash-cover-btn');
            if (btns.length === 3 && d.state != null) {
                btns[0].classList.toggle('active', d.state === 1);
                btns[1].classList.toggle('active', d.state === 0);
                btns[2].classList.toggle('active', d.state === 2);
            }
            if (held) { delete d.position; delete d.state; }
        } else if (type === 'covers') {
            updateCovers(el, d);
        } else if (type === 'dimmer') {
            if (slider && !held && d.value != null) slider.value = d.value;
            toggle = el.querySelector('.dash-dimmer-toggle .form-check-input');
            if (toggle && d.on !== undefined) toggle.checked = !!d.on;
            if (slider && d.on !== undefined) slider.classList.toggle('dimmed', !d.on);
            if (held) { delete d.value; delete d.on; }
        } else if (type === 'color') {
            ['brightness', 'white'].forEach(function(role) {
                if (d[role] == null) return;
                var sl = el.querySelector('.dash-slider-input[data-role="' + role + '"]');
                var vs = el.querySelector('.dash-slider-value[data-role="' + role + '"]');
                if (sl && userHolds(sl)) { delete d[role]; return; }
                if (sl) sl.value = d[role];
                if (vs) vs.textContent = d[role] + '%';
            });
            toggle = el.querySelector('.dash-color-toggle .form-check-input');
            if (toggle && d.on !== undefined) toggle.checked = !!d.on;
            if (d.rgb) {
                markPreset(el, d.rgb);
                var pk = el.querySelector('.dash-color-custom input');
                if (pk && document.activeElement !== pk) pk.value = toHex(d.rgb);
            }
            // an action's reply carries only its value: keep the tint until a poll
            if (d.rgb !== undefined || d.on !== undefined) {
                applyLightColor(el, d.rgb, d.on !== false);
            }
        } else if (type === 'toggle') {
            toggle = el.querySelector('.form-check-input');
            if (toggle && d.value !== undefined) toggle.checked = !!d.value;
            el.classList.toggle('active', !!d.value);
        } else if (type === 'slider') {
            if (slider && !held && d.value != null) slider.value = d.value;
            if (held) delete d.value;
        } else if (type === 'select') {
            var sel = el.querySelector('.dash-select-input');
            if (sel && document.activeElement !== sel && d.value != null) sel.value = String(d.value);
        }

        // Header status: the value is the chip's text
        if (el.classList.contains('dash-header-item') && type === 'status' && d.value !== undefined) {
            var hVal = el.querySelector('.dash-header-value');
            if (hVal) hVal.textContent = d.value != null ? fmtNum(d.value) : '--';
        }

        refreshTile(el, wDef, d);
    }

    // ---- Polling ----

    function pollValues() {
        fetchJSON('/api/dashboard/' + dashId + '/values')
            .then(function(data) {
                if (data._notFound) {
                    showOverlay();
                    return;
                }
                if (data.error) {
                    showOverlay();
                    return;
                }
                hideOverlay();
                for (var wid in data) {
                    if (!data.hasOwnProperty(wid)) continue;
                    var sec = /^_section_(\d+)$/.exec(wid);
                    if (sec) updateSectionCovers(+sec[1], data[wid]);
                    else updateWidget(wid, data[wid]);
                }
                // a chip left out of the poll is hidden
                for (var cid in widgetEls) {
                    if (widgetEls[cid].dataset.type === 'chip' && !data.hasOwnProperty(cid)) {
                        updateChip(widgetEls[cid], {}, null);
                    }
                }
                syncHeaderBars();
            })
            .catch(function() {
                showOverlay();
            });
    }

    function startPolling() {
        stopPolling();
        pollTimer = setInterval(pollValues, pollInterval);
    }

    function stopPolling() {
        if (pollTimer) {
            clearInterval(pollTimer);
            pollTimer = null;
        }
    }

    // ---- Actions ----

    function executeAction(widgetId, action, value) {
        var body = { widgetId: widgetId, action: action };
        if (value !== undefined) body.value = value;

        // Protect this widget from stale poll data for 3 seconds
        actionGrace[widgetId] = Date.now() + 3000;

        fetchJSON('/api/dashboard/' + dashId + '/action', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        }).then(function(result) {
            if (result && result.error) {
                revert();
                return;
            }
            if (result && result.value !== undefined) {
                updateWidget(widgetId, result, true);
            }
            if (result && result.message) {
                VCLU.showToast(result.message, 'success');
            }
        }).catch(revert);

        function revert() {
            delete actionGrace[widgetId];
            pollValues();
        }
    }

    // ---- Overlay ----

    function showOverlay() {
        if (overlayEl) overlayEl.style.display = '';
    }

    function hideOverlay() {
        if (overlayEl) overlayEl.style.display = 'none';
    }

    // ---- Helpers ----

    var esc = VCLU.esc;

    // ---- Init ----

    setupFullscreen();
    fetchJSON('/api/dashboard/' + dashId)
        .then(function(def) {
            if (def._notFound || def.error) {
                contentEl.className = '';
                contentEl.innerHTML = '<div class="text-center py-5"><p class="text-muted">Dashboard not found.</p></div>';
                return;
            }
            renderDashboard(def);
            startPolling();
        })
        .catch(function(err) {
            contentEl.className = '';
            contentEl.innerHTML = '<div class="text-center py-5"><p class="text-danger">Error loading dashboard.</p></div>';
        });

    // Pause polling when page is hidden, resume when visible
    document.addEventListener('visibilitychange', function() {
        if (document.hidden) {
            stopPolling();
            stopAllChartTimers();
        } else {
            pollValues();
            startPolling();
            // Restart chart timers by re-fetching and scheduling
            if (definition) {
                for (var i = 0; i < definition.widgets.length; i++) {
                    var w = definition.widgets[i];
                    if (w.type === 'calendar' && calendars[w.id]) {
                        calendars[w.id].refresh();
                        startChartTimer('cal-' + w.id, calendars[w.id].refresh, 60000);
                    }
                    if (w.type === 'chart') {
                        var canvas = document.getElementById('chart-' + w.id);
                        if (canvas) {
                            fetchChartData(w, canvas);
                            startChartTimer(w.id, (function(ww) {
                                return function() {
                                    var c = document.getElementById('chart-' + ww.id);
                                    if (c) fetchChartData(ww, c);
                                };
                            })(w), chartRefreshMs(w));
                        }
                    }
                    if (w.spark) {
                        var sparkCanvas = document.getElementById('spark-' + w.id);
                        if (sparkCanvas) {
                            fetchSparkline(w, sparkCanvas);
                            startChartTimer('spark-' + w.id, (function(ww) {
                                return function() {
                                    var c = document.getElementById('spark-' + ww.id);
                                    if (c) fetchSparkline(ww, c);
                                };
                            })(w), chartRefreshMs({range: w.sparkRange || '2h'}));
                        }
                    }
                }
            }
        }
    });
})();
