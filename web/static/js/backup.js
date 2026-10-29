/**
 * BackupManager - Backup creation and management
 * Uses prototype pattern for modularity
 * @param {Object} options - Configuration options
 */
function BackupManager(options) {
    this.options = options || {};
    this.translations = this.options.translations || {};

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize backup manager
 */
BackupManager.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        createBtn: document.getElementById('createBackupBtn'),
        loading: document.getElementById('backupListLoading'),
        empty: document.getElementById('backupListEmpty'),
        error: document.getElementById('backupListError'),
        table: document.getElementById('backupTable'),
        tbody: document.getElementById('backupTableBody')
    };

    // Bind create button
    if (this.elements.createBtn) {
        this.elements.createBtn.addEventListener('click', function() {
            self.create();
        });
    }

    // Load settings and backups on init
    this.loadSettings();
    this.loadList();
};

/**
 * Create a new backup
 */
BackupManager.prototype.create = function() {
    var self = this;
    var btn = this.elements.createBtn;

    // A backup is already running
    if (!btn || btn.disabled) return;

    var originalText = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm" role="status"></span> ' +
                    this.translations.creating;

    fetch('/api/backup/create', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast('Error: ' + data.error, 'danger');
        } else {
            VCLU.showToast(
                self.translations.createdSuccess + ': ' +
                data.filename + ' (' + data.sizeHuman + ')',
                'success'
            );
            self.loadList();
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
    })
    .finally(function() {
        btn.disabled = false;
        btn.innerHTML = originalText;
    });
};

/**
 * Load backup list
 */
BackupManager.prototype.loadList = function() {
    var self = this;

    this._showLoading();

    fetch('/api/backup/list')
        .then(function(response) {
            return response.json();
        })
        .then(function(data) {
            self.elements.loading.style.display = 'none';

            if (data.error) {
                self._showError(data.error);
                return;
            }

            if (!data.backups || data.backups.length === 0) {
                self._showEmpty();
                return;
            }

            self._renderList(data.backups);
        })
        .catch(function(err) {
            self.elements.loading.style.display = 'none';
            self._showError(err.message);
        });
};

/**
 * Render backup list
 * @param {Array} backups - Array of backup objects
 */
BackupManager.prototype._renderList = function(backups) {
    var self = this;

    this.elements.empty.style.display = 'none';
    this.elements.error.style.display = 'none';
    this.elements.tbody.innerHTML = '';

    backups.forEach(function(backup) {
        var row = document.createElement('tr');
        row.innerHTML =
            '<td><i class="bi bi-file-zip text-warning"></i> ' + VCLU.esc(backup.filename) + '</td>' +
            '<td>' + VCLU.esc(backup.sizeHuman) + '</td>' +
            '<td>' + VCLU.esc(backup.created) + '</td>' +
            '<td class="text-end text-nowrap">' +
                '<a href="/api/backup/download/' + encodeURIComponent(backup.filename) + '" class="btn btn-sm btn-outline-primary me-1">' +
                    '<i class="bi bi-download"></i> ' + self.translations.download +
                '</a>' +
                '<button class="btn btn-sm btn-outline-warning me-1 backup-restore-btn" data-filename="' + VCLU.esc(backup.filename) + '">' +
                    '<i class="bi bi-arrow-counterclockwise"></i> ' + self.translations.restore +
                '</button>' +
                '<button class="btn btn-sm btn-outline-danger backup-delete-btn" data-filename="' + VCLU.esc(backup.filename) + '">' +
                    '<i class="bi bi-trash"></i>' +
                '</button>' +
            '</td>';
        self.elements.tbody.appendChild(row);
    });

    // Bind restore buttons
    this.elements.tbody.querySelectorAll('.backup-restore-btn').forEach(function(btn) {
        btn.addEventListener('click', function() {
            self.restore(btn.dataset.filename);
        });
    });

    // Bind delete buttons
    this.elements.tbody.querySelectorAll('.backup-delete-btn').forEach(function(btn) {
        btn.addEventListener('click', function() {
            self.delete(btn.dataset.filename);
        });
    });

    this.elements.table.style.display = 'table';
};

/**
 * Delete a backup
 * @param {string} filename - Backup filename
 */
BackupManager.prototype.delete = function(filename) {
    var self = this;

    if (!confirm(this.translations.deleteConfirm + ': ' + filename + '?')) {
        return;
    }

    fetch('/api/backup/' + encodeURIComponent(filename), {
        method: 'DELETE'
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast('Error: ' + data.error, 'danger');
        } else {
            self.loadList();
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
    });
};

/**
 * Restore a backup
 * @param {string} filename - Backup filename
 */
BackupManager.prototype.restore = function(filename) {
    var self = this;

    if (!confirm(this.translations.restoreConfirm + ': ' + filename + '?\n\n' + this.translations.restoreWarning)) {
        return;
    }

    fetch('/api/backup/restore', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ filename: filename })
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast('Error: ' + data.error, 'danger');
        } else {
            VCLU.showToast(self.translations.restoreSuccess, 'success');
            self.loadList();
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
    });
};

/**
 * Load backup settings
 */
BackupManager.prototype.loadSettings = function() {
    fetch('/api/backup/settings')
        .then(function(response) {
            return response.json();
        })
        .then(function(data) {
            var el = document.getElementById('autoBackupEnabled');
            if (el) el.checked = data.autoEnabled || false;

            el = document.getElementById('backupInterval');
            if (el) el.value = data.intervalH || 24;

            el = document.getElementById('backupMaxKeep');
            if (el) el.value = data.maxBackups !== undefined ? data.maxBackups : 7;
        })
        .catch(function() {
            // Ignore — use defaults
        });
};

/**
 * Save backup settings
 */
BackupManager.prototype.saveSettings = function() {
    var self = this;
    var data = {
        autoEnabled: document.getElementById('autoBackupEnabled').checked,
        intervalH: parseInt(document.getElementById('backupInterval').value, 10) || 24,
        maxBackups: parseInt(document.getElementById('backupMaxKeep').value, 10) || 7
    };

    var btn = document.getElementById('saveBackupSettingsBtn');
    if (btn) btn.disabled = true;

    fetch('/api/backup/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(resp) {
        if (resp.error) {
            VCLU.showToast('Error: ' + resp.error, 'danger');
        } else {
            VCLU.showToast(self.translations.settingsSaved, 'success');
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
    })
    .finally(function() {
        if (btn) btn.disabled = false;
    });
};

/**
 * Show loading state
 */
BackupManager.prototype._showLoading = function() {
    this.elements.loading.style.display = 'block';
    this.elements.empty.style.display = 'none';
    this.elements.error.style.display = 'none';
    this.elements.table.style.display = 'none';
};

/**
 * Show empty state
 */
BackupManager.prototype._showEmpty = function() {
    this.elements.empty.style.display = 'block';
    this.elements.table.style.display = 'none';
};

/**
 * Show error state
 * @param {string} message - Error message
 */
BackupManager.prototype._showError = function(message) {
    this.elements.error.textContent = message;
    this.elements.error.style.display = 'block';
};



// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    window.backupManager = new BackupManager({
        translations: window.backupTranslations || {}
    });
    window.backupManager.init();
});

// Legacy functions for backwards compatibility with inline onclick
function loadBackups() {
    if (window.backupManager) {
        window.backupManager.loadList();
    }
}

function createBackup() {
    if (window.backupManager) {
        window.backupManager.create();
    }
}

function deleteBackup(filename) {
    if (window.backupManager) {
        window.backupManager.delete(filename);
    }
}

function saveBackupSettings() {
    if (window.backupManager) {
        window.backupManager.saveSettings();
    }
}
