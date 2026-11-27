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

    // Tags: quiet, they only help finding a plugin
    var tagsHtml = '';
    if (tags.length > 0) {
        tagsHtml = '<p class="small text-body-secondary mb-2">' + tags.map(function(t) { return '#' + VCLU.esc(t); }).join(' ') + '</p>';
    }

    // Status badge (for installed plugins)
    var statusBadge = '';
    if (options.isInstalled) {
        if (enabled) {
            statusBadge = '<span class="small text-success text-nowrap"><i class="bi bi-circle-fill me-1" style="font-size:.5rem;vertical-align:middle"></i>enabled</span>';
        } else {
            statusBadge = '<span class="badge bg-warning text-dark">disabled</span>';
        }
        if (plugin.configured === false) {
            statusBadge += ' <span class="badge bg-warning text-dark">not configured</span>';
        }
        if (plugin.sourceMoved) {
            statusBadge += ' <span class="badge bg-warning text-dark" title="' + VCLU.esc(pt('source_moved_title')) + '">' + VCLU.esc(pt('source_moved')) + '</span>';
        }
        if (plugin.usage && plugin.usage.suspended) {
            statusBadge += ' <span class="badge bg-danger" title="' + VCLU.esc(pt('suspended_title')) + '">' + VCLU.esc(pt('suspended')) + '</span>';
        }
        var waitingAll = (plugin.pendingPermissions || []).concat(plugin.pendingActions || []);
        if (waitingAll.length > 0) {
            statusBadge += ' <span class="badge bg-danger" title="' + VCLU.esc(pt('asks_for_title')) + '">' + VCLU.esc(pt('asks_for')) + ' ' +
                           VCLU.esc(waitingAll.join(', ')) + '</span>';
        }
    }

    // Only what the plugin uses, as chips; one on the old API reaches everything anyway
    var permsHtml = '', usageHtml = '';
    if (options.isInstalled) {
        var requested = plugin.requestedPermissions || [];
        var pending = plugin.pendingPermissions || [];
        var chips = [];
        if (plugin.api === 1) {
            chips.push(permChip('danger', 'bi-unlock', pt('api1_short'), pt('api1_note')));
        } else {
            ALL_PERMISSIONS.forEach(function(name) {
                if (name === 'internet') {
                    var hosts = plugin.internet || [], waits = plugin.pendingInternet || [];
                    if (waits.length) chips.push(permChip('warning', PERMISSION_ICONS.internet, pt('title_internet'), pt('asked_not_allowed') + ': ' + waits.join(', ')));
                    else if (hosts.indexOf('*') >= 0) chips.push(permChip('warning', PERMISSION_ICONS.internet, pt('internet_any_short'), internetLabel(hosts) + ': ' + INTERNET_ANY_WARNING));
                    else if (hosts.length) chips.push(permChip('secondary', PERMISSION_ICONS.internet, pt('title_internet'), internetLabel(hosts)));
                    return;
                }
                if (requested.indexOf(name) < 0) return;
                var perm = (plugin.permissions || {})[name];
                if (pending.indexOf(name) >= 0) {
                    chips.push(permChip('warning', PERMISSION_ICONS[name], pt('title_' + name), pt('asked_not_allowed') + ': ' + permissionSentence(name, perm)));
                } else {
                    var wide = (plugin.wide || []).indexOf(name) >= 0;
                    chips.push(permChip(PERMISSION_WARNINGS[name] ? 'danger' : 'secondary', PERMISSION_ICONS[name], pt('title_' + name),
                        pt('allowed') + (wide ? ' (' + pt('wide') + ')' : '') + ': ' + permissionSentence(name, perm)));
                }
            });
            if (!chips.length) chips.push('<span class="small text-body-secondary">' + VCLU.esc(pt('no_permissions')) + '</span>');
        }
        (plugin.actions || []).forEach(function(a) {
            var waits = (plugin.pendingActions || []).indexOf(a.name) >= 0;
            chips.push(permChip(waits ? 'warning' : 'secondary', 'bi-lightning', a.name,
                pt('actions_label') + ': ' + localText(a.description) + (a.confirm ? ' (' + pt('action_confirm') + ')' : '')));
        });
        permsHtml = '<div class="d-flex flex-wrap gap-1 mb-1">' + chips.join('') + '</div>';
        // its time on the run loop, on average since it was loaded
        if (plugin.usage && enabled) {
            usageHtml = '<span class="small text-body-secondary ms-1">' + VCLU.esc(pt('cpu', plugin.usage.cpuMsPerMin)) +
                        (plugin.usage.overruns ? ' · ' + VCLU.esc(pt('overruns', plugin.usage.overruns)) : '') + '</span>';
        }
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
        // Turn on a plugin suspended for going over its time limit
        if (plugin.usage && plugin.usage.suspended) {
            actionBtns += '<button class="btn btn-sm btn-danger me-1" ' +
                         'onclick="pluginsManager.resume(\'' + VCLU.esc(id) + '\')" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="' + VCLU.esc(pt('resume_title')) + '">' +
                         '<i class="bi bi-play-circle"></i> ' + VCLU.esc(pt('resume')) + '</button>';
        }
        // Allow what an update asks for
        if ((plugin.pendingPermissions || []).length + (plugin.pendingActions || []).length > 0) {
            actionBtns += '<button class="btn btn-sm btn-danger me-1" ' +
                         'onclick="pluginsManager.allowPermissions(\'' + VCLU.esc(id) + '\')" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="' + VCLU.esc(pt('allow')) + ': ' +
                         VCLU.esc(plugin.pendingPermissions.join(', ')) + '">' +
                         '<i class="bi bi-shield-check"></i> ' + VCLU.esc(pt('allow')) + '</button>';
        }
        // Permissions (what the plugin may reach on the LAN and MQTT)
        if (plugin.requestedPermissions && plugin.requestedPermissions.length > 0) {
            actionBtns += '<button class="btn btn-sm btn-outline-secondary me-1" ' +
                         'onclick="pluginsManager.editPermissions(\'' + VCLU.esc(id) + '\')" ' +
                         'data-bs-toggle="tooltip" data-bs-placement="top" title="' + VCLU.esc(pt('permissions_button')) + '">' +
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
           ' <span class="small fw-normal text-body-secondary">v' + VCLU.esc(version) + '</span></h6>' +
           '<div class="text-end">' + statusBadge + '</div>' +
           '</div>' +
           '<p class="card-text small text-body-secondary mb-2"><code>' + VCLU.esc(fullId) + '</code> · ' +
           VCLU.esc(repository) + ' · ' + VCLU.esc(author) + '</p>' +
           '<p class="card-text small mb-2 plugin-desc" title="' + VCLU.esc(description) + '">' + VCLU.esc(description) + '</p>' +
           tagsHtml +
           permsHtml +
           '</div>' +
           '<div class="card-footer bg-transparent d-flex justify-content-between align-items-center">' +
           '<div class="d-flex align-items-center">' + linkBtns + usageHtml + '</div>' +
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
            internet: p.internet || [],
            pendingInternet: p.pendingInternet || [],
            permissions: p.permissions || {},
            pending: p.pending || {},
            wide: p.wide || [],
            warnings: p.warnings || [],
            actions: p.actions || [],
            pendingActions: p.pendingActions || [],
            usage: p.usage || null,
            api: p.api || 0,
            updateAvailable: available.updateAvailable,
            sourceMoved: (self.availablePlugins || []).some(function(a) { return a.fullId === p.id && a.sourceMoved; }),
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
            // the files differ from what the confirmation showed: it has to be seen again
            VCLU.showToast(data.code === 'changed_since_preview' ? pt('changed_since_preview') : data.error, 'danger');
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
    var fullId = /^@/.test(id) ? '&fullId=' + encodeURIComponent(id) : '';
    fetch('/api/plugins/preview?id=' + encodeURIComponent(shortId) + '&repository=' + encodeURIComponent(repository) + fullId)
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
 * @param {Object} preview - {version, api, downgradeFrom, needsNewer, requiresVclu, requested, internet, permissions, wide,
 *   warnings, actions, dashboards, seen, all} from /api/plugins/preview
 */
PluginsManager.prototype._confirmInstall = function(id, repository, preview) {
    var self = this;
    var shortId = id.replace(/^@[^\/]+\//, '');
    var installed = Object.values(this.installedPlugins).filter(function(p) {
        return p.id === id || p.id.replace(/^@[^\/]+\//, '') === shortId && p.repository === repository;
    })[0];
    var requested = preview.requested || [];
    var name = (this.availablePlugins || []).filter(function(p) { return p.id === shortId && p.repository === repository; })
        .map(function(p) { return p.name; })[0] || shortId;

    this.elements.installPluginName.textContent = (installed ? pt('update') : pt('install')) + ' ' + name + ' v' + (preview.version || '?');
    var asked = preview.permissions || {};
    var had = installed ? (installed.permissions || {}) : null;
    var all = preview.all || ALL_PERMISSIONS;
    // only what the plugin asks for, one row each; the rest in one line under it
    var rows = all.filter(function(perm) { return requested.indexOf(perm) >= 0; }).map(function(perm) {
        var p = perm === 'internet' && (preview.api || 1) < 2 ? {hosts: ['*']} : (asked[perm] || {});
        var badges = '';
        if (had && asksMore(perm, p, had[perm], installed)) badges += permBadge('primary', pt('new'));
        if ((preview.wide || []).indexOf(perm) >= 0) badges += permBadge('warning', pt('wide'), pt('wide_title'));
        if (PERMISSION_WARNINGS[perm]) badges += permBadge('danger', pt('risky'), pt('risky_title'));
        var why = localText(p.why);
        return '<li class="list-group-item px-3 py-2 d-flex gap-3">' +
               '<i class="bi ' + (PERMISSION_ICONS[perm] || 'bi-shield') + ' fs-5 text-body-secondary"></i>' +
               '<div class="flex-grow-1">' +
               '<div class="d-flex justify-content-between align-items-start gap-2">' +
               '<span class="fw-semibold">' + VCLU.esc(pt('title_' + perm)) + '</span><span class="text-nowrap">' + badges + '</span></div>' +
               '<div class="small">' + VCLU.esc(permissionScope(perm, p)) + '</div>' +
               (why ? '<div class="small text-body-secondary">' + VCLU.esc(pt('why', why)) + '</div>' : '') +
               (PERMISSION_WARNINGS[perm] ? '<div class="small text-danger-emphasis">' +
                   VCLU.esc(PERMISSION_WARNINGS[perm] + ' ' + pt('off_until_allowed')) + '</div>' : '') +
               '</div></li>';
    });
    var html = rows.length ? '<ul class="list-group mb-2">' + rows.join('') + '</ul>' : '';
    var unused = all.filter(function(perm) { return requested.indexOf(perm) < 0; });
    if (unused.length) {
        html += '<p class="small text-body-secondary mb-2">' + VCLU.esc(pt(rows.length ? 'not_using' : 'uses_none_of',
            unused.map(function(perm) { return pt('title_' + perm); }).join(', '))) + '</p>';
    }
    // combinations: reading the house and reaching the internet, full control of a CLU
    var hosts = (preview.internet || []).indexOf('*') >= 0 ? pt('any_server') : (preview.internet || []).join(', ');
    if ((preview.warnings || []).length > 0) {
        html += '<div class="alert alert-warning small py-2 px-3 mb-2 d-flex gap-2"><i class="bi bi-exclamation-triangle"></i><div>' +
            preview.warnings.map(function(w) { return VCLU.esc(pt('warn_' + w, hosts)); }).join('<br>') + '</div></div>';
    }
    if ((preview.actions || []).length > 0) {
        html += '<div class="small fw-semibold mt-3 mb-1">' + VCLU.esc(pt('actions_title')) + '</div><ul class="list-unstyled small mb-2">' +
            preview.actions.map(function(a) {
                var isNew = installed && !(installed.actions || []).some(function(x) { return x.name === a.name; });
                return '<li class="mb-1"><code>' + VCLU.esc(a.name) + '</code> ' + VCLU.esc(localText(a.description)) +
                       (a.confirm ? permBadge('secondary', pt('action_confirm')) : '') +
                       (isNew ? permBadge('primary', pt('new')) : '') + '</li>';
            }).join('') + '</ul>';
    }
    if ((preview.dashboards || []).length > 0) {
        html += '<div class="small fw-semibold mt-3 mb-1">' + VCLU.esc(pt('dashboards_title')) + '</div><ul class="list-unstyled small mb-2">' +
            preview.dashboards.map(function(d) {
                return '<li class="mb-1"><i class="bi bi-grid-1x2 me-1 text-body-secondary"></i>' + VCLU.esc(localText(d.name) || d.id) + '</li>';
            }).join('') + '</ul>';
    }
    this.elements.installPermissions.innerHTML = html;

    var notes = [];
    if (installed) notes.push(pt('update_keeps'));
    if ((preview.api || 1) < 2) notes.push(pt('api1_note'));
    // a version on a lower plugin API would drop its limits: installed only from scratch
    var downgrade = installed && preview.downgradeFrom > 0 || preview.needsNewer;
    if (installed && preview.downgradeFrom > 0) notes = [pt('api_downgrade', preview.downgradeFrom, preview.api || 1)];
    if (preview.needsNewer) notes = [pt('needs_newer', preview.requiresVclu || '')];
    this.elements.installNote.textContent = notes.join(' ');
    this.elements.installNote.classList.toggle('text-danger', !!downgrade);
    this.elements.installNote.classList.toggle('text-muted', !downgrade);
    this.elements.installConfirm.textContent = installed ? pt('update') : pt('install');
    this.elements.installConfirm.hidden = !!downgrade;

    var modal = new bootstrap.Modal(this.elements.installModal);
    this.elements.installConfirm.onclick = function() {
        modal.hide();
        // what this dialog showed goes back: the grant gets only that, the rest waits
        self._install(id, repository, requested, preview.seen || {api: preview.api || 1});
    };
    modal.show();
};

/**
 * Install after the confirmation
 * @param {Array} accept - the permissions the user saw and confirmed
 * @param {Object} seen - the hosts and ports shown with them
 */
PluginsManager.prototype._install = function(id, repository, accept, seen) {
    var self = this;
    fetch('/api/plugins/install', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        // the Installed tab passes the full id; the repository knows the short one
        body: JSON.stringify({id: id.replace(/^@[^\/]+\//, ''), repository: repository, accept: accept, seen: seen})
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            // the files differ from what the confirmation showed: it has to be seen again
            VCLU.showToast(data.code === 'changed_since_preview' ? pt('changed_since_preview') : data.error, 'danger');
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
/**
 * Turn on a plugin suspended for going over its time limit
 * @param {string} id - Plugin ID
 */
PluginsManager.prototype.resume = function(id) {
    var self = this;
    fetch('/api/plugins/' + encodeURIComponent(id) + '/resume', {method: 'POST'})
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast(data.error, 'danger');
                return;
            }
            if (data.warning) VCLU.showToast(data.warning, 'warning');
            var plugin = self.installedPlugins[id];
            if (plugin && plugin.usage) plugin.usage.suspended = false;
            self._renderInstalledPlugins();
        });
};

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
            // the files differ from what the confirmation showed: it has to be seen again
            VCLU.showToast(data.code === 'changed_since_preview' ? pt('changed_since_preview') : data.error, 'danger');
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
 * @param {Object} grants - {internet: {hosts}, lan: {hosts, ports | anyPort}, udp: {hosts, ports, broadcast}, tcp: {hosts, ports},
 *   mqtt: {subscribe, publish}, modbus: {hosts}, clu: {hosts, discovery}, tftp: {hosts}, serial: {ports}, actions: [names]};
 *   a missing key is not allowed
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
            // the files differ from what the confirmation showed: it has to be seen again
            VCLU.showToast(data.code === 'changed_since_preview' ? pt('changed_since_preview') : data.error, 'danger');
            return;
        }
        if (data.warning) VCLU.showToast(data.warning, 'warning');
        var plugin = self.installedPlugins[id];
        if (plugin) {
            plugin.grants = grants;
            plugin.pendingPermissions = data.pendingPermissions || [];
            plugin.pendingInternet = data.pendingInternet || [];
            plugin.pending = data.pending || {};
            plugin.pendingActions = data.pendingActions || [];
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
    if (!plugin) return;
    var pendingNames = plugin.pendingPermissions || [];
    var pendingActions = plugin.pendingActions || [];
    if (pendingNames.length === 0 && pendingActions.length === 0) return;
    var warnings = pendingNames.filter(function(name) { return PERMISSION_WARNINGS[name]; })
        .map(function(name) { return '\n\n' + name + ': ' + PERMISSION_WARNINGS[name]; }).join('');
    var pendingHosts = plugin.pendingInternet || [];
    if (pendingHosts.length > 0) {
        warnings += '\n\n' + internetLabel(pendingHosts) + (pendingHosts.indexOf('*') >= 0 ? ': ' + INTERNET_ANY_WARNING : '');
    }
    pendingNames.forEach(function(name) {
        if (name !== 'internet') warnings += '\n\n' + permissionSentence(name, (plugin.permissions || {})[name]);
    });
    if (pendingActions.length > 0) warnings += '\n\n' + pt('actions_waiting', pendingActions.join(', '));
    if (!confirm(pt('allow_confirm', id, pendingNames.concat(pendingActions).join(', ')) + warnings)) return;
    var grants = Object.assign({}, plugin.grants || {});
    pendingNames.forEach(function(name) {
        // what it asks for now, the hosts the user narrowed it to kept
        grants[name] = grantFor(name, plugin, grants[name]);
    });
    grants.actions = (plugin.actions || []).map(function(a) { return a.name; });
    this._saveGrants(id, grants);
};

// Texts from web/locales (plugins.perm), rendered into PLUGINS_I18N by plugins.html;
// {0}, {1} take the arguments
function pt(key) {
    var s = (window.PLUGINS_I18N && PLUGINS_I18N[key]) || key;
    var args = arguments;
    return s.replace(/\{(\d)\}/g, function(m, n) { return args[+n + 1] !== undefined ? args[+n + 1] : m; });
}

var PERMISSION_LABELS = {
    internet: pt('label_internet'),
    lan: pt('label_lan'),
    udp: pt('label_udp'),
    tcp: pt('label_tcp'),
    mqtt: pt('label_mqtt'),
    modbus: pt('label_modbus'),
    clu: pt('label_clu'),
    tftp: pt('label_tftp'),
    serial: pt('label_serial')
};

var ALL_PERMISSIONS = ['internet', 'lan', 'udp', 'tcp', 'mqtt', 'modbus', 'clu', 'tftp', 'serial'];

// Allowed only by the user, never on install
var PERMISSION_WARNINGS = {
    clu: pt('warn_clu'),
    tftp: pt('warn_tftp'),
    serial: pt('warn_serial')
};

// "internet": {"hosts": ["*"]} (any host) is allowed on install like a list, with this in front of it
var INTERNET_ANY_WARNING = pt('warn_internet_any');

// A text of plugin.json: a string or {pl, en}, in the panel's language
function localText(t) {
    if (!t) return '';
    if (typeof t === 'string') return t;
    var lang = (window.PLUGINS_I18N && PLUGINS_I18N.lang) || 'pl';
    return t[lang] || t.pl || t.en || '';
}

var PERMISSION_ICONS = {
    internet: 'bi-globe', lan: 'bi-house', udp: 'bi-broadcast', tcp: 'bi-terminal', mqtt: 'bi-chat-square-dots',
    modbus: 'bi-diagram-3', clu: 'bi-cpu', tftp: 'bi-file-earmark-arrow-up', serial: 'bi-usb-plug'
};

// One badge style for the install dialog: new (primary), wide (warning), risky (danger)
function permBadge(color, text, title) {
    return ' <span class="badge rounded-pill bg-' + color + '-subtle text-' + color + '-emphasis border border-' + color + '-subtle fw-normal"' +
           (title ? ' title="' + VCLU.esc(title) + '"' : '') + '>' + VCLU.esc(text) + '</span>';
}

// A permission on the plugin card: icon and short name, the details in the tooltip
function permChip(color, icon, text, title) {
    return '<span class="badge rounded-pill bg-' + color + '-subtle text-' + color + '-emphasis border border-' + color + '-subtle fw-normal"' +
           ' title="' + VCLU.esc(title) + '"><i class="bi ' + icon + ' me-1"></i>' + VCLU.esc(text) + '</span>';
}

// What a permission reaches, as plugin.json declares it, without its name and why
function permissionScope(name, perm) {
    var parts = [];
    var desc = pt('desc_' + name);
    if (desc !== 'desc_' + name) parts.push(desc);
    if (name === 'internet') {
        parts.push((perm.hosts || []).indexOf('*') >= 0 ? pt('any_server_cap') : (perm.hosts || []).join(', '));
    }
    if (name === 'lan') parts.push(perm.ports ? pt('tcp_ports', perm.ports.join(', ')) : pt('lan_any_port'));
    if (name === 'udp' || name === 'tcp') parts.push(pt('tcp_ports', (perm.ports || []).join(', ')));
    if (name === 'udp' && perm.broadcast && perm.broadcast.length) parts.push(pt('udp_broadcast_to', perm.broadcast.join(', ')));
    if (name === 'mqtt') {
        if (!perm.subscribe && !perm.publish) parts.push(pt('mqtt_any'));
        if (perm.subscribe && perm.subscribe.length) parts.push(pt('mqtt_reads', perm.subscribe.join(', ')));
        if (perm.publish && perm.publish.length) parts.push(pt('mqtt_publishes_cap', perm.publish.join(', ')));
    }
    return parts.join(' · ');
}

// One sentence for a permission as plugin.json declares it (perm: its object), with why
function permissionSentence(name, perm) {
    perm = perm || {};
    var s = PERMISSION_LABELS[name] || name;
    if (name === 'internet') s = internetLabel(perm.hosts || []);
    if (name === 'lan') s += '. ' + (perm.ports ? pt('tcp_ports', perm.ports.join(', ')) : pt('lan_any_port'));
    if (name === 'udp') {
        s += '. ' + pt('tcp_ports', (perm.ports || []).join(', '));
        if (perm.broadcast && perm.broadcast.length) s += '. ' + pt('udp_broadcast_to', perm.broadcast.join(', '));
    }
    if (name === 'tcp') s += '. ' + pt('tcp_ports', (perm.ports || []).join(', '));
    if (name === 'mqtt') {
        if (!perm.subscribe && !perm.publish) {
            s += '. ' + pt('mqtt_any');
        } else {
            var parts = [];
            if (perm.subscribe && perm.subscribe.length) parts.push(pt('mqtt_reads', perm.subscribe.join(', ')));
            if (perm.publish && perm.publish.length) parts.push(pt('mqtt_publishes', perm.publish.join(', ')));
            s += '. ' + parts.join('; ');
        }
    }
    var why = localText(perm.why);
    return why ? s + '. ' + pt('why', why) : s;
}

// The update asks for more of a permission than the installed version: a new one, a host, a
// port, a topic filter, or no list where there was one
function asksMore(name, asked, had, installed) {
    if (!had) return true;
    asked = asked || {};
    var more = function(a, b) { return (a || []).some(function(x) { return (b || []).indexOf(x) < 0; }); };
    if (name === 'internet') return (installed.internet || []).indexOf('*') < 0 && more(asked.hosts, had.hosts);
    if (name === 'lan') return had.ports && (!asked.ports || more(asked.ports, had.ports));
    if (name === 'udp') return more(asked.ports, had.ports) || more(asked.broadcast, had.broadcast);
    if (name === 'tcp') return more(asked.ports, had.ports);
    if (name === 'mqtt') return (had.subscribe || had.publish) && (!asked.subscribe && !asked.publish ||
        more(asked.subscribe, had.subscribe) || more(asked.publish, had.publish));
    return false;
}

// A grant for permission name as the plugin asks for it now, keeping the hosts the user set
function grantFor(name, plugin, previous) {
    var perm = (plugin.permissions || {})[name] || {};
    var g = previous ? Object.assign({}, previous) : {};
    if (name === 'lan') {
        if (perm.ports) { g.ports = perm.ports.slice(); delete g.anyPort; } else { g.anyPort = true; delete g.ports; }
    } else if (name === 'udp') {
        g.ports = (perm.ports || []).slice(); g.broadcast = (perm.broadcast || []).slice();
    } else if (name === 'tcp') {
        g.ports = (perm.ports || []).slice();
    } else if (name === 'mqtt') {
        var any = !perm.subscribe && !perm.publish;
        g.subscribe = any ? ['#'] : (perm.subscribe || []).slice();
        g.publish = any ? ['#'] : (perm.publish || []).slice();
    } else if (name === 'internet') {
        g = {hosts: plugin.internet || []};
    }
    return g;
}

// The ports a permission lists, those an update added and not allowed yet marked
function portsHtml(plugin, name) {
    var perm = (plugin.permissions || {})[name] || {};
    var waiting = (plugin.pending || {})[name] || {};
    var lists = name === 'udp' ? [['ports', 'tcp_ports'], ['broadcast', 'udp_broadcast_to']] : [['ports', 'tcp_ports']];
    var html = '';
    lists.forEach(function(l) {
        var list = perm[l[0]] || [];
        if (list.length === 0) return;
        var w = waiting[l[0]] || [];
        html += '<div class="small mb-1">' + VCLU.esc(pt(l[1], '')) + list.map(function(port) {
            var waits = w.indexOf(port) >= 0;
            return '<span class="badge ' + (waits ? 'bg-warning text-dark' : 'bg-secondary') + ' ms-1"' +
                   (waits ? ' title="' + VCLU.esc(pt('not_allowed_yet')) + '"' : '') + '>' + port + '</span>';
        }).join('') + '</div>';
        if (w.length > 0) {
            html += '<div class="small text-muted mb-1">' + VCLU.esc(pt('ports_waiting', w.join(', '))) + ' ' +
                    VCLU.esc(pt('internet_save_allows')) + '</div>';
        }
    });
    if (name === 'lan' && !perm.ports) {
        html += '<div class="small mb-1">' + VCLU.esc(pt('lan_any_port')) + ' <span class="badge bg-warning text-dark">' + VCLU.esc(pt('wide')) + '</span></div>';
        if ((plugin.pendingPermissions || []).indexOf('lan') >= 0 && !waiting.ports) {
            html += '<div class="small text-muted mb-1">' + VCLU.esc(pt('any_port_waiting')) + ' ' + VCLU.esc(pt('internet_save_allows')) + '</div>';
        }
    }
    return html;
}

function internetLabel(hosts) {
    if (hosts.indexOf('*') >= 0) return pt('internet_any');
    return pt('internet_connects_to', hosts.join(', '));
}

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
        return '<label class="form-label small mb-1" for="perm-' + field + '">' + VCLU.esc(label) + '</label>' +
               '<textarea class="form-control form-control-sm mb-2" rows="2" id="perm-' + field + '" ' +
               'placeholder="' + VCLU.esc(placeholder) + '">' + lines(list) + '</textarea>';
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
        if (name === 'internet') {
            var pendingHosts = plugin.pendingInternet || [];
            html += '<div class="small mb-1">' + (plugin.internet || []).map(function(h) {
                var waits = pendingHosts.indexOf(h) >= 0 || pendingHosts.indexOf('*') >= 0 && h === '*';
                return '<span class="badge ' + (waits ? 'bg-warning text-dark' : 'bg-secondary') + ' me-1"' +
                       (waits ? ' title="' + VCLU.esc(pt('not_allowed_yet')) + '"' : '') + '>' + VCLU.esc(h === '*' ? pt('any_server') : h) + '</span>';
            }).join('') + '</div>';
            if ((plugin.internet || []).indexOf('*') >= 0) {
                html += '<div class="small text-danger mb-1"><i class="bi bi-exclamation-triangle"></i> ' +
                        VCLU.esc(INTERNET_ANY_WARNING) + '</div>';
            }
            if (pendingHosts.length > 0) {
                html += '<div class="small text-muted">' + VCLU.esc(pt('internet_save_allows')) + '</div>';
            }
        } else if (name === 'lan') {
            html += portsHtml(plugin, name);
            html += area('lan-hosts', pt('hosts_only'), grant && grant.hosts, pt('any_local'));
        } else if (name === 'mqtt') {
            var asked = (plugin.permissions || {}).mqtt || {};
            if (asked.subscribe || asked.publish) html += '<div class="small mb-1">' + VCLU.esc(permissionSentence('mqtt', asked)) + '</div>';
            html += area('mqtt-subscribe', pt('subscribe_only'), grant && grant.subscribe, (asked.subscribe || [pt('any_topic')]).join(', '));
            html += area('mqtt-publish', pt('publish_only'), grant && grant.publish, (asked.publish || [pt('any_topic')]).join(', '));
        } else if (name === 'modbus') {
            html += area('modbus-hosts', pt('modbus_hosts'), grant && grant.hosts, pt('any_local_but_vclu'));
        } else if (name === 'udp' || name === 'tcp' || name === 'clu' || name === 'tftp') {
            html += portsHtml(plugin, name);
            html += area(name + '-hosts', pt('hosts_only'), grant && grant.hosts, pt('any_local_but_vclu'));
            if (name === 'clu') {
                html += '<div class="form-check small"><input class="form-check-input" type="checkbox" id="perm-clu-discovery"' +
                        (grant && grant.discovery ? ' checked' : '') + '><label class="form-check-label" for="perm-clu-discovery">' +
                        VCLU.esc(pt('clu_discovery')) + '</label></div>';
            }
        } else if (name === 'serial') {
            html += area('serial-ports', pt('ports'), grant && grant.ports, pt('ports_none'));
            html += '<div class="small text-muted" id="perm-serial-available"></div>';
        }
        var why = localText(((plugin.permissions || {})[name] || {}).why);
        if (why) html += '<div class="small text-body-secondary">' + VCLU.esc(pt('why', why)) + '</div>';
        html += '</div>';
    });
    if ((plugin.actions || []).length > 0) {
        var allowedActions = grants && grants.actions ? grants.actions : null;
        html += '<div class="border rounded p-2 mb-2"><div class="mb-1">' + VCLU.esc(pt('actions_label')) + '</div>' +
                plugin.actions.map(function(a) {
                    var on = allowedActions ? allowedActions.indexOf(a.name) >= 0 : true;
                    var waits = (plugin.pendingActions || []).indexOf(a.name) >= 0;
                    return '<div class="form-check small"><input class="form-check-input perm-action" type="checkbox" data-action="' +
                           VCLU.esc(a.name) + '" id="perm-action-' + VCLU.esc(a.name) + '"' + (on || waits ? ' checked' : '') + '>' +
                           '<label class="form-check-label" for="perm-action-' + VCLU.esc(a.name) + '"><code>' + VCLU.esc(a.name) + '</code> ' +
                           VCLU.esc(localText(a.description)) + (a.confirm ? ' (' + VCLU.esc(pt('action_confirm')) + ')' : '') +
                           (waits ? ' <span class="badge bg-warning text-dark">' + VCLU.esc(pt('not_allowed_yet')) + '</span>' : '') +
                           '</label></div>';
                }).join('') + '</div>';
    }
    this.elements.permissionsFields.innerHTML = html;
    if ((plugin.requestedPermissions || []).indexOf('serial') >= 0) {
        fetch('/api/plugins/serial-ports')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var el = document.getElementById('perm-serial-available');
                if (el) el.textContent = (data.ports && data.ports.length) ? pt('ports_here', data.ports.join(', ')) : pt('ports_none_here');
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
        if (name === 'internet') {
            grants.internet = {hosts: plugin.internet || []};
        } else if (name === 'lan') {
            grants.lan = grantFor('lan', plugin, {hosts: list('lan-hosts')});
        } else if (name === 'mqtt') {
            // empty: what plugin.json declares; the server keeps a grant within it
            var mq = grantFor('mqtt', plugin, null);
            grants.mqtt = {subscribe: list('mqtt-subscribe') || mq.subscribe, publish: list('mqtt-publish') || mq.publish};
        } else if (name === 'modbus') {
            grants.modbus = {hosts: list('modbus-hosts')};
        } else if (name === 'udp' || name === 'tcp' || name === 'tftp') {
            grants[name] = grantFor(name, plugin, {hosts: list(name + '-hosts')});
        } else if (name === 'clu') {
            var disc = document.getElementById('perm-clu-discovery');
            grants.clu = {hosts: list('clu-hosts'), discovery: !!(disc && disc.checked)};
        } else if (name === 'serial') {
            grants.serial = {ports: list('serial-ports')};
        } else {
            grants[name] = {};
        }
    });
    if ((plugin.actions || []).length > 0) {
        grants.actions = Array.prototype.slice.call(document.querySelectorAll('.perm-action'))
            .filter(function(b) { return b.checked; }).map(function(b) { return b.getAttribute('data-action'); });
    }
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

    // plugin.json comes from the repository: every text of it is escaped
    Object.keys(configSchema).forEach(function(rawKey) {
        var field = configSchema[rawKey] || {};
        var key = VCLU.esc(rawKey);
        var currentValue = currentConfig[rawKey] !== undefined ? currentConfig[rawKey] : (field.default || '');
        var value = VCLU.esc(String(currentValue));
        var required = field.required ? '<span class="text-danger">*</span>' : '';

        html += '<div class="mb-3 config-field">';
        html += '<label class="form-label">' + key + ' ' + required + '</label>';

        if (Array.isArray(field.enum)) {
            html += '<select class="form-select form-select-sm config-value" data-key="' + key + '">';
            field.enum.forEach(function(opt) {
                var selected = currentValue === opt ? 'selected' : '';
                html += '<option value="' + VCLU.esc(String(opt)) + '" ' + selected + '>' + VCLU.esc(String(opt)) + '</option>';
            });
            html += '</select>';
        } else if (field.type === 'number') {
            html += '<input type="number" class="form-control form-control-sm config-value" data-key="' +
                   key + '" value="' + value + '">';
        } else if (field.type === 'boolean') {
            var checked = currentValue === true || currentValue === 'true' ? 'checked' : '';
            html += '<div class="form-check"><input type="checkbox" class="form-check-input config-value" data-key="' +
                   key + '" ' + checked + '></div>';
        } else {
            var inputType = rawKey.toLowerCase().includes('password') || rawKey.toLowerCase().includes('apikey') ?
                           'password' : 'text';
            html += '<input type="' + inputType + '" class="form-control form-control-sm config-value" data-key="' +
                   key + '" value="' + value + '">';
        }

        if (field.description) {
            html += '<small class="text-muted">' + VCLU.esc(localText(field.description)) + '</small>';
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

    Object.keys(config).forEach(function(rawKey) {
        var key = VCLU.esc(rawKey), value = VCLU.esc(String(config[rawKey]));
        html += '<div class="mb-2 row g-2 config-field">' +
                '<div class="col-5"><input type="text" class="form-control form-control-sm config-key" value="' +
                key + '" readonly></div>' +
                '<div class="col-5"><input type="text" class="form-control form-control-sm config-value" data-key="' +
                key + '" value="' + value + '"></div></div>';
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
            // the files differ from what the confirmation showed: it has to be seen again
            VCLU.showToast(data.code === 'changed_since_preview' ? pt('changed_since_preview') : data.error, 'danger');
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

// A repository's documentation is shown as text and plain formatting only: marked keeps the
// HTML a markdown file carries, and nothing from a repository runs on this page.
var DOC_TAGS = {a: 1, p: 1, br: 1, hr: 1, h1: 1, h2: 1, h3: 1, h4: 1, h5: 1, h6: 1, strong: 1, b: 1, em: 1, i: 1,
    u: 1, s: 1, del: 1, sup: 1, sub: 1, code: 1, pre: 1, kbd: 1, blockquote: 1, ul: 1, ol: 1, li: 1,
    table: 1, thead: 1, tbody: 1, tr: 1, th: 1, td: 1, img: 1};
var DOC_ATTRS = {a: ['href', 'title'], img: ['src', 'alt', 'title'], th: ['align'], td: ['align'], ol: ['start']};
// what a link or an image may point at: http(s), mail, a fragment or a relative path
var DOC_URL = /^(?:https?:|mailto:|#|\.{0,2}\/|[^:]*$)/i;
var DOC_DROP = {script: 1, style: 1, iframe: 1, object: 1, embed: 1, svg: 1, math: 1, template: 1, noscript: 1, frame: 1, frameset: 1};

function sanitizeDocNode(node) {
    Array.prototype.slice.call(node.childNodes).forEach(function(child) {
        if (child.nodeType === 3) return;
        if (child.nodeType !== 1) { child.remove(); return; }
        var tag = child.tagName.toLowerCase();
        if (DOC_DROP[tag]) { child.remove(); return; }
        sanitizeDocNode(child);
        if (!DOC_TAGS[tag]) {
            // an element not listed goes, its text stays
            while (child.firstChild) node.insertBefore(child.firstChild, child);
            child.remove();
            return;
        }
        Array.prototype.slice.call(child.attributes).forEach(function(attr) {
            var name = attr.name.toLowerCase();
            var ok = (DOC_ATTRS[tag] || []).indexOf(name) >= 0 &&
                (name !== 'href' && name !== 'src' || DOC_URL.test(attr.value.trim()));
            if (!ok) child.removeAttribute(attr.name);
        });
        if (tag === 'a') {
            child.setAttribute('target', '_blank');
            child.setAttribute('rel', 'noopener noreferrer');
        }
    });
}

// showDocHTML puts the rendered markdown into el, sanitized; the nodes move over without
// another parse of their text.
function showDocHTML(el, html) {
    var parsed = new DOMParser().parseFromString(html, 'text/html');
    sanitizeDocNode(parsed.body);
    el.textContent = '';
    while (parsed.body.firstChild) {
        el.appendChild(document.adoptNode(parsed.body.firstChild));
    }
}

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
        this.elements.docNav.textContent = '';
        plugin.docs.forEach(function(d, i) {
            var active = i === docIndex ? 'btn-primary' : 'btn-outline-secondary';
            var navBtn = document.createElement('button');
            navBtn.className = 'btn btn-sm ' + active + ' me-1';
            navBtn.textContent = d.title || ('#' + (i + 1));
            navBtn.onclick = function() { self.showDoc(pluginId, i); };
            self.elements.docNav.appendChild(navBtn);
        });
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
                showDocHTML(self.elements.docContent, marked.parse(data.content || ''));
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
