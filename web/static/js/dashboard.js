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
    var actionGrace = {}; // widgetId -> timestamp (ignore polls until this time)
    var chartInstances = {}; // widgetId -> Chart.js instance
    var chartTimers = {};   // widgetId -> per-chart refresh timer

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

    // Apply a polled colour; a falsy one drops it (header: icon only, as when rendered)
    function applyColor(el, c) {
        var color = resolveColor(c);
        if (el.classList.contains('dash-header-item')) {
            var icon = el.querySelector(':scope > i');
            if (icon) icon.style.color = color || '';
        } else if (color) {
            el.style.setProperty('--widget-color', color);
        } else {
            el.style.removeProperty('--widget-color');
        }
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

    function renderDashboard(def) {
        definition = def;
        pollInterval = (def.poll || 2) * 1000;
        stopAllChartTimers();
        destroyAllCharts();
        contentEl.className = '';
        contentEl.innerHTML = '';
        contentEl.style.cssText = '--dash-columns:' + (def.columns || 3);
        widgetEls = {};

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
                continue;
            }

            var secEl = document.createElement('div');
            secEl.className = 'dash-section';

            // Section header
            var hdr = document.createElement('div');
            hdr.className = 'dash-section-header';
            if (sec.icon) hdr.innerHTML = '<i class="bi bi-' + esc(sec.icon) + '"></i>';
            hdr.innerHTML += esc(sec.name);
            secEl.appendChild(hdr);

            // Grid
            var grid = document.createElement('div');
            grid.className = 'dash-grid';
            grid.style.cssText = '--dash-columns:' + (sec.columns || def.columns || 3);

            for (var wi = 0; wi < widgets.length; wi++) {
                var wEl = renderWidget(widgets[wi]);
                grid.appendChild(wEl);
            }

            secEl.appendChild(grid);
            contentEl.appendChild(secEl);
        }
    }

    function renderWidget(w) {
        var el = document.createElement('div');
        el.className = 'dash-widget dash-widget-' + w.type;
        el.dataset.widgetId = w.id;

        // Span (stored as data-attr; CSS responsive rules can override)
        if (w.span && w.span > 1) {
            el.style.gridColumn = 'span ' + w.span;
            el.dataset.span = w.span;
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

        // Error indicator
        if (w.error) {
            el.classList.add('has-error');
            var errIcon = document.createElement('span');
            errIcon.className = 'dash-widget-error';
            errIcon.title = w.error;
            errIcon.innerHTML = '<i class="bi bi-exclamation-triangle"></i>';
            el.appendChild(errIcon);
        }

        // Icon
        if (w.icon) {
            var iconEl = document.createElement('div');
            iconEl.className = 'dash-widget-icon';
            iconEl.innerHTML = '<i class="bi bi-' + esc(w.icon) + '"></i>';
            el.appendChild(iconEl);
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
        } else if (w.type === 'dimmer') {
            renderDimmer(el, w);
        } else if (w.type === 'color') {
            renderColor(el, w);
        } else if (w.type === 'image') {
            renderImage(el, w);
            return el; // image has its own label placement
        } else if (w.type === 'group') {
            return renderGroup(w);
        }

        // Label
        var lbl = document.createElement('div');
        lbl.className = 'dash-widget-label';
        lbl.textContent = w.label;
        el.appendChild(lbl);

        widgetEls[w.id] = el;
        return el;
    }

    function renderButton(el, w) {
        el.addEventListener('click', function() {
            if (w.confirm && !confirm(w.confirm)) return;
            el.classList.add('flash');
            setTimeout(function() { el.classList.remove('flash'); }, 300);
            executeAction(w.id, 'tap');
        });
    }

    function renderToggle(el, w) {
        // Set active state based on value
        if (w.value) el.classList.add('active');

        var sw = document.createElement('div');
        sw.className = 'dash-toggle-switch form-check form-switch';
        var input = document.createElement('input');
        input.type = 'checkbox';
        input.className = 'form-check-input';
        input.checked = !!w.value;
        input.dataset.widgetId = w.id;
        input.addEventListener('change', function() {
            executeAction(w.id, 'toggle', input.checked);
        });
        sw.appendChild(input);

        // Insert before label (after icon)
        el.appendChild(sw);
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

        var valEl = document.createElement('span');
        valEl.className = 'dash-slider-value';
        valEl.textContent = input.value;

        var debounceTimer = null;
        input.addEventListener('input', function() {
            valEl.textContent = input.value;
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(function() {
                executeAction(w.id, 'set', parseFloat(input.value));
            }, 250);
        });

        row.appendChild(input);
        row.appendChild(valEl);
        el.appendChild(row);
    }

    function renderStatus(el, w) {
        var valEl = document.createElement('div');
        valEl.className = 'dash-status-value';
        var display = w.value != null ? w.value : '--';
        valEl.innerHTML = esc(String(display));
        if (w.unit) {
            valEl.innerHTML += ' <span class="dash-status-unit">' + esc(w.unit) + '</span>';
        }
        el.appendChild(valEl);

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
        el.appendChild(ext);
        el.addEventListener('click', function() {
            window.open(w.url, '_blank');
        });
    }

    function renderSelect(el, w) {
        el.style.cursor = 'default';
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
        sel.addEventListener('change', function() {
            executeAction(w.id, 'select', sel.value);
        });
        wrap.appendChild(sel);
        el.appendChild(wrap);
    }

    function renderImage(el, w) {
        el.classList.add('dash-widget-image');
        el.style.cursor = 'default';

        // Image first
        var img = document.createElement('img');
        img.className = 'dash-image-img';
        img.src = w.url || '';
        img.alt = w.label || '';
        if (w.height) img.style.height = w.height + 'px';
        img.onerror = function() { img.style.opacity = '0.2'; };
        el.appendChild(img);

        // Caption below image
        var lbl = document.createElement('div');
        lbl.className = 'dash-image-label';
        if (w.align) lbl.classList.add('align-' + w.align);
        lbl.textContent = w.label;
        el.appendChild(lbl);

        widgetEls[w.id] = el;

        // Auto-refresh image
        if (w.refresh) {
            startChartTimer('img-' + w.id, function() {
                var sep = w.url.indexOf('?') >= 0 ? '&' : '?';
                img.src = w.url + sep + '_t=' + Date.now();
            }, w.refresh * 1000);
        }
        return el;
    }

    function renderGroup(w) {
        var el = document.createElement('div');
        el.className = 'dash-group-label';
        if (w.icon) {
            el.innerHTML = '<i class="bi bi-' + esc(w.icon) + '"></i> ';
        }
        el.innerHTML += esc(w.label);
        return el;
    }

    // ---- Cover widget ----

    function renderCover(el, w) {
        el.style.cursor = 'default';

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

            var valEl = document.createElement('span');
            valEl.className = 'dash-slider-value';
            valEl.textContent = w.position != null ? input.value + '%' : '--';

            var debounceTimer = null;
            input.addEventListener('input', function() {
                valEl.textContent = input.value + '%';
                clearTimeout(debounceTimer);
                debounceTimer = setTimeout(function() {
                    executeAction(w.id, 'set', parseFloat(input.value));
                }, 500);
            });

            row.appendChild(input);
            row.appendChild(valEl);
            el.appendChild(row);
        }
    }

    // ---- Dimmer widget ----

    function renderDimmer(el, w) {
        el.style.cursor = 'default';

        var caps = w.capabilities || {};
        var isOn = w.on !== false;

        if (caps.toggle) {
            var sw = document.createElement('div');
            sw.className = 'dash-dimmer-toggle form-check form-switch';
            var input = document.createElement('input');
            input.type = 'checkbox';
            input.className = 'form-check-input';
            input.checked = isOn;
            input.addEventListener('change', function(e) {
                e.stopPropagation();
                executeAction(w.id, input.checked ? 'switchOn' : 'switchOff');
            });
            sw.appendChild(input);
            el.appendChild(sw);
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

        var valEl = document.createElement('span');
        valEl.className = 'dash-slider-value';
        valEl.textContent = (w.value != null ? w.value : 0) + '%';

        var debounceTimer = null;
        slider.addEventListener('input', function() {
            valEl.textContent = slider.value + '%';
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(function() {
                executeAction(w.id, 'set', parseFloat(slider.value));
            }, 500);
        });

        row.appendChild(slider);
        row.appendChild(valEl);
        el.appendChild(row);

        var iconEl = el.querySelector('.dash-widget-icon');
        if (iconEl) {
            var brightness = w.value != null ? w.value : 0;
            iconEl.style.opacity = isOn ? (0.3 + 0.7 * brightness / 100) : 0.3;
        }
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

        var debounceTimer = null;
        slider.addEventListener('input', function() {
            valEl.textContent = slider.value + '%';
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(function() {
                executeAction(w.id, action, parseFloat(slider.value));
            }, 500);
        });

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
            var sw = document.createElement('div');
            sw.className = 'dash-color-toggle form-check form-switch';
            var input = document.createElement('input');
            input.type = 'checkbox';
            input.className = 'form-check-input';
            input.checked = isOn;
            input.addEventListener('change', function(e) {
                e.stopPropagation();
                executeAction(w.id, input.checked ? 'switchOn' : 'switchOff');
                if (!input.checked) applyLightColor(el, null, false);
            });
            sw.appendChild(input);
            el.appendChild(sw);
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
        el.insertBefore(wrap, el.querySelector('.dash-widget-label'));

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

                // Smooth to reduce sensor jitter
                var values = smoothValues(rawValues, 3);

                var color = resolveColor(w.color) || 'rgba(39,194,168,1)';
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
                                    ticks: { color: 'rgba(255,255,255,0.4)', maxTicksLimit: 6, font: {size: 10} },
                                    grid: { color: 'rgba(255,255,255,0.05)' },
                                },
                                y: {
                                    min: yBounds.min,
                                    max: yBounds.max,
                                    ticks: {
                                        color: 'rgba(255,255,255,0.4)', font: {size: 10},
                                        callback: function(v) { return v + (w.unit ? ' ' + w.unit : ''); }
                                    },
                                    grid: { color: 'rgba(255,255,255,0.05)' },
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
                var color = resolveColor(w.color) || 'rgba(39,194,168,1)';
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

    function renderHeaderWidget(w) {
        var el = document.createElement('div');
        el.className = 'dash-header-item';
        el.dataset.widgetId = w.id;

        var color = resolveColor(w.color);

        if (w.icon) {
            var icon = document.createElement('i');
            icon.className = 'bi bi-' + w.icon;
            if (color) icon.style.color = color;
            el.appendChild(icon);
        }

        if (w.type === 'status') {
            var val = document.createElement('span');
            val.className = 'dash-header-value';
            val.textContent = w.value != null ? w.value : '--';
            el.appendChild(val);
            if (w.unit) {
                var unit = document.createElement('span');
                unit.className = 'dash-header-unit';
                unit.textContent = w.unit;
                el.appendChild(unit);
            }
        } else if (w.type === 'link') {
            var lbl = document.createElement('span');
            lbl.className = 'dash-header-value';
            lbl.textContent = w.label;
            el.appendChild(lbl);
            el.style.cursor = 'pointer';
            el.addEventListener('click', function() {
                window.open(w.url, '_blank');
            });
        } else {
            var lbl = document.createElement('span');
            lbl.className = 'dash-header-value';
            lbl.textContent = w.label;
            el.appendChild(lbl);
        }

        widgetEls[w.id] = el;
        return el;
    }

    // ---- Update widgets from poll ----

    function updateWidget(wid, data, force) {
        var el = widgetEls[wid];
        if (!el) return;

        // Skip stale poll data if widget was recently acted upon
        if (!force && actionGrace[wid] && Date.now() < actionGrace[wid]) return;

        // Find widget definition
        var wDef = null;
        if (definition) {
            for (var i = 0; i < definition.widgets.length; i++) {
                if (definition.widgets[i].id === wid) {
                    wDef = definition.widgets[i];
                    break;
                }
            }
        }

        // Disabled state
        if (data.disabled !== undefined) {
            el.classList.toggle('disabled', !!data.disabled);
        }

        // Colour from a function (false: none)
        if (data.color !== undefined) applyColor(el, data.color);

        // Error state
        var errEl = el.querySelector('.dash-widget-error');
        if (data.error) {
            el.classList.add('has-error');
            if (!errEl) {
                errEl = document.createElement('span');
                errEl.className = 'dash-widget-error';
                errEl.innerHTML = '<i class="bi bi-exclamation-triangle"></i>';
                el.insertBefore(errEl, el.firstChild);
            }
            errEl.title = data.error;
        } else if (errEl) {
            errEl.remove();
            el.classList.remove('has-error');
        }

        // Type-specific updates
        var type = el.className.match(/dash-widget-(\w+)/);
        if (!type) return;
        type = type[1];

        // Compound widgets (multiple data fields, not just value)
        if (type === 'cover') {
            var slider = el.querySelector('.dash-slider-input');
            var valSpan = el.querySelector('.dash-slider-value');
            if (slider && document.activeElement !== slider && data.position != null) {
                slider.value = data.position;
            }
            if (valSpan && data.position != null) valSpan.textContent = data.position + '%';
            var btns = el.querySelectorAll('.dash-cover-btn');
            if (btns.length === 3 && data.state != null) {
                btns[0].classList.toggle('active', data.state === 1);
                btns[1].classList.toggle('active', data.state === 0);
                btns[2].classList.toggle('active', data.state === 2);
            }
            return;
        } else if (type === 'dimmer') {
            var slider = el.querySelector('.dash-slider-input');
            var valSpan = el.querySelector('.dash-slider-value');
            if (slider && document.activeElement !== slider && data.value != null) {
                slider.value = data.value;
            }
            if (valSpan && data.value != null) valSpan.textContent = data.value + '%';
            var toggle = el.querySelector('.dash-dimmer-toggle .form-check-input');
            if (toggle && data.on !== undefined) toggle.checked = !!data.on;
            var isOn = data.on !== false;
            if (slider) slider.classList.toggle('dimmed', !isOn);
            var iconEl = el.querySelector('.dash-widget-icon');
            if (iconEl) {
                var br = data.value != null ? data.value : 0;
                iconEl.style.opacity = isOn ? (0.3 + 0.7 * br / 100) : 0.3;
            }
            return;
        } else if (type === 'color') {
            ['brightness', 'white'].forEach(function(role) {
                if (data[role] == null) return;
                var sl = el.querySelector('.dash-slider-input[data-role="' + role + '"]');
                var vs = el.querySelector('.dash-slider-value[data-role="' + role + '"]');
                if (sl && document.activeElement !== sl) sl.value = data[role];
                if (vs) vs.textContent = data[role] + '%';
            });
            var toggle = el.querySelector('.dash-color-toggle .form-check-input');
            if (toggle && data.on !== undefined) toggle.checked = !!data.on;
            if (data.rgb) {
                markPreset(el, data.rgb);
                var pk = el.querySelector('.dash-color-custom input');
                if (pk && document.activeElement !== pk) pk.value = toHex(data.rgb);
            }
            // an action's reply carries only its value: keep the tint until a poll
            if (data.rgb !== undefined || data.on !== undefined) {
                applyLightColor(el, data.rgb, data.on !== false);
            }
            return;
        }

        // Simple widgets (value-based)
        if (data.value === undefined) return;
        var value = data.value;

        if (type === 'toggle') {
            var input = el.querySelector('.form-check-input');
            if (input) input.checked = !!value;
            el.classList.toggle('active', !!value);
        } else if (type === 'slider') {
            var slider = el.querySelector('.dash-slider-input');
            var valSpan = el.querySelector('.dash-slider-value');
            if (slider && document.activeElement !== slider) {
                slider.value = value;
            }
            if (valSpan) valSpan.textContent = value;
        } else if (type === 'status') {
            var valEl = el.querySelector('.dash-status-value');
            if (valEl) {
                var display = value != null ? value : '--';
                var unit = wDef && wDef.unit ? wDef.unit : '';
                valEl.innerHTML = esc(String(display));
                if (unit) {
                    valEl.innerHTML += '<span class="dash-status-unit">' + esc(unit) + '</span>';
                }
            }
        } else if (type === 'select') {
            var sel = el.querySelector('.dash-select-input');
            if (sel && document.activeElement !== sel) {
                sel.value = String(value);
            }
        }

        // Header item update (status in header bar)
        if (el.classList.contains('dash-header-item')) {
            var hVal = el.querySelector('.dash-header-value');
            if (hVal) hVal.textContent = value != null ? value : '--';
        }
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
                    if (data.hasOwnProperty(wid)) {
                        updateWidget(wid, data[wid]);
                    }
                }
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
            if (result && result.value !== undefined) {
                updateWidget(widgetId, result, true);
            }
            if (result && result.message) {
                VCLU.showToast(result.message, 'success');
            }
        });
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
