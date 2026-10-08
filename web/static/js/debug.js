/**
 * DebugConsole - Real-time log viewer with filtering
 * Uses prototype pattern to avoid polluting window object
 * @param {Object} options - Configuration options
 */
function DebugConsole(options) {
    this.options = options || {};
    this.eventSource = null;
    this.paused = false;
    this.autoScroll = true;
    this.maxEntries = this.options.maxEntries || 500;
    this.sources = new Set();
    this.reconnectAttempts = 0;
    this.maxReconnectAttempts = 10;
    this.reconnectDelay = 1000;

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize the debug console
 */
DebugConsole.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        container: document.getElementById('logContainer'),
        searchInput: document.getElementById('searchFilter'),
        levelSelect: document.getElementById('levelFilter'),
        sourceSelect: document.getElementById('sourceFilter'),
        pauseBtn: document.getElementById('pauseBtn'),
        clearBtn: document.getElementById('clearBtn'),
        downloadBtn: document.getElementById('downloadBtn'),
        autoScrollCb: document.getElementById('autoScrollCb'),
        countBadge: document.getElementById('logCount'),
        connectionStatus: document.getElementById('connectionStatus')
    };

    // Bind events
    if (this.elements.searchInput) {
        this.elements.searchInput.addEventListener('input', function() {
            self.applyFilters();
        });
    }

    if (this.elements.levelSelect) {
        this.elements.levelSelect.addEventListener('change', function() {
            self.applyFilters();
        });
    }

    if (this.elements.sourceSelect) {
        this.elements.sourceSelect.addEventListener('change', function() {
            self.applyFilters();
        });
    }

    if (this.elements.pauseBtn) {
        this.elements.pauseBtn.addEventListener('click', function() {
            self.togglePause();
        });
    }

    if (this.elements.clearBtn) {
        this.elements.clearBtn.addEventListener('click', function() {
            self.clear();
        });
    }

    if (this.elements.downloadBtn) {
        this.elements.downloadBtn.addEventListener('click', function() {
            self.downloadLogs();
        });
    }

    if (this.elements.autoScrollCb) {
        this.elements.autoScrollCb.checked = this.autoScroll;
        this.elements.autoScrollCb.addEventListener('change', function() {
            self.autoScroll = this.checked;
        });
    }

    // Connect SSE
    this.connect();
};

/**
 * Connect to SSE stream
 */
DebugConsole.prototype.connect = function() {
    var self = this;

    if (this.eventSource) {
        this.eventSource.close();
    }

    this.setConnectionStatus('connecting');
    this.eventSource = new EventSource('/api/debug/stream');

    this.eventSource.onopen = function() {
        self.reconnectAttempts = 0;
        self.setConnectionStatus('connected');
    };

    this.eventSource.onmessage = function(event) {
        try {
            var log = JSON.parse(event.data);
            if (!self.paused) {
                self.appendLog(log);
            }
        } catch (e) {
            console.error('Failed to parse log entry:', e);
        }
    };

    this.eventSource.onerror = function() {
        self.setConnectionStatus('disconnected');

        // Auto-reconnect with backoff
        if (self.reconnectAttempts < self.maxReconnectAttempts) {
            self.reconnectAttempts++;
            var delay = self.reconnectDelay * Math.pow(2, self.reconnectAttempts - 1);
            setTimeout(function() {
                self.connect();
            }, Math.min(delay, 30000));
        }
    };
};

/**
 * Disconnect from SSE stream
 */
DebugConsole.prototype.disconnect = function() {
    if (this.eventSource) {
        this.eventSource.close();
        this.eventSource = null;
    }
    this.setConnectionStatus('disconnected');
};

/**
 * Set connection status indicator
 * @param {string} status - 'connecting', 'connected', or 'disconnected'
 */
DebugConsole.prototype.setConnectionStatus = function(status) {
    if (!this.elements.connectionStatus) return;

    var badge = this.elements.connectionStatus;
    badge.className = 'badge';

    switch (status) {
        case 'connected':
            badge.classList.add('bg-success');
            badge.innerHTML = '<i class="bi bi-wifi"></i> Connected';
            break;
        case 'connecting':
            badge.classList.add('bg-warning');
            badge.innerHTML = '<i class="bi bi-hourglass-split"></i> Connecting...';
            break;
        case 'disconnected':
            badge.classList.add('bg-danger');
            badge.innerHTML = '<i class="bi bi-wifi-off"></i> Disconnected';
            break;
    }
};

/**
 * Append a log entry to the container
 * @param {Object} entry - Log entry object
 */
DebugConsole.prototype.appendLog = function(entry) {
    // Add source to set and update dropdown
    if (entry.source && !this.sources.has(entry.source)) {
        this.sources.add(entry.source);
        this._updateSourceDropdown();
    }

    // Create log element
    var div = document.createElement('div');
    div.className = 'log-entry log-' + entry.level;
    div.dataset.level = entry.level;
    div.dataset.source = entry.source || '';
    div.dataset.id = entry.id;
    div.innerHTML = this._formatEntry(entry);

    this.elements.container.appendChild(div);

    // Limit entries
    while (this.elements.container.children.length > this.maxEntries) {
        this.elements.container.removeChild(this.elements.container.firstChild);
    }

    // Apply current filter
    this._applyFilterToEntry(div);

    // Auto-scroll
    if (this.autoScroll) {
        this.elements.container.scrollTop = this.elements.container.scrollHeight;
    }

    // Update count
    this._updateCount();
};

/**
 * Format a log entry for display
 * @param {Object} entry - Log entry object
 * @returns {string} HTML string
 */
DebugConsole.prototype._formatEntry = function(entry) {
    var time = new Date(entry.time);
    var timeStr = time.toLocaleTimeString('pl-PL', {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit'
    });

    var sourceHtml = entry.source
        ? '<span class="log-source">[' + VCLU.esc(entry.source) + ']</span> '
        : '';

    return '<span class="log-time">' + timeStr + '</span> ' +
           sourceHtml +
           '<span class="log-message">' + VCLU.esc(entry.message) + '</span>';
};

/**
 * Apply all filters to displayed entries
 */
DebugConsole.prototype.applyFilters = function() {
    var entries = this.elements.container.querySelectorAll('.log-entry');
    var self = this;
    entries.forEach(function(entry) {
        self._applyFilterToEntry(entry);
    });
    this._updateCount();
};

/**
 * Apply filters to a single entry
 * @param {HTMLElement} entry - Log entry element
 */
DebugConsole.prototype._applyFilterToEntry = function(entry) {
    var search = this.elements.searchInput ? this.elements.searchInput.value.toLowerCase() : '';
    var level = this.elements.levelSelect ? this.elements.levelSelect.value : '';
    var source = this.elements.sourceSelect ? this.elements.sourceSelect.value : '';

    var matchSearch = !search || entry.textContent.toLowerCase().indexOf(search) !== -1;
    var matchLevel = !level || entry.dataset.level === level;
    var matchSource = !source || entry.dataset.source === source;

    entry.style.display = (matchSearch && matchLevel && matchSource) ? '' : 'none';
};

/**
 * Update source dropdown with discovered sources
 */
DebugConsole.prototype._updateSourceDropdown = function() {
    if (!this.elements.sourceSelect) return;

    var currentValue = this.elements.sourceSelect.value;

    // Clear existing options except first (All)
    while (this.elements.sourceSelect.options.length > 1) {
        this.elements.sourceSelect.remove(1);
    }

    // Add sorted sources
    var sortedSources = Array.from(this.sources).sort();
    sortedSources.forEach(function(source) {
        var option = document.createElement('option');
        option.value = source;
        option.textContent = source;
        this.elements.sourceSelect.appendChild(option);
    }, this);

    // Restore selected value if it still exists
    if (currentValue && this.sources.has(currentValue)) {
        this.elements.sourceSelect.value = currentValue;
    }
};

/**
 * Update visible log count badge
 */
DebugConsole.prototype._updateCount = function() {
    if (!this.elements.countBadge) return;

    var total = this.elements.container.querySelectorAll('.log-entry').length;
    var visible = this.elements.container.querySelectorAll('.log-entry:not([style*="display: none"])').length;

    if (visible === total) {
        this.elements.countBadge.textContent = total;
    } else {
        this.elements.countBadge.textContent = visible + ' / ' + total;
    }
};

/**
 * Toggle pause state
 */
DebugConsole.prototype.togglePause = function() {
    this.paused = !this.paused;

    if (this.elements.pauseBtn) {
        if (this.paused) {
            this.elements.pauseBtn.classList.add('btn-warning');
            this.elements.pauseBtn.classList.remove('btn-secondary');
            this.elements.pauseBtn.innerHTML = '<i class="bi bi-play-fill"></i> Resume';
        } else {
            this.elements.pauseBtn.classList.remove('btn-warning');
            this.elements.pauseBtn.classList.add('btn-secondary');
            this.elements.pauseBtn.innerHTML = '<i class="bi bi-pause-fill"></i> Pause';
        }
    }
};

/**
 * Clear all displayed logs
 */
DebugConsole.prototype.clear = function() {
    if (this.elements.container) {
        this.elements.container.innerHTML = '';
    }
    this._updateCount();
};

/**
 * Export logs as text
 * @returns {string} Logs as plain text
 */
DebugConsole.prototype.exportAsText = function() {
    var entries = this.elements.container.querySelectorAll('.log-entry');
    var lines = [];

    entries.forEach(function(entry) {
        var time = entry.querySelector('.log-time');
        var source = entry.querySelector('.log-source');
        var message = entry.querySelector('.log-message');

        var line = (time ? time.textContent + ' ' : '') +
                   (source ? source.textContent + ' ' : '') +
                   (message ? message.textContent : '');
        lines.push(line);
    });

    return lines.join('\n');
};

/**
 * Download logs as text file
 */
DebugConsole.prototype.downloadLogs = function() {
    var text = this.exportAsText();
    var blob = new Blob([text], { type: 'text/plain' });
    var url = URL.createObjectURL(blob);

    var a = document.createElement('a');
    a.href = url;
    a.download = 'vclu-logs-' + new Date().toISOString().slice(0, 19).replace(/[T:]/g, '-') + '.txt';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
};


/**
 * RegistryBrowser - Browse and manage Lua registry objects
 * @param {Object} options - Configuration options
 */
function RegistryBrowser(options) {
    this.options = options || {};
    this.allObjects = [];
    this.elements = {};
    this.completions = null;

    // Mapping from object type to class name in completions
    this.typeToClass = {
        'DOUT': 'RemoteSwitch',
        'DIN': 'RemoteSensor',
        'DIMMER': 'RemoteLight',
        'ROLLER': 'RemoteCover',
        'LEDRGB': 'RemoteRGBLight',
        'AnalogOUT': 'RemoteNumber',
        'AnalogIN': 'RemoteNumber',
        'plugin': 'Plugin'
    };

    // Methods to hide (internal/constructor methods)
    this.hiddenMethods = ['new', 'init', 'create', '__index', '__newindex', '__tostring'];
}

/**
 * Initialize the registry browser
 */
RegistryBrowser.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        container: document.getElementById('registryContainer'),
        list: document.getElementById('registryList'),
        loading: document.getElementById('registryLoading'),
        empty: document.getElementById('registryEmpty'),
        error: document.getElementById('registryError'),
        searchInput: document.getElementById('registrySearchFilter'),
        typeSelect: document.getElementById('registryTypeFilter'),
        stateSelect: document.getElementById('registryStateFilter'),
        syncBtn: document.getElementById('syncBtn'),
        refreshBtn: document.getElementById('refreshRegistryBtn')
    };

    // Load completions for API reference
    this._loadCompletions();

    // Bind events
    if (this.elements.searchInput) {
        this.elements.searchInput.addEventListener('input', function() {
            self.applyFilters();
        });
    }

    if (this.elements.typeSelect) {
        this.elements.typeSelect.addEventListener('change', function() {
            self.applyFilters();
        });
    }

    if (this.elements.stateSelect) {
        this.elements.stateSelect.addEventListener('change', function() {
            self.applyFilters();
        });
    }

    if (this.elements.syncBtn) {
        this.elements.syncBtn.addEventListener('click', function() {
            self.sync();
        });
    }

    if (this.elements.refreshBtn) {
        this.elements.refreshBtn.addEventListener('click', function() {
            self.load();
        });
    }
};

/**
 * Load registry objects from API
 */
RegistryBrowser.prototype.load = function() {
    var self = this;

    this.elements.loading.style.display = 'block';
    this.elements.empty.style.display = 'none';
    this.elements.error.style.display = 'none';
    this.elements.list.innerHTML = '';

    fetch('/api/registry/list')
        .then(function(response) { return response.json(); })
        .then(function(data) {
            self.elements.loading.style.display = 'none';

            if (data.error) {
                self.elements.error.textContent = data.error;
                self.elements.error.style.display = 'block';
                return;
            }

            self.allObjects = Array.isArray(data) ? data : [];

            if (self.allObjects.length === 0) {
                self.elements.empty.style.display = 'block';
                return;
            }

            self.applyFilters();
        })
        .catch(function(err) {
            self.elements.loading.style.display = 'none';
            self.elements.error.textContent = err.message;
            self.elements.error.style.display = 'block';
        });
};

/**
 * Sync - fetch fresh states from remote CLUs
 */
RegistryBrowser.prototype.sync = function() {
    var self = this;
    var btn = this.elements.syncBtn;
    var originalHtml = btn.innerHTML;

    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm" role="status"></span> Syncing...';

    fetch('/api/registry/subscribe', { method: 'POST' })
        .then(function(response) { return response.json(); })
        .then(function(data) {
            btn.disabled = false;
            btn.innerHTML = originalHtml;

            if (data.error) {
                alert('Sync error: ' + data.error);
            } else {
                // Reload registry after sync to show new values
                setTimeout(function() { self.load(); }, 500);
            }
        })
        .catch(function(err) {
            btn.disabled = false;
            btn.innerHTML = originalHtml;
            alert('Sync error: ' + err.message);
        });
};

/**
 * Apply filters to objects and render
 */
RegistryBrowser.prototype.applyFilters = function() {
    var searchTerm = this.elements.searchInput ? this.elements.searchInput.value.toLowerCase() : '';
    var typeFilter = this.elements.typeSelect ? this.elements.typeSelect.value : '';
    var stateFilter = this.elements.stateSelect ? this.elements.stateSelect.value : '';

    var self = this;
    var filtered = this.allObjects.filter(function(obj) {
        // Search filter
        if (searchTerm) {
            var inPath = obj.path && obj.path.toLowerCase().indexOf(searchTerm) !== -1;
            var inName = obj.name && obj.name.toLowerCase().indexOf(searchTerm) !== -1;
            if (!inPath && !inName) return false;
        }

        // Type filter
        if (typeFilter && obj.type !== typeFilter) {
            return false;
        }

        // State filter
        if (stateFilter) {
            var isOn = obj.state === 1 || obj.state === '1' || obj.state === true;
            var isOff = obj.state === 0 || obj.state === '0' || obj.state === false;
            if (stateFilter === 'on' && !isOn) return false;
            if (stateFilter === 'off' && !isOff) return false;
            if (stateFilter === 'unknown' && (isOn || isOff)) return false;
        }

        return true;
    });

    this._render(filtered);
};

/**
 * Render filtered objects as table
 * @param {Array} objects - Objects to render
 */
RegistryBrowser.prototype._render = function(objects) {
    var self = this;

    if (objects.length === 0) {
        this.elements.list.innerHTML = '';
        this.elements.empty.style.display = 'block';
        return;
    }

    this.elements.empty.style.display = 'none';
    this.elements.list.innerHTML = '';

    // Group by CLU
    var grouped = {};
    objects.forEach(function(obj) {
        var clu = obj.path ? obj.path.split('.')[0] : 'Unknown';
        if (!grouped[clu]) {
            grouped[clu] = [];
        }
        grouped[clu].push(obj);
    });

    // Type badge classes
    var typeBadges = {
        'DOUT': 'bg-primary',
        'DIN': 'bg-info',
        'DIMMER': 'bg-warning text-dark',
        'LEDRGB': 'bg-danger',
        'ROLLER': 'bg-success',
        'AnalogIN': 'bg-secondary',
        'AnalogOUT': 'bg-dark',
        'plugin': 'bg-purple'
    };

    // Render groups as tables
    Object.keys(grouped).sort().forEach(function(cluName) {
        var groupDiv = document.createElement('div');
        groupDiv.className = 'registry-group';

        var header = document.createElement('div');
        header.className = 'registry-group-header';
        var groupIcon = cluName === 'Plugins' ? 'bi-plug' : 'bi-hdd-network';
        header.innerHTML = '<i class="bi ' + groupIcon + '"></i> ' + VCLU.esc(cluName) +
                          ' <span class="badge bg-secondary">' + grouped[cluName].length + '</span>';
        groupDiv.appendChild(header);

        var table = document.createElement('table');
        table.className = 'registry-table';

        var thead = document.createElement('thead');
        thead.innerHTML = '<tr>' +
            '<th>Name</th>' +
            '<th>Path</th>' +
            '<th>Type</th>' +
            '<th>State</th>' +
            '</tr>';
        table.appendChild(thead);

        var tbody = document.createElement('tbody');

        grouped[cluName].forEach(function(obj) {
            var tr = document.createElement('tr');
            tr.className = 'registry-row';

            // Display name
            var displayName = obj.name || obj.objectId || obj.path;
            if (obj.type === 'plugin' && obj.version) {
                displayName += ' v' + obj.version;
            }

            // Format state
            var stateHtml = '';
            var stateValue = obj.state;

            if (stateValue === 1 || stateValue === '1' || stateValue === true) {
                stateHtml = '<span class="state-indicator state-on"><i class="bi bi-circle-fill"></i> ON</span>';
            } else if (stateValue === 0 || stateValue === '0' || stateValue === false) {
                stateHtml = '<span class="state-indicator state-off"><i class="bi bi-circle"></i> OFF</span>';
            } else if (stateValue !== null && stateValue !== undefined) {
                stateHtml = '<span class="state-indicator state-value"><i class="bi bi-activity"></i> ' + VCLU.esc(String(stateValue)) + '</span>';
            } else {
                stateHtml = '<span class="state-indicator state-unknown"><i class="bi bi-question-circle"></i></span>';
            }

            var badgeClass = typeBadges[obj.type] || 'bg-secondary';

            // Get methods for this type (from lua-completions.json)
            var methods = self._getMethodsForType(obj.type);
            // For plugins, append registered sensors/controls
            if (obj.type === 'plugin') {
                (obj.sensors || []).forEach(function(s) {
                    methods.push({label: 'sensor: ' + s, detail: '— plugin:get("' + s + '")', documentation: 'Read-only sensor'});
                });
                (obj.controls || []).forEach(function(c) {
                    methods.push({label: 'control: ' + c, detail: '— plugin:get("' + c + '")', documentation: 'Read-write control'});
                });
            }
            var hasApi = methods.length > 0;
            var expandIcon = hasApi ? '<i class="bi bi-chevron-right registry-expand-icon"></i> ' : '';

            tr.innerHTML =
                '<td class="registry-cell-name">' + expandIcon + VCLU.esc(displayName) + '</td>' +
                '<td class="registry-cell-path">' + VCLU.esc(obj.path) + '</td>' +
                '<td><span class="badge ' + badgeClass + '">' + VCLU.esc(obj.type || '?') + '</span></td>' +
                '<td>' + stateHtml + '</td>';

            tbody.appendChild(tr);

            // Add expandable API row if we have methods
            if (hasApi) {
                var apiRow = document.createElement('tr');
                apiRow.className = 'registry-api-row';
                apiRow.style.display = 'none';

                var apiHtml = methods.map(function(m) {
                    var params = m.detail || '()';
                    return '<span class="api-method" title="' + VCLU.esc(m.documentation || '') + '">' +
                           VCLU.esc(m.label) + '<span class="api-params">' + VCLU.esc(params) + '</span></span>';
                }).join('<br>');

                apiRow.innerHTML = '<td colspan="4" class="registry-cell-api">' + apiHtml + '</td>';
                tbody.appendChild(apiRow);

                // Toggle on click
                tr.style.cursor = 'pointer';
                tr.addEventListener('click', function() {
                    var isExpanded = apiRow.style.display !== 'none';
                    apiRow.style.display = isExpanded ? 'none' : 'table-row';
                    var icon = tr.querySelector('.registry-expand-icon');
                    if (icon) {
                        icon.className = isExpanded
                            ? 'bi bi-chevron-right registry-expand-icon'
                            : 'bi bi-chevron-down registry-expand-icon';
                    }
                    tr.classList.toggle('expanded', !isExpanded);
                });
            }
        });

        table.appendChild(tbody);
        groupDiv.appendChild(table);
        self.elements.list.appendChild(groupDiv);
    });
};

/**
 * Load lua-completions.json for API reference
 */
RegistryBrowser.prototype._loadCompletions = function() {
    var self = this;
    fetch('/static/js/lua-completions.json')
        .then(function(response) { return response.json(); })
        .then(function(data) {
            self.completions = data;
        })
        .catch(function(err) {
            console.warn('Failed to load lua-completions.json:', err);
        });
};

/**
 * Get methods for a given object type
 * @param {string} objType - Object type (DOUT, DIN, etc.)
 * @returns {Array} Array of method objects
 */
RegistryBrowser.prototype._getMethodsForType = function(objType) {
    if (!this.completions || !this.completions.methods) {
        return [];
    }

    var className = this.typeToClass[objType];
    if (!className) {
        return [];
    }

    var methods = this.completions.methods[className];
    if (!methods) {
        return [];
    }

    var self = this;
    return methods.filter(function(m) {
        return self.hiddenMethods.indexOf(m.label) === -1;
    });
};



/**
 * DebugPage - Manages tabs and initializes components
 */
function DebugPage() {
    this.debugConsole = null;
    this.registryBrowser = null;
    this.currentTab = 'logs';
}

/**
 * Initialize the debug page
 */
DebugPage.prototype.init = function() {
    var self = this;

    // Initialize components
    this.debugConsole = new DebugConsole();
    this.debugConsole.init();

    this.registryBrowser = new RegistryBrowser();
    this.registryBrowser.init();

    // Setup tab switching
    var tabs = document.querySelectorAll('.debug-tab');
    tabs.forEach(function(tab) {
        tab.addEventListener('click', function() {
            self.switchTab(this.dataset.tab);
        });
    });

    // Check URL for tab parameter
    var urlParams = new URLSearchParams(window.location.search);
    var tabParam = urlParams.get('tab');
    if (tabParam === 'registry') {
        this.switchTab('registry');
    }
};

/**
 * Switch between tabs
 * @param {string} tabName - Tab name ('logs' or 'registry')
 */
DebugPage.prototype.switchTab = function(tabName) {
    var self = this;
    this.currentTab = tabName;

    // Update tab buttons
    var tabs = document.querySelectorAll('.debug-tab');
    tabs.forEach(function(tab) {
        if (tab.dataset.tab === tabName) {
            tab.classList.add('active');
        } else {
            tab.classList.remove('active');
        }
    });

    // Show/hide content
    var logsTab = document.getElementById('logsTab');
    var registryTab = document.getElementById('registryTab');

    if (tabName === 'logs') {
        logsTab.style.display = 'flex';
        registryTab.style.display = 'none';
    } else if (tabName === 'registry') {
        logsTab.style.display = 'none';
        registryTab.style.display = 'flex';
        // Load registry on first view
        if (this.registryBrowser.allObjects.length === 0) {
            this.registryBrowser.load();
        }
    }

    // Update URL without reload
    var url = new URL(window.location);
    if (tabName === 'logs') {
        url.searchParams.delete('tab');
    } else {
        url.searchParams.set('tab', tabName);
    }
    window.history.replaceState({}, '', url);
};


// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    var debugPage = new DebugPage();
    debugPage.init();

    // Expose for external access if needed
    window.debugConsole = debugPage.debugConsole;
    window.registryBrowser = debugPage.registryBrowser;
});
