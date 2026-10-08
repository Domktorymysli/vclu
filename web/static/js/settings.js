// Virtual CLU Settings - Object-oriented JavaScript

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
    if (!confirm(_st('confirm_restart', 'Restart Virtual CLU?\n\nNo data will be lost.'))) {
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

// Legacy function for backwards compatibility
function resetCLU() {
    if (window.securitySettings) {
        window.securitySettings.resetCLU();
    }
}
