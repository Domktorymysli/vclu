/**
 * HomeKitManager - HomeKit bridge configuration and status
 * Uses prototype pattern for modularity
 * @param {Object} options - Configuration options
 */
function HomeKitManager(options) {
    this.options = options || {};
    this.refreshInterval = this.options.refreshInterval || 10000;
    this.intervalId = null;

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize HomeKit manager
 */
HomeKitManager.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        enabledCheckbox: document.getElementById('homekitEnabled'),
        configFields: document.getElementById('configFields'),
        statusBadge: document.getElementById('statusBadge'),
        statAccessories: document.getElementById('statAccessories'),
        statPairing: document.getElementById('statPairing'),
        statPort: document.getElementById('statPort'),
        saveBtn: document.getElementById('saveHomekitBtn'),
        restartBtn: document.querySelector('[onclick="restartHomeKit()"]')
    };

    // Bind toggle event
    if (this.elements.enabledCheckbox) {
        this.elements.enabledCheckbox.addEventListener('change', function() {
            self.toggleConfigFields(this.checked);
        });
    }

    // Remove inline onclick and bind properly for restart
    if (this.elements.restartBtn) {
        this.elements.restartBtn.removeAttribute('onclick');
        this.elements.restartBtn.addEventListener('click', function(e) {
            self.restart(e.target);
        });
    }

    // Start auto-refresh
    this.startAutoRefresh();
};

/**
 * Toggle configuration fields visibility
 * @param {boolean} show - Whether to show fields
 */
HomeKitManager.prototype.toggleConfigFields = function(show) {
    if (this.elements.configFields) {
        this.elements.configFields.style.display = show ? 'block' : 'none';
    }
};

/**
 * Save HomeKit configuration via JSON API
 */
HomeKitManager.prototype.saveConfig = function() {
    var self = this;
    var btn = this.elements.saveBtn;
    if (btn) {
        btn.disabled = true;
        btn.innerHTML = '<i class="bi bi-hourglass-split"></i> Saving...';
    }

    var data = {
        enabled: document.getElementById('homekitEnabled') ? document.getElementById('homekitEnabled').checked : false,
        pin: document.getElementById('pin') ? document.getElementById('pin').value : '00102003',
        port: document.getElementById('port') ? parseInt(document.getElementById('port').value, 10) : 5123,
        bridgeName: document.getElementById('bridgeName') ? document.getElementById('bridgeName').value : '',
        serialNumber: document.getElementById('serialNumber') ? document.getElementById('serialNumber').value : ''
    };

    fetch('/api/homekit/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
    })
    .then(function(response) { return response.json(); })
    .then(function(result) {
        if (result.error) {
            VCLU.showToast('Error: ' + result.error, 'danger');
        } else {
            var msg = 'HomeKit configuration saved!';
            if (result.warning) msg += '\n' + result.warning;
            VCLU.showToast(msg, 'success');
            // Refresh status after save
            setTimeout(function() { self.loadStatus(); }, 1000);
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
    })
    .finally(function() {
        if (btn) {
            btn.disabled = false;
            btn.innerHTML = '<i class="bi bi-save"></i> Save Configuration';
        }
    });
};

/**
 * Restart HomeKit bridge
 * @param {HTMLElement} btn - Button element that triggered restart
 */
HomeKitManager.prototype.restart = function(btn) {
    var self = this;

    if (!confirm('Restart HomeKit bridge? This will temporarily disconnect all Apple Home devices.')) {
        return;
    }

    // Find the actual button (might be icon inside)
    while (btn && btn.tagName !== 'BUTTON') {
        btn = btn.parentElement;
    }

    if (!btn) return;

    var originalHtml = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '<i class="bi bi-hourglass-split"></i> Restarting...';

    fetch('/api/homekit/restart', { method: 'POST' })
        .then(function(response) {
            return response.json();
        })
        .then(function(result) {
            if (result.error) {
                VCLU.showToast('Error: ' + result.error, 'danger');
            } else {
                VCLU.showToast('HomeKit bridge restarted successfully!', 'success');
                // Reload status after short delay
                setTimeout(function() {
                    self.loadStatus();
                }, 2000);
            }
        })
        .catch(function(error) {
            VCLU.showToast('Error: ' + error.message, 'danger');
        })
        .finally(function() {
            btn.disabled = false;
            btn.innerHTML = originalHtml;
        });
};

/**
 * Load current HomeKit status
 */
HomeKitManager.prototype.loadStatus = function() {
    var self = this;

    fetch('/api/homekit/status')
        .then(function(response) {
            return response.json();
        })
        .then(function(status) {
            self._updateStatusUI(status);
        })
        .catch(function(err) {
            console.error('Error loading HomeKit status:', err);
        });
};

/**
 * Update UI with status data
 * @param {Object} status - Status object from API
 */
HomeKitManager.prototype._updateStatusUI = function(status) {
    // Update status badge
    if (this.elements.statusBadge) {
        if (status.running) {
            this.elements.statusBadge.className = 'badge bg-success';
            this.elements.statusBadge.innerHTML = '<i class="bi bi-check-circle"></i> Running';
        } else {
            this.elements.statusBadge.className = 'badge bg-secondary';
            this.elements.statusBadge.innerHTML = '<i class="bi bi-stop-circle"></i> Stopped';
        }
    }

    // Update accessories count
    if (this.elements.statAccessories) {
        this.elements.statAccessories.textContent = status.accessoryCount || 0;
    }

    // Update port
    if (this.elements.statPort) {
        this.elements.statPort.textContent = status.port || '-';
    }

    // Update pairing status
    if (this.elements.statPairing) {
        var labelEl = this.elements.statPairing.parentElement.querySelector('.text-muted');
        if (status.pairingAvailable) {
            this.elements.statPairing.innerHTML = '<i class="bi bi-qr-code text-success"></i>';
            if (labelEl) labelEl.textContent = 'Ready to Pair';
        } else {
            this.elements.statPairing.innerHTML = '<i class="bi bi-lock-fill text-warning"></i>';
            if (labelEl) labelEl.textContent = 'Paired';
        }
    }
};

/**
 * Start auto-refresh interval
 */
HomeKitManager.prototype.startAutoRefresh = function() {
    var self = this;

    if (this.intervalId) {
        clearInterval(this.intervalId);
    }

    this.intervalId = setInterval(function() {
        self.loadStatus();
    }, this.refreshInterval);
};

/**
 * Stop auto-refresh interval
 */
HomeKitManager.prototype.stopAutoRefresh = function() {
    if (this.intervalId) {
        clearInterval(this.intervalId);
        this.intervalId = null;
    }
};


/**
 * Get current configuration from form
 * @returns {Object} Configuration object
 */
HomeKitManager.prototype.getConfig = function() {
    return {
        enabled: this.elements.enabledCheckbox ? this.elements.enabledCheckbox.checked : false,
        bridgeName: document.getElementById('bridgeName') ? document.getElementById('bridgeName').value : '',
        serialNumber: document.getElementById('serialNumber') ? document.getElementById('serialNumber').value : '',
        pin: document.getElementById('pin') ? document.getElementById('pin').value : '',
        port: document.getElementById('port') ? parseInt(document.getElementById('port').value, 10) : 5123
    };
};

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    window.homeKitManager = new HomeKitManager();
    window.homeKitManager.init();
});

// Global functions for inline onclick handlers
function saveHomeKitConfig() {
    if (window.homeKitManager) {
        window.homeKitManager.saveConfig();
    }
}

function restartHomeKit() {
    if (window.homeKitManager) {
        window.homeKitManager.restart(event.target);
    }
}
