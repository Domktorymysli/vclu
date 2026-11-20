/**
 * OmFilesManager - OM file upload and management
 * Uses prototype pattern for modularity
 * @param {Object} options - Configuration options
 */
function OmFilesManager(options) {
    this.options = options || {};
    this.translations = this.options.translations || {};
    this.detailsModal = null;

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize OM files manager
 */
OmFilesManager.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        uploadForm: document.getElementById('uploadForm'),
        uploadBtn: document.getElementById('uploadBtn'),
        fileInput: document.getElementById('omFile'),
        uploadResult: document.getElementById('uploadResult'),

        loading: document.getElementById('filesListLoading'),
        empty: document.getElementById('filesListEmpty'),
        error: document.getElementById('filesListError'),
        table: document.getElementById('filesTable'),
        tbody: document.getElementById('filesTableBody'),

        // Modal
        detailsModal: document.getElementById('detailsModal'),
        modalTitle: document.getElementById('modalTitle'),
        modalLoading: document.getElementById('modalLoading'),
        modalContent: document.getElementById('modalContent'),
        modulesBody: document.getElementById('modulesBody'),
        objectsBody: document.getElementById('objectsBody')
    };

    // Initialize Bootstrap modal
    if (this.elements.detailsModal) {
        this.detailsModal = new bootstrap.Modal(this.elements.detailsModal);
    }

    // Bind upload form
    if (this.elements.uploadForm) {
        this.elements.uploadForm.addEventListener('submit', function(e) {
            e.preventDefault();
            self.upload();
        });
    }

    // Load files on init
    this.loadFiles();
};

/**
 * Upload OM file
 */
OmFilesManager.prototype.upload = function() {
    var self = this;
    var btn = this.elements.uploadBtn;
    var fileInput = this.elements.fileInput;
    var result = this.elements.uploadResult;

    if (!fileInput.files[0]) {
        result.innerHTML = '<div class="alert alert-warning">' +
                          (this.translations.selectFile || 'Select a file') + '</div>';
        result.style.display = 'block';
        return;
    }

    var file = fileInput.files[0];
    var originalText = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span> ' +
                    (this.translations.validating || 'Validating...');

    // what the import would change first: removed or changed objects, and Lua that would break
    var preview = new FormData();
    preview.append('file', file);
    fetch('/api/om/preview', { method: 'POST', body: preview })
    .then(function(response) { return response.json(); })
    .then(function(data) {
        // never import without a preview: its error is the answer
        if (data.error) {
            self._previewFailed(data.error, originalText);
            return;
        }
        if (!self._needsConfirm(data)) {
            self._send(file, originalText, false);
            return;
        }
        btn.disabled = false;
        btn.innerHTML = originalText;
        result.innerHTML = self._renderPreview(data);
        result.style.display = 'block';
        document.getElementById('omPreviewImport').onclick = function() {
            // once: a second click would upload again
            this.disabled = true;
            document.getElementById('omPreviewCancel').disabled = true;
            btn.disabled = true;
            btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span> ' +
                            (self.translations.validating || 'Validating...');
            self._send(file, originalText, true);
        };
        document.getElementById('omPreviewCancel').onclick = function() {
            result.innerHTML = '';
            result.style.display = 'none';
        };
    })
    .catch(function(error) {
        self._previewFailed(error.message, originalText);
    });
};

/**
 * Show why there is no preview; nothing is imported
 */
OmFilesManager.prototype._previewFailed = function(message, originalText) {
    var btn = this.elements.uploadBtn;
    var result = this.elements.uploadResult;
    btn.disabled = false;
    btn.innerHTML = originalText;
    result.innerHTML = '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> ' +
                       VCLU.esc((this.translations.previewFailed || 'Not imported, no preview') + ': ' + message) + '</div>';
    result.style.display = 'block';
};

/**
 * Whether a preview shows something to confirm: a CLU removed or changed, or Lua that breaks
 */
OmFilesManager.prototype._needsConfirm = function(data) {
    var changed = (data.clus || []).some(function(c) {
        return c.change === 'removed' || (c.change === 'changed' && (c.removed.length > 0 || c.retyped.length > 0 || c.renamedFrom));
    });
    return changed || (data.breaks && data.breaks.length > 0);
};

/**
 * Render what an import would change, with Import and Cancel
 */
OmFilesManager.prototype._renderPreview = function(data) {
    var tr = this.translations;
    var html = '<div class="alert alert-warning"><p class="mb-2"><i class="bi bi-exclamation-triangle"></i> <strong>' +
               VCLU.esc(tr.previewTitle || 'This import changes:') + '</strong></p><ul class="small mb-2">';
    (data.clus || []).forEach(function(c) {
        var name = VCLU.esc((c.name || c.id) + ' (' + c.id + ')');
        if (c.change === 'removed') {
            html += '<li>' + name + ': ' + VCLU.esc(tr.cluRemoved || 'removed, not in the project') + '</li>';
            return;
        }
        if (c.change !== 'changed') return;
        var parts = [];
        if (c.renamedFrom) {
            parts.push(VCLU.esc((tr.renamed || 'renamed from') + ' ' + c.renamedFrom));
        }
        if (c.removed.length > 0) {
            parts.push(VCLU.esc(tr.objectsRemoved || 'objects removed') + ': ' +
                       c.removed.map(function(o) { return VCLU.esc(o.name ? o.id + ' ' + o.name : o.id); }).join(', '));
        }
        if (c.retyped.length > 0) {
            parts.push(VCLU.esc(tr.objectsRetyped || 'other type') + ': ' +
                       c.retyped.map(function(o) { return VCLU.esc(o.id + ' ' + o.from + ' → ' + o.to); }).join(', '));
        }
        if (parts.length > 0) {
            html += '<li>' + name + ': ' + parts.join('; ') + '</li>';
        }
    });
    html += '</ul>';
    if (data.breaks && data.breaks.length > 0) {
        html += '<p class="mb-1 small"><strong>' + VCLU.esc(tr.breaks || 'Your Lua that would name a missing object') + ':</strong></p>' +
                this._renderRefs(data.breaks);
    }
    html += '<button type="button" class="btn btn-warning btn-sm" id="omPreviewImport">' + VCLU.esc(tr.importAnyway || 'Import') +
            '</button> <button type="button" class="btn btn-secondary btn-sm" id="omPreviewCancel">' +
            VCLU.esc(tr.cancel || 'Cancel') + '</button></div>';
    return html;
};

/**
 * Render places in modules/ as file:line path
 */
OmFilesManager.prototype._renderRefs = function(refs) {
    return '<ul class="small mb-2">' + refs.map(function(r) {
        return '<li><code>modules/' + VCLU.esc(r.file) + ':' + r.line + '</code> ' + VCLU.esc(r.path) + '</li>';
    }).join('') + '</ul>';
};

/**
 * Note after an import: places in modules/ that name an object no CLU has
 */
OmFilesManager.prototype._brokenNote = function(data) {
    if (!data.brokenRefs || data.brokenRefs.length === 0) return '';
    return '<div class="alert alert-warning"><i class="bi bi-exclamation-triangle"></i> ' +
           VCLU.esc(this.translations.brokenRefs || 'Your Lua names objects no CLU has') + ':' +
           this._renderRefs(data.brokenRefs) + '</div>';
};

/**
 * Upload the file; removeMissing after a confirmed preview lets a project remove the CLUs it lacks
 */
OmFilesManager.prototype._send = function(file, originalText, removeMissing) {
    var self = this;
    var btn = this.elements.uploadBtn;
    var fileInput = this.elements.fileInput;
    var result = this.elements.uploadResult;
    var formData = new FormData();
    formData.append('file', file);
    if (removeMissing) {
        formData.append('removeMissing', 'true');
    }

    fetch('/api/om/upload', {
        method: 'POST',
        body: formData
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(data) {
        if (data.error) {
            result.innerHTML = '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> ' +
                              VCLU.esc(data.error) + '</div>';
        } else if (data.isZip || data.isProject) {
            result.innerHTML = self._renderZipResult(data) + self._brokenNote(data);
            fileInput.value = '';
            self.loadFiles();
        } else {
            result.innerHTML = '<div class="alert alert-success"><i class="bi bi-check-circle"></i> ' +
                              (self.translations.fileSaved || 'File saved') + ': ' +
                              VCLU.esc(data.cluName) + ' (' + data.objectCount + ' ' +
                              (self.translations.objects || 'objects') + ', ' + data.moduleCount + ' ' +
                              (self.translations.modules || 'modules') + ')</div>' +
                              self._reloadNote(data);
            if (data.luaError) {
                result.innerHTML += '<div class="alert alert-warning"><i class="bi bi-exclamation-triangle"></i> ' +
                                    VCLU.esc(data.luaError) + '</div>';
            }
            if (data.warnings && data.warnings.length > 0) {
                result.innerHTML += '<div class="alert alert-warning"><strong>' +
                                    VCLU.esc(self.translations.warnings || 'Left out') + ':</strong> ' +
                                    data.warnings.map(VCLU.esc).join('; ') + '</div>';
            }
            result.innerHTML += self._brokenNote(data);
            fileInput.value = '';
            self.loadFiles();
        }
        result.style.display = 'block';
    })
    .catch(function(error) {
        result.innerHTML = '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> Error: ' +
                          VCLU.esc(error.message) + '</div>';
        result.style.display = 'block';
    })
    .finally(function() {
        btn.disabled = false;
        btn.innerHTML = originalText;
    });
};

/**
 * Render ZIP upload result
 * @param {Object} data - Response data
 * @returns {string} HTML string
 */
OmFilesManager.prototype._renderZipResult = function(data) {
    var html = '<div class="alert alert-' + (data.success ? 'success' : 'warning') + '">';
    var title = data.isProject ? (this.translations.project || 'Object Manager project') :
                                 (this.translations.diagnosticPack || 'Diagnostic Pack');
    html += '<h6><i class="bi bi-file-earmark-zip"></i> ' + title + ': ' +
            VCLU.esc(data.originalName) + (data.omVersion ? ' (OM ' + VCLU.esc(data.omVersion) + ')' : '') + '</h6>';
    html += '<p class="mb-2">' + (this.translations.imported || 'Imported') + ': <strong>' +
            data.imported + '</strong> ' + (this.translations.cluFiles || 'CLU files');

    if (data.skipped > 0) {
        html += ' <span class="text-muted">(' + (this.translations.skipped || 'skipped') + ': ' +
                data.skipped + ')</span>';
    }
    html += '</p>';

    if (data.results && data.results.length > 0) {
        html += '<ul class="mb-2">';
        data.results.forEach(function(r) {
            html += '<li><i class="bi bi-check text-success"></i> ' +
                   VCLU.esc(r.cluName) + ' (' + r.objectCount + ' obj, ' + r.moduleCount + ' mod)</li>';
        });
        html += '</ul>';
    }

    if (data.skippedList && data.skippedList.length > 0) {
        html += '<div class="text-muted small"><i class="bi bi-skip-forward"></i> ' +
               (this.translations.skipped || 'Skipped') + ': ' + data.skippedList.map(VCLU.esc).join(', ') + '</div>';
    }

    if (data.removed && data.removed.length > 0) {
        html += '<div class="text-warning small"><i class="bi bi-trash"></i> ' +
               (this.translations.removed || 'Removed, not in the project') + ': ' + data.removed.map(VCLU.esc).join(', ') + '</div>';
    }

    if (data.agentDocs) {
        html += '<div class="text-muted small"><i class="bi bi-robot"></i> ' +
               (this.translations.agentDocs || 'Event bindings and scripts for the agent') + ': ' +
               VCLU.esc(data.agentDocs) + '/</div>';
    }

    if (data.warnings && data.warnings.length > 0) {
        html += '<div class="text-warning mt-2"><strong>' +
               (this.translations.warnings || 'Left out') + ':</strong><ul class="mb-0">';
        data.warnings.forEach(function(w) {
            html += '<li>' + VCLU.esc(w) + '</li>';
        });
        html += '</ul></div>';
    }

    if (data.errors && data.errors.length > 0) {
        html += '<div class="text-danger mt-2"><strong>' +
               (this.translations.errors || 'Errors') + ':</strong><ul class="mb-0">';
        data.errors.forEach(function(e) {
            html += '<li>' + VCLU.esc(e) + '</li>';
        });
        html += '</ul></div>';
    }

    html += '</div>';
    return html + this._reloadNote(data);
};

/**
 * Whether the Lua runtime took the change: an import, delete or regenerate reloads it
 * @param {Object} data - Response data (reloaded, reloadError, moduleErrors)
 * @returns {string} HTML string, empty when there was no reload
 */
OmFilesManager.prototype._reloadNote = function(data) {
    if (data.reloadError) {
        return '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> ' +
               (this.translations.reloadFailed || 'Lua reload failed') + ': ' + VCLU.esc(data.reloadError) + '</div>';
    }
    if (data.proxyErrors && data.proxyErrors.length > 0) {
        return '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> ' +
               (this.translations.proxyFailed || 'Lua reloaded, but the CLU objects failed to load') + ':<ul class="mb-0">' +
               data.proxyErrors.map(function(e) { return '<li>' + VCLU.esc(e) + '</li>'; }).join('') + '</ul></div>';
    }
    if (data.moduleErrors && data.moduleErrors.length > 0) {
        return '<div class="alert alert-warning"><i class="bi bi-exclamation-triangle"></i> ' +
               (this.translations.modulesFailed || 'Lua reloaded, but modules failed to load') + ':<ul class="mb-0">' +
               data.moduleErrors.map(function(e) { return '<li>' + VCLU.esc(e) + '</li>'; }).join('') + '</ul></div>';
    }
    if (data.reloaded) {
        return '<div class="text-muted small"><i class="bi bi-arrow-repeat"></i> ' +
               (this.translations.reloaded || 'Lua reloaded with the new objects') + '</div>';
    }
    return '';
};

/**
 * Load OM files list
 */
OmFilesManager.prototype.loadFiles = function() {
    var self = this;

    this._showLoading();

    fetch('/api/om/list')
        .then(function(response) {
            return response.json();
        })
        .then(function(data) {
            self.elements.loading.style.display = 'none';

            if (data.error) {
                self._showError(data.error);
                return;
            }

            if (!data.files || data.files.length === 0) {
                self._showEmpty();
                return;
            }

            self._renderFiles(data.files);
        })
        .catch(function(err) {
            self.elements.loading.style.display = 'none';
            self._showError(err.message);
        });
};

/**
 * Render files table
 * @param {Array} files - Array of file objects
 */
OmFilesManager.prototype._renderFiles = function(files) {
    var self = this;

    this.elements.empty.style.display = 'none';
    this.elements.error.style.display = 'none';
    this.elements.tbody.innerHTML = '';

    files.forEach(function(file) {
        var row = document.createElement('tr');
        var date = new Date(file.uploadedAt).toLocaleDateString();

        row.innerHTML =
            '<td><i class="bi bi-hdd-network text-primary"></i> <strong>' +
                VCLU.esc(file.cluName || file.filename) + '</strong><br>' +
                '<small class="text-muted">' + VCLU.esc(file.serial) + '</small></td>' +
            '<td><code>' + VCLU.esc(file.cluIp) + '</code></td>' +
            '<td>' + file.objectCount + '</td>' +
            '<td>' + file.moduleCount + '</td>' +
            '<td>' + date + '</td>' +
            '<td class="text-end">' +
                '<button class="btn btn-sm btn-info me-1 details-btn" data-serial="' + file.serial + '">' +
                    '<i class="bi bi-eye"></i>' +
                '</button>' +
                '<button class="btn btn-sm btn-danger delete-btn" data-serial="' + file.serial + '">' +
                    '<i class="bi bi-trash"></i>' +
                '</button>' +
            '</td>';
        self.elements.tbody.appendChild(row);
    });

    // Bind buttons
    this.elements.tbody.querySelectorAll('.details-btn').forEach(function(btn) {
        btn.addEventListener('click', function() {
            self.showDetails(btn.dataset.serial);
        });
    });

    this.elements.tbody.querySelectorAll('.delete-btn').forEach(function(btn) {
        btn.addEventListener('click', function() {
            self.deleteFile(btn.dataset.serial);
        });
    });

    this.elements.table.style.display = 'table';
};

/**
 * Delete OM file
 * @param {string} serial - File serial number
 */
OmFilesManager.prototype.deleteFile = function(serial) {
    var self = this;

    if (!confirm(this.translations.deleteConfirm || 'Delete OM file?')) {
        return;
    }

    fetch('/api/om/' + serial, { method: 'DELETE' })
        .then(function(response) {
            return response.json();
        })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast('Error: ' + data.error, 'danger');
            } else {
                var problems = [].concat(data.reloadError ? [data.reloadError] : [], data.cleanupError ? [data.cleanupError] : [], data.proxyErrors || [], data.moduleErrors || []);
                if (problems.length > 0) {
                    VCLU.showToast(problems.join('; '), 'warning');
                }
                var result = self.elements.uploadResult;
                if (result && data.brokenRefs && data.brokenRefs.length > 0) {
                    result.innerHTML = self._brokenNote(data);
                    result.style.display = 'block';
                }
                self.loadFiles();
            }
        })
        .catch(function(error) {
            VCLU.showToast('Error: ' + error.message, 'danger');
        });
};

/**
 * Show file details in modal
 * @param {string} serial - File serial number
 */
OmFilesManager.prototype.showDetails = function(serial) {
    var self = this;

    this.elements.modalLoading.style.display = 'block';
    this.elements.modalContent.style.display = 'none';
    this.detailsModal.show();

    fetch('/api/om/' + serial)
        .then(function(response) {
            return response.json();
        })
        .then(function(data) {
            self.elements.modalLoading.style.display = 'none';

            if (data.error) {
                self.elements.modalContent.innerHTML =
                    '<div class="alert alert-danger">' + VCLU.esc(data.error) + '</div>';
                self.elements.modalContent.style.display = 'block';
                return;
            }

            self.elements.modalTitle.textContent = data.file.cluName || data.file.filename;
            self._renderModules(data.modules || []);
            self._renderObjects(data.objects || []);
            self.elements.modalContent.style.display = 'block';
        })
        .catch(function(error) {
            self.elements.modalLoading.style.display = 'none';
            self.elements.modalContent.innerHTML =
                '<div class="alert alert-danger">' + VCLU.esc(error.message) + '</div>';
            self.elements.modalContent.style.display = 'block';
        });
};

/**
 * Render modules table in modal
 * @param {Array} modules - Array of module objects
 */
OmFilesManager.prototype._renderModules = function(modules) {
    var self = this;
    this.elements.modulesBody.innerHTML = '';

    modules.forEach(function(m) {
        var row = document.createElement('tr');
        row.innerHTML =
            '<td><code>' + VCLU.esc(m.ID) + '</code></td>' +
            '<td>' + VCLU.esc(m.Serial) + '</td>' +
            '<td>0x' + m.ModuleType.toString(16) + '</td>';
        self.elements.modulesBody.appendChild(row);
    });
};

/**
 * Render objects table in modal
 * @param {Array} objects - Array of object definitions
 */
OmFilesManager.prototype._renderObjects = function(objects) {
    var self = this;
    var classNames = {3: 'DIN', 4: 'DOUT', 5: 'DIMMER', 9: 'LED', 11: 'ROLLER', 24: 'ROLLER'};

    this.elements.objectsBody.innerHTML = '';

    objects.forEach(function(obj) {
        var row = document.createElement('tr');
        row.innerHTML =
            '<td><code>' + VCLU.esc(obj.ID) + '</code></td>' +
            '<td>' + VCLU.esc(obj.Name || '-') + '</td>' +
            '<td>' + (classNames[obj.Class] || obj.Class) + '</td>' +
            '<td><small>' + VCLU.esc(obj.ModuleID) + '</small></td>';
        self.elements.objectsBody.appendChild(row);
    });
};

/**
 * Show loading state
 */
OmFilesManager.prototype._showLoading = function() {
    this.elements.loading.style.display = 'block';
    this.elements.empty.style.display = 'none';
    this.elements.error.style.display = 'none';
    this.elements.table.style.display = 'none';
};

/**
 * Show empty state
 */
OmFilesManager.prototype._showEmpty = function() {
    this.elements.empty.style.display = 'block';
    this.elements.table.style.display = 'none';
};

/**
 * Show error state
 * @param {string} message - Error message
 */
OmFilesManager.prototype._showError = function(message) {
    this.elements.error.textContent = message;
    this.elements.error.style.display = 'block';
};

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    window.omFilesManager = new OmFilesManager();
    window.omFilesManager.init();
});

// Legacy functions for backwards compatibility with inline onclick
function loadFiles() {
    if (window.omFilesManager) {
        window.omFilesManager.loadFiles();
    }
}

function deleteFile(serial) {
    if (window.omFilesManager) {
        window.omFilesManager.deleteFile(serial);
    }
}

function showDetails(serial) {
    if (window.omFilesManager) {
        window.omFilesManager.showDetails(serial);
    }
}
