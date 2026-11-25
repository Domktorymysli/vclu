/**
 * PluginsManager - Plugin repository and installation management
 * Uses prototype pattern for modularity
 * @param {Object} options - Configuration options
 */
function PluginsManager(options) {
    this.options = options || {};
    this.installedPlugins = {};
    this.availablePlugins = [];
    this.pluginDocs = {};
    this.currentConfigPlugin = null;

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize plugins manager
 */
PluginsManager.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        availableLoading: document.getElementById('availableLoading'),
        availableError: document.getElementById('availableError'),
        availableList: document.getElementById('availableList'),
        installedList: document.getElementById('installedList'),
        installedCount: document.getElementById('installedCount'),

        // Tab buttons
        availableTabBtn: document.getElementById('available-tab'),
        installedTabBtn: document.getElementById('installed-tab'),
        // Tab content
        availableTab: document.getElementById('availableTab'),
        installedTab: document.getElementById('installedTab'),

        // Modals
        addRepoModal: document.getElementById('addRepoModal'),
        configModal: document.getElementById('configModal'),
        docModal: document.getElementById('docModal'),
        permissionsModal: document.getElementById('permissionsModal'),
        permissionsPluginId: document.getElementById('permissionsPluginId'),
        permissionsFields: document.getElementById('permissionsFields'),
        installModal: document.getElementById('installModal'),
        installPluginName: document.getElementById('installPluginName'),
        installPermissions: document.getElementById('installPermissions'),
        installNote: document.getElementById('installNote'),
        installConfirm: document.getElementById('installConfirm'),
        configPluginId: document.getElementById('configPluginId'),
        configFields: document.getElementById('configFields'),
        docTitle: document.getElementById('docTitle'),
        docLoading: document.getElementById('docLoading'),
        docContent: document.getElementById('docContent'),
        docError: document.getElementById('docError'),
        docNav: document.getElementById('docNav'),

        // Repository form
        repoName: document.getElementById('repoName'),
        repoUrl: document.getElementById('repoUrl')
    };

    // Load installed plugins data from page
    this._loadInstalledPluginsData();

    // Setup tab switching
    this._setupTabs();

    // Load available plugins
    this.refreshAvailable();

    // Render installed plugins (will be re-rendered after available loads)
    this._renderInstalledPlugins();

    // Check for plugin docs
    this.checkPluginDocs();
};

/**
 * Setup custom tab switching
 */
PluginsManager.prototype._setupTabs = function() {
    var self = this;
    var tabBtns = document.querySelectorAll('.plugins-tab[data-tab]');

    tabBtns.forEach(function(btn) {
        btn.addEventListener('click', function() {
            var tabName = this.dataset.tab;
            self.switchTab(tabName);
        });
    });
};

/**
 * Load installed plugins data from script tag
 */
PluginsManager.prototype._loadInstalledPluginsData = function() {
    // This data is passed from the template
    // Override this method or call setInstalledPlugins() after init
};

/**
 * Set installed plugins data
 * @param {Object} plugins - Map of plugin ID to plugin data
 */
PluginsManager.prototype.setInstalledPlugins = function(plugins) {
    this.installedPlugins = plugins || {};
};

/**
 * Reload installed plugins from the server (permissions an update asks for) and re-render
 */
PluginsManager.prototype.refreshInstalled = function() {
    var self = this;
    fetch('/api/plugins/installed')
        .then(function(r) { return r.json(); })
        .then(function(data) {
            (data.installed || []).forEach(function(p) {
                self.installedPlugins[p.id] = p;
            });
            self._renderInstalledPlugins();
        });
};

/**
 * Toggle config visibility
 * @param {HTMLElement} btn - Toggle button
 */
PluginsManager.prototype.toggleConfig = function(btn) {
    var content = btn.nextElementSibling;
    if (content.style.display === 'none') {
        content.style.display = 'block';
        btn.innerHTML = '<i class="bi bi-eye-slash"></i> Hide config';
    } else {
        content.style.display = 'none';
        btn.innerHTML = '<i class="bi bi-eye"></i> Show config';
    }
};

/**
 * Refresh available plugins from repositories
 */
PluginsManager.prototype.refreshAvailable = function() {
    var self = this;

    this.elements.availableLoading.style.display = 'block';
    this.elements.availableList.style.display = 'none';
    this.elements.availableError.style.display = 'none';

    fetch('/api/plugins/available')
        .then(function(r) { return r.json(); })
        .then(function(data) {
            self.elements.availableLoading.style.display = 'none';

            if (data.errors && data.errors.length > 0) {
                self.elements.availableError.innerHTML =
                    '<i class="bi bi-exclamation-triangle"></i> ' + data.errors.join(', ');
                self.elements.availableError.style.display = 'block';
            }

            self._renderAvailablePlugins(data.plugins || []);
        })
        .catch(function(err) {
            self.elements.availableLoading.style.display = 'none';
            self.elements.availableError.innerHTML =
                '<i class="bi bi-exclamation-triangle"></i> Failed to load: ' + err;
            self.elements.availableError.style.display = 'block';
        });
};

/**
 * Render a unified plugin card
 * @param {Object} plugin - Plugin data
 * @param {Object} options - Rendering options (isInstalled, showStatus, etc.)
 * @returns {string} HTML string for the card
 */
PluginsManager.prototype._renderPluginCard = function(plugin, options) {
    var self = this;
    options = options || {};

    var id = plugin.id || '';
    var fullId = plugin.fullId || id;
    var name = plugin.name || id;
    var version = plugin.version || '?';
    var description = plugin.description || '';
    var author = plugin.author || 'Unknown';
    var repository = plugin.repository || '';
    var repoUrl = plugin.repoUrl || '';
    var tags = plugin.tags || [];
    var enabled = plugin.enabled !== false;

    // Tags
    var tagsHtml = '';
    if (tags.length > 0) {
        tagsHtml = '<p class="mb-2">' + tags.map(function(t) {
            return '<span class="badge bg-secondary me-1">' + VCLU.esc(t) + '</span>';
        }).join('') + '</p>';
    }

    // Status badge (for installed plugins)
    var statusBadge = '';
    if (options.isInstalled) {
        if (enabled) {
            statusBadge = '<span class="badge bg-success">enabled</span>';
        } else {
            statusBadge = '<span class="badge bg-warning text-dark">disabled</span>';
        }
        if (plugin.configured === false) {
            statusBadge += ' <span class="badge bg-warning text-dark">not configured</span>';
        }
        if (plugin.pendingPermissions && plugin.pendingPermissions.length > 0) {
            statusBadge += ' <span class="badge bg-danger" title="The update asks for this; the plugin runs without it until allowed">asks for ' +
                           VCLU.esc(plugin.pendingPermissions.join(', ')) + '</span>';
        }
    }

    // Every permission, the ones the plugin uses in green (installed plugins only: the
    // repository index does not say, the install confirmation reads it from plugin.json)
    var permsHtml = '';
    if (options.isInstalled) {
        var requested = plugin.requestedPermissions || [];
        var pending = plugin.pendingPermissions || [];
        permsHtml = '<p class="mb-2 small">' + ALL_PERMISSIONS.map(function(name) {
            var cls = 'bg-transparent border border-secondary text-body-secondary', title = 'Not used';
            if (pending.indexOf(name) >= 0) {
                cls = 'bg-warning text-dark'; title = 'Asked for, not allowed yet';
            } else if (requested.indexOf(name) >= 0) {
                cls = 'bg-success'; title = 'Allowed';
            }
            return '<span class="badge ' + cls + ' me-1" title="' + VCLU.esc(title + ': ' + (PERMISSION_LABELS[name] || name)) + '">' +
                   VCLU.esc(PERMISSION_SHORT[name] || name) + '</span>';
        }).join('') + '</p>';
    }

    // Link buttons
    var linkBtns = '';
    // Docs button (hidden by default, shown by checkPluginDocs)
    linkBtns += '<button class="btn btn-sm btn-outline-info doc-btn me-1" ' +
               'data-plugin-id="' + VCLU.esc(id) + '" ' +
               'data-repository="' + VCLU.esc(repository) + '" ' +
               'style="display:none;" data-bs-toggle="tooltip" data-bs-placement="top" title="Documentation">' +
               '<i class="bi bi-book"></i></button>';
    // Repo link
    if (repoUrl) {
        linkBtns += '<a href="' + VCLU.esc(repoUrl) + '" target="_blank" ' +
                   'class="btn btn-sm btn-outline-secondary me-1" data-bs-toggle="tooltip" data-bs-placement="top" title="View repository">' +
                   '<i class="bi bi-github"></i></a>';
    }

    // Action buttons
    var actionBtns = '';
    if (options.isInstalled) {
        // Configure button
        actionBtns += '<button class="btn btn-sm btn-outline-primary me-1" ' +
                     'onclick="pluginsManager.configure(\'' + VCLU.esc(id) + '\')" ' +
                     'data-bs-toggle="tooltip" data-bs-placement="top" title="Configure">' +
                     '<i class="bi bi-gear"></i></button>';
        // Allow what an update asks for
        if (plugin.pendingPermissions && plugin.pendingPermissions.length > 0) {
            actionBtns += '<button class="btn btn-sm btn-danger me-1" ' +
                         'onclick="pluginsManager.allowPermissions(\'' + VCLU.esc(id) + '\')" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="Allow ' +
                         VCLU.esc(plugin.pendingPermissions.join(', ')) + '">' +
                         '<i class="bi bi-shield-check"></i> Allow</button>';
        }
        // Permissions (what the plugin may reach on the LAN and MQTT)
        if (plugin.requestedPermissions && plugin.requestedPermissions.length > 0) {
            actionBtns += '<button class="btn btn-sm btn-outline-secondary me-1" ' +
                         'onclick="pluginsManager.editPermissions(\'' + VCLU.esc(id) + '\')" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="Permissions">' +
                         '<i class="bi bi-shield-lock"></i></button>';
        }
        // Enable/Disable toggle
        if (enabled) {
            actionBtns += '<button class="btn btn-sm btn-outline-warning me-1" ' +
                         'onclick="pluginsManager.toggle(\'' + VCLU.esc(id) + '\', false)" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="Disable">' +
                         '<i class="bi bi-pause-circle"></i></button>';
        } else {
            actionBtns += '<button class="btn btn-sm btn-outline-success me-1" ' +
                         'onclick="pluginsManager.toggle(\'' + VCLU.esc(id) + '\', true)" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="Enable">' +
                         '<i class="bi bi-play-circle"></i></button>';
        }
        // Update button (if available)
        if (plugin.updateAvailable && plugin.availableVersion) {
            actionBtns += '<button class="btn btn-sm btn-warning me-1" ' +
                         'onclick="pluginsManager.install(\'' + VCLU.esc(id) + '\', \'' +
                         VCLU.esc(repository) + '\')" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="Update to v' + VCLU.esc(plugin.availableVersion) + '">' +
                         '<i class="bi bi-arrow-up-circle"></i></button>';
        }
        // Delete button
        actionBtns += '<button class="btn btn-sm btn-outline-danger" ' +
                     'onclick="pluginsManager.uninstall(\'' + VCLU.esc(id) + '\')" ' +
                     'data-bs-toggle="tooltip" data-bs-placement="top" title="Delete">' +
                     '<i class="bi bi-trash"></i></button>';
    } else {
        // Install/Update button for available plugins
        if (plugin.installed) {
            if (plugin.updateAvailable) {
                actionBtns = '<button class="btn btn-sm btn-warning" onclick="pluginsManager.install(\'' +
                           VCLU.esc(id) + '\', \'' + VCLU.esc(repository) + '\')">' +
                           '<i class="bi bi-arrow-up-circle"></i> Update to v' + VCLU.esc(version) + '</button>';
            } else {
                actionBtns = '<button class="btn btn-sm btn-secondary" disabled>' +
                           '<i class="bi bi-check"></i> Installed</button>';
            }
        } else {
            actionBtns = '<button class="btn btn-sm btn-primary" onclick="pluginsManager.install(\'' +
                       VCLU.esc(id) + '\', \'' + VCLU.esc(repository) + '\')">' +
                       '<i class="bi bi-download"></i> Install</button>';
        }
    }

    return '<div class="col-md-6 mb-3">' +
           '<div class="card h-100">' +
           '<div class="card-body">' +
           '<div class="d-flex justify-content-between align-items-start mb-2">' +
           '<h6 class="card-title mb-0">' + VCLU.esc(name) +
           ' <span class="badge bg-secondary">v' + VCLU.esc(version) + '</span></h6>' +
           statusBadge +
           '</div>' +
           '<p class="card-text small mb-1"><code>' + VCLU.esc(fullId) + '</code></p>' +
           '<p class="card-text small text-muted mb-1">Repository: ' + VCLU.esc(repository) + '</p>' +
           '<p class="card-text small mb-2">' + VCLU.esc(description) + '</p>' +
           '<p class="card-text small mb-2"><span class="text-muted">By:</span> ' + VCLU.esc(author) + '</p>' +
           tagsHtml +
           permsHtml +
           '</div>' +
           '<div class="card-footer bg-transparent d-flex justify-content-between align-items-center">' +
           '<div>' + linkBtns + '</div>' +
           '<div>' + actionBtns + '</div>' +
           '</div>' +
           '</div></div>';
};

/**
 * Render available plugins list
 * @param {Array} plugins - Array of available plugins
 */
PluginsManager.prototype._renderAvailablePlugins = function(plugins) {
    var self = this;
    var list = this.elements.availableList;

    // Store for later use
    this.availablePlugins = plugins || [];

    if (!plugins || plugins.length === 0) {
        list.innerHTML = '<p class="text-muted text-center py-4">No plugins available</p>';
    } else {
        var html = '<div class="row">';
        plugins.forEach(function(p) {
            html += self._renderPluginCard(p, { isInstalled: false });
        });
        html += '</div>';
        list.innerHTML = html;
    }
    list.style.display = 'block';

    // Re-render installed plugins now that we have available plugins data (for author, description, etc.)
    this._renderInstalledPlugins();

    // Check for docs on newly rendered cards
    this.checkPluginDocs();

    // Initialize tooltips
    this._initTooltips();
};

/**
 * Render installed plugins list
 */
PluginsManager.prototype._renderInstalledPlugins = function() {
    var self = this;
    var list = this.elements.installedList;
    if (!list) return;

    var plugins = Object.values(this.installedPlugins);

    // Update count badge
    if (this.elements.installedCount) {
        this.elements.installedCount.textContent = plugins.length;
    }

    if (plugins.length === 0) {
        list.innerHTML = '<p class="text-muted text-center py-4">No plugins installed</p>';
        return;
    }

    // Find available versions for update indicators
    // Several repositories may list one id: the one offering the update (only the plugin's
    // own repository does), else the plugin's own repository, else the first
    var installedRepo = {};
    plugins.forEach(function(p) { installedRepo[p.id] = p.repository; });
    var rank = function(a) { return a.updateAvailable ? 2 : (a.repository === installedRepo[a.fullId] ? 1 : 0); };
    var availableMap = {};
    this.availablePlugins.forEach(function(p) {
        var cur = availableMap[p.fullId];
        if (!cur || rank(p) > rank(cur)) availableMap[p.fullId] = p;
    });

    var html = '<div class="row">';
    plugins.forEach(function(p) {
        // Merge with available plugin data for full info
        var available = availableMap[p.id] || {};
        var pluginData = {
            id: p.id,
            fullId: available.fullId || p.id,
            name: available.name || p.id,
            version: p.version,
            description: available.description || '',
            author: available.author || '',
            repository: p.repository || available.repository,
            repoUrl: available.repoUrl || '',
            tags: available.tags || [],
            enabled: p.enabled,
            configured: !p.needsConfig,
            requestedPermissions: p.requestedPermissions || [],
            pendingPermissions: p.pendingPermissions || [],
            updateAvailable: available.updateAvailable,
            availableVersion: available.version
        };
        html += self._renderPluginCard(pluginData, { isInstalled: true });
    });
    html += '</div>';
    list.innerHTML = html;

    // Check for docs on newly rendered cards
    this.checkPluginDocs();

    // Initialize tooltips
    this._initTooltips();
};

/**
 * Add a new repository
 */
PluginsManager.prototype.addRepository = function() {
    var name = this.elements.repoName.value.trim();
    var url = this.elements.repoUrl.value.trim();

    if (!name || !url) {
        VCLU.showToast('Please fill in all fields', 'warning');
        return;
    }

    fetch('/api/plugins/repositories', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({name: name, url: url})
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast(data.error, 'danger');
        } else {
            location.reload();
        }
    });
};

/**
 * Remove a repository
 * @param {string} name - Repository name
 */
PluginsManager.prototype.removeRepo = function(name) {
    if (!confirm('Remove repository "' + name + '"?')) return;

    fetch('/api/plugins/repositories/' + encodeURIComponent(name), { method: 'DELETE' })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast(data.error, 'danger');
            } else {
                location.reload();
            }
        });
};

/**
 * Switch to a specific tab
 * @param {string} tabName - Tab name ('available' or 'installed')
 */
PluginsManager.prototype.switchTab = function(tabName) {
    // Update tab buttons
    document.querySelectorAll('.plugins-tab[data-tab]').forEach(function(btn) {
        btn.classList.toggle('active', btn.dataset.tab === tabName);
    });

    // Update tab content
    if (this.elements.availableTab) {
        this.elements.availableTab.style.display = tabName === 'available' ? 'block' : 'none';
    }
    if (this.elements.installedTab) {
        this.elements.installedTab.style.display = tabName === 'installed' ? 'block' : 'none';
    }
};

/**
 * Install a plugin
 * @param {string} id - Plugin ID
 * @param {string} repository - Repository name
 */
PluginsManager.prototype.install = function(id, repository) {
    var self = this;
    var shortId = id.replace(/^@[^\/]+\//, '');
    // what the plugin asks for, from its plugin.json, before anything is downloaded
    fetch('/api/plugins/preview?id=' + encodeURIComponent(shortId) + '&repository=' + encodeURIComponent(repository))
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast(data.error, 'danger');
                return;
            }
            self._confirmInstall(id, repository, data);
        })
        .catch(function(err) { VCLU.showToast(err.message, 'danger'); });
};

/**
 * Show every permission, those the plugin asks for in green, and install on confirmation
 * @param {Object} preview - {version, requested, all} from /api/plugins/preview
 */
PluginsManager.prototype._confirmInstall = function(id, repository, preview) {
    var self = this;
    var shortId = id.replace(/^@[^\/]+\//, '');
    var installed = Object.values(this.installedPlugins).filter(function(p) {
        return p.id === id || p.id.replace(/^@[^\/]+\//, '') === shortId && p.repository === repository;
    })[0];
    var had = installed ? (installed.requestedPermissions || []) : [];
    var requested = preview.requested || [];
    var name = (this.availablePlugins || []).filter(function(p) { return p.id === shortId && p.repository === repository; })
        .map(function(p) { return p.name; })[0] || shortId;

    this.elements.installPluginName.textContent = (installed ? 'Update ' : 'Install ') + name + ' v' + (preview.version || '?');
    this.elements.installPermissions.innerHTML = (preview.all || ALL_PERMISSIONS).map(function(perm) {
        var asks = requested.indexOf(perm) >= 0;
        var line = '<li class="mb-1 ' + (asks ? 'text-success fw-semibold' : 'text-muted') + '">' +
                   '<i class="bi ' + (asks ? 'bi-check-circle-fill' : 'bi-circle') + ' me-1"></i>' +
                   VCLU.esc(PERMISSION_LABELS[perm] || perm);
        if (asks && installed && had.indexOf(perm) < 0) {
            line += ' <span class="badge bg-warning text-dark">new</span>';
        }
        if (asks && PERMISSION_WARNINGS[perm]) {
            line += '<div class="small text-danger fw-normal ms-3"><i class="bi bi-exclamation-triangle"></i> ' +
                    VCLU.esc(PERMISSION_WARNINGS[perm]) + ' Off until you allow it in Permissions.</div>';
        }
        return line + '</li>';
    }).join('');

    var notes = [];
    if (requested.length === 0) notes.push('This plugin uses nothing beyond the internet.');
    if (installed) notes.push('An update keeps what you allowed; anything new waits for you to allow it.');
    this.elements.installNote.textContent = notes.join(' ');
    this.elements.installConfirm.textContent = installed ? 'Update' : 'Install';

    var modal = new bootstrap.Modal(this.elements.installModal);
    this.elements.installConfirm.onclick = function() {
        modal.hide();
        self._install(id, repository, requested);
    };
    modal.show();
};

/**
 * Install after the confirmation
 * @param {Array} accept - the permissions the user saw and confirmed
 */
PluginsManager.prototype._install = function(id, repository, accept) {
    var self = this;
    fetch('/api/plugins/install', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        // the Installed tab passes the full id; the repository knows the short one
        body: JSON.stringify({id: id.replace(/^@[^\/]+\//, ''), repository: repository, accept: accept})
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast(data.error, 'danger');
        } else {
            // Installed plugins are keyed by full id; an update keeps config and enabled
            var existing = self.installedPlugins[data.fullId];
            if (existing) {
                existing.version = data.version || existing.version;
            } else {
                self.installedPlugins[data.fullId] = {
                    id: data.fullId,
                    version: data.version || '?',
                    enabled: true,
                    repository: repository,
                    config: {}
                };
            }
            // What the update asks for comes from the server
            self.refreshInstalled();
            // Refresh available will also re-render installed
            self.refreshAvailable();
            self.switchTab('installed');
        }
    });
};

/**
 * Uninstall a plugin
 * @param {string} id - Plugin ID
 */
PluginsManager.prototype.uninstall = function(id) {
    var self = this;
    if (!confirm('Uninstall plugin "' + id + '"?')) return;

    fetch('/api/plugins/' + encodeURIComponent(id), { method: 'DELETE' })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast(data.error, 'danger');
            } else {
                // Update local state and re-render
                delete self.installedPlugins[id];
                // Refresh available will also re-render installed
                self.refreshAvailable();
            }
        });
};

/**
 * Toggle plugin enabled state
 * @param {string} id - Plugin ID
 * @param {boolean} enabled - New enabled state
 */
PluginsManager.prototype.toggle = function(id, enabled) {
    var self = this;
    fetch('/api/plugins/' + encodeURIComponent(id) + '/toggle', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({enabled: enabled})
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast(data.error, 'danger');
        } else {
            // Update local state and re-render
            if (self.installedPlugins[id]) {
                self.installedPlugins[id].enabled = enabled;
            }
            self._renderInstalledPlugins();
        }
    });
};

/**
 * Save what the user allows a plugin and re-render
 * @param {string} id - Plugin ID
 * @param {Object} grants - {lan: {hosts}, mqtt: {subscribe, publish}, modbus: {}, grenton_raw: {hosts},
 *   tftp: {hosts}, serial: {ports}}; a missing key is not allowed
 */
PluginsManager.prototype._saveGrants = function(id, grants, done) {
    var self = this;
    fetch('/api/plugins/' + encodeURIComponent(id) + '/permissions', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(grants)
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast(data.error, 'danger');
            return;
        }
        if (data.warning) VCLU.showToast(data.warning, 'warning');
        var plugin = self.installedPlugins[id];
        if (plugin) {
            plugin.grants = grants;
            plugin.pendingPermissions = data.pendingPermissions || [];
        }
        self._renderInstalledPlugins();
        if (done) done();
    });
};

/**
 * Allow what the plugin asks for and the user has not allowed yet
 * @param {string} id - Plugin ID
 */
PluginsManager.prototype.allowPermissions = function(id) {
    var plugin = this.installedPlugins[id];
    if (!plugin || !plugin.pendingPermissions || plugin.pendingPermissions.length === 0) return;
    var warnings = plugin.pendingPermissions.filter(function(name) { return PERMISSION_WARNINGS[name]; })
        .map(function(name) { return '\n\n' + name + ': ' + PERMISSION_WARNINGS[name]; }).join('');
    if (!confirm('Allow ' + id + ' to use ' + plugin.pendingPermissions.join(', ') + '?' + warnings)) return;
    var grants = Object.assign({}, plugin.grants || {});
    plugin.pendingPermissions.forEach(function(name) { grants[name] = {}; });
    this._saveGrants(id, grants);
};

var PERMISSION_LABELS = {
    lan: 'Local network (HTTP to devices in the house)',
    mqtt: 'MQTT',
    modbus: 'Modbus',
    grenton_raw: 'Grenton frames (UDP to CLUs)',
    tftp: 'TFTP (files on devices)',
    serial: 'Serial port'
};

var ALL_PERMISSIONS = ['lan', 'mqtt', 'modbus', 'grenton_raw', 'tftp', 'serial'];

var PERMISSION_SHORT = {
    lan: 'LAN',
    mqtt: 'MQTT',
    modbus: 'Modbus',
    grenton_raw: 'Grenton frames',
    tftp: 'TFTP',
    serial: 'Serial'
};

// Allowed only by the user, never on install
var PERMISSION_WARNINGS = {
    grenton_raw: 'the plugin can send any command to a CLU it has the key for.',
    tftp: 'the plugin can read and replace files on devices on the network, a CLU\'s too; with Grenton frames that is full control of the CLU.',
    serial: 'the plugin drives whatever is connected to the port you assign.'
};

/**
 * Edit what a plugin may use: each permission it asks for, and lists that narrow it
 * @param {string} id - Plugin ID
 */
PluginsManager.prototype.editPermissions = function(id) {
    var plugin = this.installedPlugins[id];
    if (!plugin) return;
    this.currentPermissionsPlugin = id;
    this.elements.permissionsPluginId.textContent = id;
    // Without grants the plugin has what it asks for, in full, but what needs the user
    var grants = plugin.grants;
    var lines = function(list) { return VCLU.esc((list || []).join('\n')); };
    var area = function(field, label, list, placeholder) {
        return '<label class="form-label small mb-1" for="perm-' + field + '">' + label + '</label>' +
               '<textarea class="form-control form-control-sm mb-2" rows="2" id="perm-' + field + '" ' +
               'placeholder="' + placeholder + '">' + lines(list) + '</textarea>';
    };
    var html = '';
    (plugin.requestedPermissions || []).forEach(function(name) {
        var grant = grants ? grants[name] : (PERMISSION_WARNINGS[name] ? null : {});
        html += '<div class="border rounded p-2 mb-2">' +
                '<div class="form-check mb-1"><input class="form-check-input" type="checkbox" id="perm-' + name + '"' +
                (grant ? ' checked' : '') + '><label class="form-check-label" for="perm-' + name + '">' +
                VCLU.esc(PERMISSION_LABELS[name] || name) + '</label></div>';
        if (PERMISSION_WARNINGS[name]) {
            html += '<div class="small text-danger mb-1"><i class="bi bi-exclamation-triangle"></i> ' +
                    VCLU.esc(PERMISSION_WARNINGS[name]) + '</div>';
        }
        if (name === 'lan') {
            html += area('lan-hosts', 'Only these hosts (IP or prefix, one per line)', grant && grant.hosts, 'any local address');
        } else if (name === 'mqtt') {
            html += area('mqtt-subscribe', 'Subscribe only to (topic filters, + and #)', grant && grant.subscribe, 'any topic');
            html += area('mqtt-publish', 'Publish only on', grant && grant.publish, 'any topic');
        } else if (name === 'grenton_raw' || name === 'tftp') {
            html += area(name + '-hosts', 'Only these hosts (IP or prefix, one per line)', grant && grant.hosts, 'any local address except this vCLU');
        } else if (name === 'serial') {
            html += area('serial-ports', 'Ports (one per line)', grant && grant.ports, 'none: the plugin opens no port until you assign one');
            html += '<div class="small text-muted" id="perm-serial-available"></div>';
        }
        html += '</div>';
    });
    this.elements.permissionsFields.innerHTML = html;
    if ((plugin.requestedPermissions || []).indexOf('serial') >= 0) {
        fetch('/api/plugins/serial-ports')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var el = document.getElementById('perm-serial-available');
                if (el) el.textContent = (data.ports && data.ports.length) ? 'On this machine: ' + data.ports.join(', ') : 'No serial ports found on this machine.';
            });
    }
    new bootstrap.Modal(this.elements.permissionsModal).show();
};

PluginsManager.prototype.savePermissions = function() {
    var self = this;
    var id = this.currentPermissionsPlugin;
    var plugin = this.installedPlugins[id];
    if (!plugin) return;
    var list = function(field) {
        var el = document.getElementById('perm-' + field);
        var items = el ? el.value.split('\n').map(function(s) { return s.trim(); }).filter(Boolean) : [];
        return items.length > 0 ? items : undefined;
    };
    var grants = {};
    (plugin.requestedPermissions || []).forEach(function(name) {
        var box = document.getElementById('perm-' + name);
        if (!box || !box.checked) return;
        if (name === 'lan') {
            grants.lan = {hosts: list('lan-hosts')};
        } else if (name === 'mqtt') {
            grants.mqtt = {subscribe: list('mqtt-subscribe'), publish: list('mqtt-publish')};
        } else if (name === 'grenton_raw' || name === 'tftp') {
            grants[name] = {hosts: list(name + '-hosts')};
        } else if (name === 'serial') {
            grants.serial = {ports: list('serial-ports')};
        } else {
            grants[name] = {};
        }
    });
    this._saveGrants(id, grants, function() {
        var modal = bootstrap.Modal.getInstance(self.elements.permissionsModal);
        if (modal) modal.hide();
    });
};

/**
 * Configure a plugin
 * @param {string} id - Plugin ID
 */
PluginsManager.prototype.configure = function(id) {
    var self = this;
    // a plugin with a form gets the wizard; others the simple form below
    var p = this.installedPlugins[id] || {};
    var listed = (this.availablePlugins || []).filter(function(a) { return a.fullId === id; })[0] || {};
    PluginWizard.open(id, {name: listed.name || p.name, version: p.version}).then(function(opened) {
        if (!opened) self._configureSimple(id);
    }).catch(function(err) {
        VCLU.showToast(err.message, 'danger');
    });
};

PluginsManager.prototype._configureSimple = function(id) {
    var self = this;
    this.currentConfigPlugin = id;
    this.elements.configPluginId.textContent = id;

    var plugin = this.installedPlugins[id];
    var currentConfig = plugin ? plugin.config : {};
    var repository = plugin ? plugin.repository : '';

    this.elements.configFields.innerHTML =
        '<div class="text-center"><div class="spinner-border spinner-border-sm"></div> Loading schema...</div>';
    new bootstrap.Modal(this.elements.configModal).show();

    fetch('/api/plugins/' + encodeURIComponent(id) + '/schema?repository=' + encodeURIComponent(repository))
        .then(function(r) { return r.json(); })
        .then(function(schema) {
            self._renderConfigForm(schema.config || {}, currentConfig);
        })
        .catch(function(err) {
            self._renderSimpleConfigForm(currentConfig);
        });
};

/**
 * Render config form from schema
 * @param {Object} configSchema - Config schema
 * @param {Object} currentConfig - Current config values
 */
PluginsManager.prototype._renderConfigForm = function(configSchema, currentConfig) {
    var html = '<div id="configFieldsList">';

    Object.keys(configSchema).forEach(function(key) {
        var field = configSchema[key];
        var currentValue = currentConfig[key] !== undefined ? currentConfig[key] : (field.default || '');
        var required = field.required ? '<span class="text-danger">*</span>' : '';

        html += '<div class="mb-3 config-field">';
        html += '<label class="form-label">' + key + ' ' + required + '</label>';

        if (field.enum) {
            html += '<select class="form-select form-select-sm config-value" data-key="' + key + '">';
            field.enum.forEach(function(opt) {
                var selected = currentValue === opt ? 'selected' : '';
                html += '<option value="' + opt + '" ' + selected + '>' + opt + '</option>';
            });
            html += '</select>';
        } else if (field.type === 'number') {
            html += '<input type="number" class="form-control form-control-sm config-value" data-key="' +
                   key + '" value="' + currentValue + '">';
        } else if (field.type === 'boolean') {
            var checked = currentValue === true || currentValue === 'true' ? 'checked' : '';
            html += '<div class="form-check"><input type="checkbox" class="form-check-input config-value" data-key="' +
                   key + '" ' + checked + '></div>';
        } else {
            var inputType = key.toLowerCase().includes('password') || key.toLowerCase().includes('apikey') ?
                           'password' : 'text';
            html += '<input type="' + inputType + '" class="form-control form-control-sm config-value" data-key="' +
                   key + '" value="' + currentValue + '">';
        }

        if (field.description) {
            html += '<small class="text-muted">' + field.description + '</small>';
        }
        html += '</div>';
    });

    html += '</div>';
    this.elements.configFields.innerHTML = html;
};

/**
 * Render simple config form (fallback)
 * @param {Object} config - Current config
 */
PluginsManager.prototype._renderSimpleConfigForm = function(config) {
    config = config || {};
    var html = '<div id="configFieldsList">';

    Object.keys(config).forEach(function(key) {
        html += '<div class="mb-2 row g-2 config-field">' +
                '<div class="col-5"><input type="text" class="form-control form-control-sm config-key" value="' +
                key + '" readonly></div>' +
                '<div class="col-5"><input type="text" class="form-control form-control-sm config-value" data-key="' +
                key + '" value="' + config[key] + '"></div></div>';
    });

    html += '</div>';
    this.elements.configFields.innerHTML = html;
};

/**
 * Save plugin configuration
 */
PluginsManager.prototype.saveConfig = function() {
    var self = this;
    var config = {};

    document.querySelectorAll('.config-value').forEach(function(el) {
        var key = el.dataset.key;
        if (!key) return;

        var value;
        if (el.type === 'checkbox') {
            value = el.checked;
        } else if (el.type === 'number') {
            value = el.value !== '' ? Number(el.value) : '';
        } else {
            value = el.value;
            if (value === 'true') value = true;
            else if (value === 'false') value = false;
            else if (!isNaN(value) && value !== '') value = Number(value);
        }
        config[key] = value;
    });

    fetch('/api/plugins/' + encodeURIComponent(this.currentConfigPlugin) + '/config', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({config: config})
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            VCLU.showToast(data.error, 'danger');
        } else {
            // Update local state
            if (self.installedPlugins[self.currentConfigPlugin]) {
                self.installedPlugins[self.currentConfigPlugin].config = config;
            }
            // Close modal
            var modal = bootstrap.Modal.getInstance(self.elements.configModal);
            if (modal) modal.hide();
        }
    });
};

/**
 * Check for plugin documentation
 */
PluginsManager.prototype.checkPluginDocs = function() {
    var self = this;

    document.querySelectorAll('.doc-btn').forEach(function(btn) {
        var pluginId = btn.dataset.pluginId;
        var repository = btn.dataset.repository;

        fetch('/api/plugins/' + encodeURIComponent(pluginId) + '/schema?repository=' + encodeURIComponent(repository))
            .then(function(r) { return r.json(); })
            .then(function(schema) {
                if (schema.docs && schema.docs.length > 0) {
                    self.pluginDocs[pluginId] = { docs: schema.docs, repository: repository };
                    btn.style.display = 'inline-block';
                    btn.onclick = function() { self.showDoc(pluginId, 0); };
                }
            }).catch(function() {});
    });
};

/**
 * Show plugin documentation
 * @param {string} pluginId - Plugin ID
 * @param {number} docIndex - Document index
 */
PluginsManager.prototype.showDoc = function(pluginId, docIndex) {
    var self = this;
    var plugin = this.pluginDocs[pluginId];

    if (!plugin || !plugin.docs || !plugin.docs[docIndex]) return;

    var doc = plugin.docs[docIndex];
    this.elements.docTitle.textContent = doc.title || 'Documentation';
    this.elements.docLoading.style.display = 'block';
    this.elements.docContent.style.display = 'none';
    this.elements.docError.style.display = 'none';

    // Render nav if multiple docs
    if (plugin.docs.length > 1) {
        var navHtml = '';
        plugin.docs.forEach(function(d, i) {
            var active = i === docIndex ? 'btn-primary' : 'btn-outline-secondary';
            navHtml += '<button class="btn btn-sm ' + active + ' me-1" onclick="pluginsManager.showDoc(\'' +
                      pluginId + '\', ' + i + ')">' + d.title + '</button>';
        });
        this.elements.docNav.innerHTML = navHtml;
        this.elements.docNav.style.display = 'block';
    } else {
        this.elements.docNav.style.display = 'none';
    }

    new bootstrap.Modal(this.elements.docModal).show();

    fetch('/api/plugins/' + encodeURIComponent(pluginId) + '/doc?file=' + encodeURIComponent(doc.file) +
          '&repository=' + encodeURIComponent(plugin.repository))
        .then(function(r) { return r.json(); })
        .then(function(data) {
            self.elements.docLoading.style.display = 'none';
            if (data.error) {
                self.elements.docError.textContent = data.error;
                self.elements.docError.style.display = 'block';
            } else {
                self.elements.docContent.innerHTML = marked.parse(data.content || '');
                self.elements.docContent.style.display = 'block';
            }
        })
        .catch(function(err) {
            self.elements.docLoading.style.display = 'none';
            self.elements.docError.textContent = 'Failed to load: ' + err;
            self.elements.docError.style.display = 'block';
        });
};

/**
 * Initialize Bootstrap tooltips on plugin cards
 */
PluginsManager.prototype._initTooltips = function() {
    // Dispose existing tooltips first to prevent duplicates
    var existingTooltips = document.querySelectorAll('[data-bs-toggle="tooltip"]');
    existingTooltips.forEach(function(el) {
        var tooltip = bootstrap.Tooltip.getInstance(el);
        if (tooltip) {
            tooltip.dispose();
        }
    });

    // Initialize new tooltips
    var tooltipTriggerList = document.querySelectorAll('[data-bs-toggle="tooltip"]');
    tooltipTriggerList.forEach(function(el) {
        new bootstrap.Tooltip(el);
    });
};

// Create instance immediately (so inline scripts can populate data)
window.pluginsManager = new PluginsManager();

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    window.pluginsManager.init();
});

// Legacy functions for backwards compatibility with inline onclick
function toggleConfig(btn) {
    if (window.pluginsManager) {
        window.pluginsManager.toggleConfig(btn);
    }
}

function refreshAvailable() {
    if (window.pluginsManager) {
        window.pluginsManager.refreshAvailable();
    }
}

function addRepository() {
    if (window.pluginsManager) {
        window.pluginsManager.addRepository();
    }
}

function removeRepo(name) {
    if (window.pluginsManager) {
        window.pluginsManager.removeRepo(name);
    }
}

function installPlugin(id, repository) {
    if (window.pluginsManager) {
        window.pluginsManager.install(id, repository);
    }
}

function uninstallPlugin(id) {
    if (window.pluginsManager) {
        window.pluginsManager.uninstall(id);
    }
}

function togglePlugin(id, enabled) {
    if (window.pluginsManager) {
        window.pluginsManager.toggle(id, enabled);
    }
}

function configurePlugin(id) {
    if (window.pluginsManager) {
        window.pluginsManager.configure(id);
    }
}

function savePluginConfig() {
    if (window.pluginsManager) {
        window.pluginsManager.saveConfig();
    }
}

function showPluginDoc(pluginId, docIndex) {
    if (window.pluginsManager) {
        window.pluginsManager.showDoc(pluginId, docIndex);
    }
}
