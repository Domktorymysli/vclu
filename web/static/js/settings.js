// vclu Settings - Object-oriented JavaScript

// Translation helper — falls back to English default
function _st(key, fallback) {
    return (window.SETTINGS_T && window.SETTINGS_T[key]) || fallback || key;
}

/**
 * Security Settings handler
 */
function SecuritySettings() {
    this.elements = {};
}

/**
 * Initialize security settings
 */
SecuritySettings.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        authEnabled: document.getElementById('authEnabled'),
        authFields: document.getElementById('authFields'),
        authUsername: document.getElementById('authUsername'),
        authPassword: document.getElementById('authPassword'),
        authPasswordConfirm: document.getElementById('authPasswordConfirm'),
        securityForm: document.getElementById('securityForm'),
        resetBtn: document.getElementById('resetBtn'),
        restartBtn: document.getElementById('restartBtn')
    };

    // Bind auth toggle
    if (this.elements.authEnabled) {
        this.elements.authEnabled.addEventListener('change', function() {
            self.toggleAuthFields(this.checked);
        });
    }

    // Bind security form validation
    if (this.elements.securityForm) {
        this.elements.securityForm.addEventListener('submit', function(e) {
            if (!self.validateSecurityForm()) {
                e.preventDefault();
                return false;
            }
        });
    }

    // Bind reset button
    if (this.elements.resetBtn) {
        this.elements.resetBtn.addEventListener('click', function() {
            self.resetCLU();
        });
    }

    // Bind restart button
    if (this.elements.restartBtn) {
        this.elements.restartBtn.addEventListener('click', function() {
            self.restartCLU();
        });
    }
};

/**
 * Toggle auth fields visibility
 * @param {boolean} show - Whether to show auth fields
 */
SecuritySettings.prototype.toggleAuthFields = function(show) {
    if (this.elements.authFields) {
        this.elements.authFields.style.display = show ? 'block' : 'none';
    }
};

/**
 * Validate security form
 * @returns {boolean} Whether form is valid
 */
SecuritySettings.prototype.validateSecurityForm = function() {
    var authEnabled = this.elements.authEnabled && this.elements.authEnabled.checked;

    if (authEnabled) {
        var password = this.elements.authPassword ? this.elements.authPassword.value : '';
        var passwordConfirm = this.elements.authPasswordConfirm ? this.elements.authPasswordConfirm.value : '';
        var username = this.elements.authUsername ? this.elements.authUsername.value : '';

        if (username.length < 3) {
            VCLU.showToast(_st('username_min', 'Username must be at least 3 characters!'), 'warning');
            return false;
        }

        if (password !== '' && password !== passwordConfirm) {
            VCLU.showToast(_st('password_mismatch', 'Passwords do not match!'), 'warning');
            return false;
        }

        if (password !== '' && password.length < 8) {
            VCLU.showToast(_st('password_min', 'Password must be at least 8 characters!'), 'warning');
            return false;
        }
    }

    return true;
};

/**
 * Restart CLU (no data loss)
 */
SecuritySettings.prototype.restartCLU = function() {
    if (!confirm(_st('confirm_restart', 'Restart vclu?\n\nNo data will be lost.'))) {
        return;
    }

    var btn = this.elements.restartBtn;
    btn.disabled = true;
    btn.innerHTML = '<i class="bi bi-hourglass-split"></i> ' + _st('restarting', 'Restarting...');

    fetch('/api/restart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
    }).catch(function() {});

    RestartOverlay.show({
        title: _st('restarting_title', 'Restarting CLU...'),
        subtitle: _st('please_wait', 'Please wait'),
        redirectUrl: '/settings'
    });
};

/**
 * Reset CLU to factory defaults
 */
SecuritySettings.prototype.resetCLU = function() {
    if (!confirm(_st('confirm_reset', 'Are you sure you want to reset the CLU?\n\nThis will delete ALL configuration files and cannot be undone.'))) {
        return;
    }

    if (!confirm(_st('confirm_reset_final', 'FINAL WARNING: All Lua scripts, keys, and settings will be permanently deleted.\n\nContinue?'))) {
        return;
    }

    var resetBtnLabel = '<i class="bi bi-trash"></i> ' + _st('reset_btn', 'Reset CLU to Factory Defaults');
    var btn = this.elements.resetBtn;
    btn.disabled = true;
    btn.innerHTML = '<i class="bi bi-hourglass-split"></i> ' + _st('resetting', 'Resetting...');

    fetch('/api/reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast('Error: ' + data.error, 'danger');
            btn.disabled = false;
            btn.innerHTML = resetBtnLabel;
            return;
        }
        RestartOverlay.show({
            title: _st('resetting_title', 'Resetting CLU...'),
            subtitle: _st('reset_subtitle', 'All data cleared. Restarting...'),
            redirectUrl: data.redirect || '/wizard'
        });
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
        btn.disabled = false;
        btn.innerHTML = resetBtnLabel;
    });
};

// Global instances
var securitySettings;

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
    securitySettings = new SecuritySettings();
    securitySettings.init();

    // API Key buttons
    var generateBtn = document.getElementById('generateAPIKeyBtn');
    if (generateBtn) {
        generateBtn.addEventListener('click', function() {
            if (!confirm(_st('confirm_generate', 'Generate a new API key? Any existing key will be replaced.'))) return;
            generateBtn.disabled = true;
            fetch('/api/settings/generate-api-key', { method: 'POST' })
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    generateBtn.disabled = false;
                    if (data.error) { VCLU.showToast('Error: ' + data.error, 'danger'); return; }
                    document.getElementById('apiKeyValue').textContent = data.apiKey;
                    document.getElementById('apiKeyResult').style.display = 'block';
                })
                .catch(function(err) {
                    generateBtn.disabled = false;
                    VCLU.showToast('Error: ' + err.message, 'danger');
                });
        });
    }

    // New-version check: saved at once, no restart
    var updateCheck = document.getElementById('updateCheckEnabled');
    if (updateCheck) {
        updateCheck.addEventListener('change', function() {
            var on = updateCheck.checked;
            updateCheck.disabled = true;
            fetch('/api/update/settings', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ enabled: on })
            })
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    updateCheck.disabled = false;
                    if (data.error) { updateCheck.checked = !on; VCLU.showToast('Error: ' + data.error, 'danger'); return; }
                    VCLU.showToast(_st('update_check_saved', 'Saved'), 'success');
                    document.dispatchEvent(new CustomEvent('vclu:update-check', { detail: { enabled: data.enabled } }));
                })
                .catch(function(err) {
                    updateCheck.disabled = false;
                    updateCheck.checked = !on;
                    VCLU.showToast('Error: ' + err.message, 'danger');
                });
        });
    }

    initNotificationSettings();

    var revokeBtn = document.getElementById('revokeAPIKeyBtn');
    if (revokeBtn) {
        revokeBtn.addEventListener('click', function() {
            if (!confirm(_st('confirm_revoke', 'Revoke the current API key? Any integrations using it will stop working.'))) return;
            revokeBtn.disabled = true;
            fetch('/api/settings/api-key', { method: 'DELETE' })
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    if (data.error) { VCLU.showToast('Error: ' + data.error, 'danger'); revokeBtn.disabled = false; return; }
                    location.reload();
                })
                .catch(function(err) {
                    revokeBtn.disabled = false;
                    VCLU.showToast('Error: ' + err.message, 'danger');
                });
        });
    }
});

/**
 * Notifications card: a switch per source from /api/notifications/settings, and the muted
 * keys (#248). Texts are the bell's (notifyI18n in _sidebar.html).
 */
function initNotificationSettings() {
    var box = document.getElementById('notifySources');
    var muted = document.getElementById('notifyMuted');
    if (!box || !muted) return;
    var t = {};
    try {
        t = JSON.parse(document.getElementById('notifyI18n').textContent) || {};
    } catch (err) { /* English fallbacks */ }
    function tr(key, fallback) {
        return Object.prototype.hasOwnProperty.call(t, 'notify.' + key) && t['notify.' + key] ? t['notify.' + key] : fallback;
    }

    // the entry's message in the panel's language; the English title without one
    function mutedText(m) {
        var text = m.msg && t[m.msg];
        if (!text) return m.title || m.key;
        var p = m.params || {};
        return text.replace(/\{(\w+)\}/g, function(all, k) {
            if (!(k in p)) return all;
            return k === 'source' ? tr('source.' + p[k], p[k]) : p[k];
        });
    }

    function showMuted(list) {
        muted.textContent = '';
        if (!list || !list.length) {
            var none = document.createElement('li');
            none.className = 'text-muted small';
            none.textContent = tr('muted_none', 'Nothing is muted.');
            muted.appendChild(none);
            return;
        }
        list.forEach(function(m) {
            var li = document.createElement('li');
            li.className = 'd-flex align-items-center gap-2 mb-1';
            var name = document.createElement('span');
            name.className = 'text-truncate';
            name.textContent = mutedText(m);
            var until = document.createElement('small');
            until.className = 'text-muted text-nowrap';
            until.textContent = m.until === 'forever' ? tr('muted_forever', 'for good')
                : tr('muted_until', 'until {time}').replace('{time}', new Date(m.until).toLocaleString(document.documentElement.lang || undefined));
            var restore = document.createElement('button');
            restore.type = 'button';
            restore.className = 'btn btn-sm btn-outline-secondary ms-auto';
            restore.textContent = tr('restore', 'Restore');
            restore.addEventListener('click', function() {
                restore.disabled = true;
                VCLU.fetchJSON('/api/notifications/mute', {
                    method: 'DELETE',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ key: m.key })
                }).then(load, function(err) {
                    restore.disabled = false;
                    VCLU.showToast('Error: ' + err.message, 'danger');
                });
            });
            li.appendChild(name);
            li.appendChild(until);
            li.appendChild(restore);
            muted.appendChild(li);
        });
    }

    // one switch per source, in the server's order; only the one changed is sent
    function showSources(sources) {
        box.textContent = '';
        (sources || []).forEach(function(src) {
            var wrap = document.createElement('div');
            wrap.className = 'form-check form-switch';
            var sw = document.createElement('input');
            sw.className = 'form-check-input';
            sw.type = 'checkbox';
            sw.setAttribute('role', 'switch');
            sw.id = 'notifySource-' + src.id;
            sw.dataset.source = src.id;
            sw.checked = !!src.enabled;
            var label = document.createElement('label');
            label.className = 'form-check-label';
            label.htmlFor = sw.id;
            label.textContent = tr('source.' + src.id, src.id);
            wrap.appendChild(sw);
            wrap.appendChild(label);
            var hint = tr('source.' + src.id + '_hint', '');
            if (hint) {
                var h = document.createElement('div');
                h.className = 'form-text mt-0';
                h.textContent = hint;
                wrap.appendChild(h);
            }
            sw.addEventListener('change', function() {
                var body = { sources: {} };
                body.sources[src.id] = sw.checked;
                VCLU.fetchJSON('/api/notifications/settings', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(body)
                }).then(function() {
                    VCLU.showToast(tr('saved', 'Saved'), 'success');
                    document.dispatchEvent(new CustomEvent('vclu:notifications-changed'));
                }, function(err) {
                    sw.checked = !sw.checked;
                    VCLU.showToast('Error: ' + err.message, 'danger');
                });
            });
            box.appendChild(wrap);
        });
    }

    function load() {
        return VCLU.fetchJSON('/api/notifications/settings').then(function(data) {
            showSources(data.sources);
            showMuted(data.muted);
        }).catch(function() { /* the card stays empty */ });
    }

    // a key muted from the bell
    document.addEventListener('vclu:notifications-changed', function(ev) {
        if (ev.detail && ev.detail.from === 'bell') load();
    });
    load();
}

// Legacy function for backwards compatibility
function resetCLU() {
    if (window.securitySettings) {
        window.securitySettings.resetCLU();
    }
}
