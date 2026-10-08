/**
 * VCLU shared frontend helpers.
 * Loaded before csrf.js on every page.
 */
window.VCLU = (function() {
    'use strict';

    // ---- escapeHtml ----

    var _escDiv = document.createElement('div');

    function esc(str) {
        if (str === null || str === undefined) return '';
        _escDiv.textContent = String(str);
        return _escDiv.innerHTML;
    }

    // ---- fetchJSON ----

    function fetchJSON(url, opts) {
        return fetch(url, opts).then(function(res) {
            if (!res.ok) {
                return res.json().catch(function() { return {}; }).then(function(body) {
                    var err = new Error(body.error || 'HTTP ' + res.status);
                    err.status = res.status;
                    err.body = body;
                    throw err;
                });
            }
            return res.json();
        });
    }

    // ---- showToast ----

    var _toastContainer = null;

    function _ensureContainer() {
        if (_toastContainer) return _toastContainer;
        _toastContainer = document.createElement('div');
        _toastContainer.className = 'toast-container position-fixed bottom-0 end-0 p-3';
        _toastContainer.style.zIndex = '1090';
        document.body.appendChild(_toastContainer);
        return _toastContainer;
    }

    function showToast(msg, type) {
        type = type || 'info';
        var container = _ensureContainer();

        var icons = {
            success: 'bi-check-circle-fill',
            danger:  'bi-exclamation-triangle-fill',
            warning: 'bi-exclamation-triangle-fill',
            info:    'bi-info-circle-fill'
        };
        var icon = icons[type] || icons.info;

        var toastEl = document.createElement('div');
        toastEl.className = 'toast align-items-center text-bg-' + type + ' border-0';
        toastEl.setAttribute('role', 'alert');
        toastEl.innerHTML =
            '<div class="d-flex">' +
                '<div class="toast-body">' +
                    '<i class="bi ' + icon + ' me-1"></i> ' + esc(msg) +
                '</div>' +
                '<button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast"></button>' +
            '</div>';

        container.appendChild(toastEl);

        var toast = new bootstrap.Toast(toastEl, { delay: 5000 });
        toastEl.addEventListener('hidden.bs.toast', function() {
            toastEl.remove();
        });
        toast.show();
    }

    // ---- Theme helpers ----

    function isDark() {
        return document.documentElement.getAttribute('data-bs-theme') === 'dark';
    }

    function onThemeChange(cb) {
        var observer = new MutationObserver(function(mutations) {
            for (var i = 0; i < mutations.length; i++) {
                if (mutations[i].attributeName === 'data-bs-theme') {
                    cb(isDark());
                    break;
                }
            }
        });
        observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-bs-theme'] });
        return function() { observer.disconnect(); };
    }

    // ---- Public API ----

    return {
        esc: esc,
        fetchJSON: fetchJSON,
        showToast: showToast,
        isDark: isDark,
        onThemeChange: onThemeChange
    };
})();
