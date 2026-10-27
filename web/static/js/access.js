/**
 * AccessControl - Object access management for integrations
 * Uses prototype pattern for modularity
 * @param {Object} options - Configuration options
 */
function AccessControl(options) {
    this.options = options || {};
    this.objects = [];
    this.bots = [];       // MCP bots for dynamic columns
    this.elements = {};
    this.bulkInProgress = false;
}

/**
 * Initialize access control manager
 */
AccessControl.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        loading: document.getElementById('objectsLoading'),
        empty: document.getElementById('objectsEmpty'),
        table: document.getElementById('objectsTable'),
        tbody: document.getElementById('objectsTableBody'),
        countBadge: document.getElementById('objectCount'),
        searchInput: document.getElementById('searchFilter'),
        typeFilter: document.getElementById('typeFilter'),
        bulkActions: document.getElementById('bulkActions'),
        bulkProgress: document.getElementById('bulkProgress'),
        bulkProgressText: document.getElementById('bulkProgressText'),
        selectedCount: document.getElementById('selectedCount'),
        selectAll: document.getElementById('selectAll')
    };

    // Bind search filter
    if (this.elements.searchInput) {
        this.elements.searchInput.addEventListener('input', function() {
            self.applyFilters();
        });
    }

    // Bind type filter
    if (this.elements.typeFilter) {
        this.elements.typeFilter.addEventListener('change', function() {
            self.applyFilters();
        });
    }

    // Bind select all checkbox
    if (this.elements.selectAll) {
        this.elements.selectAll.addEventListener('change', function() {
            self.toggleSelectAll(this.checked);
        });
    }

    // Load objects
    this.loadObjects();
    this.loadRemoteProfiles();
};

/**
 * Show the profiles assigned to remote access as checkboxes
 */
AccessControl.prototype.loadRemoteProfiles = function() {
    var box = document.getElementById('remoteProfiles');
    if (!box) return;
    Promise.all([
        fetch('/api/access/profiles').then(function(r) { return r.json(); }),
        fetch('/api/access/assignments').then(function(r) { return r.json(); })
    ]).then(function(res) {
        var profiles = (res[0].profiles || []).map(function(p) { return p.name; }).sort();
        var assigned = (res[1].assignments || {}).remote || [];
        box.innerHTML = '';
        profiles.forEach(function(name) {
            var label = document.createElement('label');
            label.className = 'form-check-label small';
            var cb = document.createElement('input');
            cb.type = 'checkbox';
            cb.className = 'form-check-input me-1 remote-profile';
            cb.value = name;
            cb.checked = assigned.indexOf(name) >= 0;
            label.appendChild(cb);
            label.appendChild(document.createTextNode(name));
            box.appendChild(label);
        });
    }).catch(function(err) {
        VCLU.showToast('Error: ' + err.message, 'danger');
    });
};

/**
 * Save the profiles checked for remote access (none checked: no-access)
 */
AccessControl.prototype.saveRemoteProfiles = function() {
    var self = this;
    var names = [];
    document.querySelectorAll('#remoteProfiles .remote-profile:checked').forEach(function(cb) {
        names.push(cb.value);
    });
    if (names.length === 0) names = ['no-access'];
    fetch('/api/access/assignments', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ assignments: { remote: names } })
    })
    .then(function(r) { return r.json(); })
    .then(function(result) {
        if (result.error) {
            VCLU.showToast('Error: ' + result.error, 'danger');
        } else {
            VCLU.showToast('Saved', 'success');
        }
        self.loadRemoteProfiles();
        self.loadObjects();
    })
    .catch(function(err) {
        VCLU.showToast('Error: ' + err.message, 'danger');
    });
};

/**
 * Load objects with access levels from API
 */
AccessControl.prototype.loadObjects = function() {
    var self = this;

    this._showLoading();

    fetch('/api/access')
        .then(function(response) {
            return response.json();
        })
        .then(function(data) {
            self.objects = data.objects || [];
            self.bots = data.bots || [];
            self._render();
        })
        .catch(function(err) {
            self._showError('Error loading objects: ' + err.message);
        });
};

/**
 * Render objects table
 */
AccessControl.prototype._render = function() {
    var self = this;

    this.elements.loading.style.display = 'none';

    if (this.objects.length === 0) {
        this._showEmpty();
        return;
    }

    this.elements.empty.style.display = 'none';
    this.elements.table.style.display = 'block';

    // Render dynamic bot column headers
    this._renderBotHeaders();

    // Clear existing rows
    this.elements.tbody.innerHTML = '';

    // Create rows
    this.objects.forEach(function(obj) {
        var row = self._createRow(obj);
        self.elements.tbody.appendChild(row);
    });

    // Populate type filter from data
    this._populateTypeFilter();

    // Update count
    this._updateCount();

    // Reset selection
    this.elements.selectAll.checked = false;
    this._updateSelectionUI();

    // Show MCP bot link if bots exist
    var mcpLink = document.getElementById('mcpBotLink');
    if (mcpLink) {
        mcpLink.style.display = this.bots.length > 0 ? 'inline' : 'none';
    }
};

/**
 * Populate type filter dropdown from loaded objects
 */
AccessControl.prototype._populateTypeFilter = function() {
    if (!this.elements.typeFilter) return;

    var types = {};
    this.objects.forEach(function(obj) {
        if (obj.type) {
            types[obj.type] = (types[obj.type] || 0) + 1;
        }
    });

    var sortedTypes = Object.keys(types).sort();
    var currentValue = this.elements.typeFilter.value;

    this.elements.typeFilter.innerHTML = '<option value="">All types</option>';
    sortedTypes.forEach(function(type) {
        var option = document.createElement('option');
        option.value = type;
        option.textContent = type + ' (' + types[type] + ')';
        if (type === currentValue) {
            option.selected = true;
        }
        this.elements.typeFilter.appendChild(option);
    }, this);
};

/**
 * Render dynamic bot column header (single "Bots" column)
 */
AccessControl.prototype._renderBotHeaders = function() {
    var headRow = document.getElementById('accessTableHead');
    if (!headRow) return;

    // Remove old dynamic bot header
    headRow.querySelectorAll('.bot-header').forEach(function(th) { th.remove(); });

    // Add single "Bots" column if bots exist
    if (this.bots.length > 0) {
        var th = document.createElement('th');
        th.className = 'text-center bot-header';
        th.innerHTML = '<i class="bi bi-robot"></i> Bots';
        headRow.appendChild(th);
    }

    // Render bot bulk action items in dropdown
    this._renderBotBulkActions();
};

/**
 * Append bot sections to the bulk action dropdown menu
 */
AccessControl.prototype._renderBotBulkActions = function() {
    var menu = document.getElementById('bulkMenu');
    if (!menu) return;

    // Remove old dynamic bot items
    menu.querySelectorAll('.bulk-bot-item').forEach(function(el) { el.remove(); });

    var self = this;
    this.bots.forEach(function(bot) {
        var divider = document.createElement('li');
        divider.className = 'bulk-bot-item';
        divider.innerHTML = '<hr class="dropdown-divider">';
        menu.appendChild(divider);

        var header = document.createElement('li');
        header.className = 'bulk-bot-item';
        header.innerHTML = '<h6 class="dropdown-header"><i class="bi bi-robot"></i> ' + VCLU.esc(bot.name) + '</h6>';
        menu.appendChild(header);

        ['full', 'readonly', 'hidden'].forEach(function(level) {
            var badgeClass = level === 'full' ? 'bg-success' : level === 'readonly' ? 'bg-warning' : 'bg-secondary';
            var label = level.charAt(0).toUpperCase() + level.slice(1);
            var li = document.createElement('li');
            li.className = 'bulk-bot-item';
            var a = document.createElement('a');
            a.className = 'dropdown-item';
            a.href = '#';
            a.innerHTML = '<span class="badge ' + badgeClass + '">' + label.charAt(0) + '</span> ' + label;
            a.addEventListener('click', function(e) {
                e.preventDefault();
                self.bulkSetBotAccess(bot.id, level);
            });
            li.appendChild(a);
            menu.appendChild(li);
        });
    });
};

/**
 * Create table row for object
 * @param {Object} obj - Object data
 * @returns {HTMLElement} Table row element
 */
AccessControl.prototype._createRow = function(obj) {
    var self = this;
    var row = document.createElement('tr');
    var typeIcon = this._getTypeIcon(obj.type);
    var displayName = obj.name || obj.path.split('.').pop();

    row.dataset.path = obj.path;
    row.dataset.type = obj.type;
    row.dataset.name = displayName.toLowerCase();

    var html =
        '<td><input type="checkbox" class="form-check-input row-checkbox"></td>' +
        '<td><i class="bi ' + typeIcon + '"></i> ' + VCLU.esc(displayName) +
            '<br><code class="small text-muted">' + VCLU.esc(obj.path) + '</code></td>' +
        '<td><small>' + VCLU.esc(obj.type) + '</small></td>' +
        '<td class="text-center">' +
            this._createSelect(obj.path, 'mqtt', obj.access.mqtt) +
        '</td>' +
        '<td class="text-center">' +
            this._createSelect(obj.path, 'homekit', obj.access.homekit) +
        '</td>' +
        '<td class="text-center">' +
            this._createSelect(obj.path, 'remote', obj.access.remote || 'hidden') +
        '</td>';

    // Single "Bots" column with popover
    if (this.bots.length > 0) {
        var botAccess = obj.botAccess || {};
        html += '<td class="text-center">' +
            this._createBotSummary(obj.path, botAccess) +
        '</td>';
    }

    row.innerHTML = html;

    // Bind checkbox change
    var checkbox = row.querySelector('.row-checkbox');
    checkbox.addEventListener('change', function() {
        self._updateSelectionUI();
    });

    // Bind integration select change handlers
    row.querySelectorAll('.access-select').forEach(function(select) {
        select.addEventListener('change', function() {
            self.setAccess(
                select.dataset.path,
                select.dataset.integration,
                select.value,
                select
            );
        });
    });

    // Bind bot popover trigger
    var botTrigger = row.querySelector('.bot-popover-trigger');
    if (botTrigger) {
        botTrigger.addEventListener('click', function(e) {
            e.stopPropagation();
            self._showBotPopover(botTrigger, obj.path, obj.botAccess || {});
        });
    }

    return row;
};

/**
 * Create access select element HTML
 */
AccessControl.prototype._createSelect = function(path, integration, currentValue) {
    var options = ['full', 'readonly', 'hidden'];
    var html = '<select class="form-select form-select-sm access-select" ' +
               'style="width: auto; margin: 0 auto;" ' +
               'data-path="' + VCLU.esc(path) + '" ' +
               'data-integration="' + integration + '">';

    options.forEach(function(opt) {
        var selected = (currentValue === opt) ? ' selected' : '';
        var label = opt.charAt(0).toUpperCase() + opt.slice(1);
        html += '<option value="' + opt + '"' + selected + '>' + label + '</option>';
    });

    html += '</select>';
    return html;
};

/**
 * Toggle select all visible rows
 */
AccessControl.prototype.toggleSelectAll = function(checked) {
    var visibleRows = this.elements.tbody.querySelectorAll('tr:not([style*="display: none"])');
    visibleRows.forEach(function(row) {
        var checkbox = row.querySelector('.row-checkbox');
        if (checkbox) {
            checkbox.checked = checked;
        }
    });
    this._updateSelectionUI();
};

/**
 * Clear all selections
 */
AccessControl.prototype.clearSelection = function() {
    var checkboxes = this.elements.tbody.querySelectorAll('.row-checkbox');
    checkboxes.forEach(function(cb) {
        cb.checked = false;
    });
    this.elements.selectAll.checked = false;
    this._updateSelectionUI();
};

/**
 * Get selected rows
 */
AccessControl.prototype._getSelectedRows = function() {
    var rows = [];
    var checkboxes = this.elements.tbody.querySelectorAll('.row-checkbox:checked');
    checkboxes.forEach(function(cb) {
        rows.push(cb.closest('tr'));
    });
    return rows;
};

/**
 * Update selection UI (count, show/hide bulk actions)
 */
AccessControl.prototype._updateSelectionUI = function() {
    var selectedRows = this._getSelectedRows();
    var count = selectedRows.length;

    this.elements.selectedCount.textContent = count;

    if (count > 0) {
        this.elements.bulkActions.style.display = 'flex';
    } else {
        this.elements.bulkActions.style.display = 'none';
    }

    // Update select all checkbox state
    var visibleRows = this.elements.tbody.querySelectorAll('tr:not([style*="display: none"])');
    var visibleChecked = this.elements.tbody.querySelectorAll('tr:not([style*="display: none"]) .row-checkbox:checked');
    this.elements.selectAll.checked = visibleRows.length > 0 && visibleRows.length === visibleChecked.length;
    this.elements.selectAll.indeterminate = visibleChecked.length > 0 && visibleChecked.length < visibleRows.length;
};

/**
 * Set access level for single object
 */
AccessControl.prototype.setAccess = function(path, integration, access, selectElement) {
    var self = this;

    selectElement.disabled = true;

    fetch('/api/access', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            path: path,
            integration: integration,
            access: access
        })
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(result) {
        if (result.error) {
            VCLU.showToast('Error: ' + result.error, 'danger');
            self.loadObjects();
        } else {
            self._flashSuccess(selectElement);
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
        self.loadObjects();
    })
    .finally(function() {
        selectElement.disabled = false;
    });
};

/**
 * Create bot summary badge HTML for the single Bots column
 */
AccessControl.prototype._createBotSummary = function(path, botAccess) {
    var self = this;
    var counts = { full: 0, readonly: 0, hidden: 0 };

    this.bots.forEach(function(bot) {
        var level = botAccess[bot.id] || 'full';
        counts[level] = (counts[level] || 0) + 1;
    });

    // Build compact summary
    var parts = [];
    if (counts.full > 0) parts.push('<span class="badge bg-success">' + counts.full + 'F</span>');
    if (counts.readonly > 0) parts.push('<span class="badge bg-warning">' + counts.readonly + 'R</span>');
    if (counts.hidden > 0) parts.push('<span class="badge bg-secondary">' + counts.hidden + 'H</span>');

    var summary = parts.length > 0 ? parts.join(' ') : '<span class="badge bg-success">All Full</span>';

    return '<button type="button" class="btn btn-sm btn-outline-secondary bot-popover-trigger" ' +
           'data-path="' + VCLU.esc(path) + '" style="white-space: nowrap;">' +
           summary + '</button>';
};

/**
 * Show popover with per-bot access dropdowns
 */
AccessControl.prototype._showBotPopover = function(trigger, path, botAccess) {
    var self = this;

    // Close any existing popover
    this._closeBotPopover();

    // Build popover content
    var content = document.createElement('div');
    content.className = 'bot-access-popover';
    content.style.cssText = 'min-width: 220px;';

    this.bots.forEach(function(bot) {
        var level = botAccess[bot.id] || 'full';
        var row = document.createElement('div');
        row.className = 'd-flex justify-content-between align-items-center mb-2';
        row.innerHTML =
            '<span class="small me-2"><i class="bi bi-robot"></i> ' + VCLU.esc(bot.name) + '</span>' +
            '<select class="form-select form-select-sm bot-access-select" ' +
            'style="width: auto;" ' +
            'data-path="' + VCLU.esc(path) + '" ' +
            'data-bot-id="' + bot.id + '">' +
            '<option value="full"' + (level === 'full' ? ' selected' : '') + '>Full</option>' +
            '<option value="readonly"' + (level === 'readonly' ? ' selected' : '') + '>Readonly</option>' +
            '<option value="hidden"' + (level === 'hidden' ? ' selected' : '') + '>Hidden</option>' +
            '</select>';
        content.appendChild(row);
    });

    // Bind select change handlers inside popover
    content.querySelectorAll('.bot-access-select').forEach(function(select) {
        select.addEventListener('change', function() {
            self.setBotAccess(select.dataset.botId, select.dataset.path, select.value, select);
            // Update the summary badge in the row after change
            botAccess[select.dataset.botId] = select.value;
            var tr = trigger.closest('tr');
            if (tr) {
                var td = tr.querySelector('.bot-popover-trigger');
                if (td) {
                    td.innerHTML = self._createBotSummaryInner(botAccess);
                }
            }
        });
    });

    // Create popover element
    var popover = document.createElement('div');
    popover.className = 'bot-popover-container card shadow';
    popover.style.cssText = 'position: absolute; z-index: 1050; padding: 12px; min-width: 240px;';
    popover.appendChild(content);

    // Position relative to trigger
    document.body.appendChild(popover);
    var rect = trigger.getBoundingClientRect();
    var popoverRect = popover.getBoundingClientRect();

    // Position to the left of the trigger if it would overflow right
    var left = rect.left + window.scrollX;
    if (left + popoverRect.width > window.innerWidth) {
        left = rect.right + window.scrollX - popoverRect.width;
    }
    var top = rect.bottom + window.scrollY + 4;

    popover.style.left = left + 'px';
    popover.style.top = top + 'px';

    this._activePopover = popover;
    this._activePopoverTrigger = trigger;

    // Close on click outside
    var closeHandler = function(e) {
        if (!popover.contains(e.target) && e.target !== trigger && !trigger.contains(e.target)) {
            self._closeBotPopover();
            document.removeEventListener('click', closeHandler);
        }
    };
    setTimeout(function() {
        document.addEventListener('click', closeHandler);
    }, 0);
    this._popoverCloseHandler = closeHandler;
};

/**
 * Inner HTML for bot summary badges (used to update without replacing the button)
 */
AccessControl.prototype._createBotSummaryInner = function(botAccess) {
    var counts = { full: 0, readonly: 0, hidden: 0 };
    var self = this;

    this.bots.forEach(function(bot) {
        var level = botAccess[bot.id] || 'full';
        counts[level] = (counts[level] || 0) + 1;
    });

    var parts = [];
    if (counts.full > 0) parts.push('<span class="badge bg-success">' + counts.full + 'F</span>');
    if (counts.readonly > 0) parts.push('<span class="badge bg-warning">' + counts.readonly + 'R</span>');
    if (counts.hidden > 0) parts.push('<span class="badge bg-secondary">' + counts.hidden + 'H</span>');

    return parts.length > 0 ? parts.join(' ') : '<span class="badge bg-success">All Full</span>';
};

/**
 * Close active bot popover
 */
AccessControl.prototype._closeBotPopover = function() {
    if (this._activePopover) {
        this._activePopover.remove();
        this._activePopover = null;
    }
    if (this._popoverCloseHandler) {
        document.removeEventListener('click', this._popoverCloseHandler);
        this._popoverCloseHandler = null;
    }
    this._activePopoverTrigger = null;
};

/**
 * Set access level for a bot and object
 */
AccessControl.prototype.setBotAccess = function(botId, path, access, selectElement) {
    var self = this;

    selectElement.disabled = true;

    fetch('/api/access/bot', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            botId: botId,
            path: path,
            access: access
        })
    })
    .then(function(response) {
        return response.json();
    })
    .then(function(result) {
        if (result.error) {
            VCLU.showToast('Error: ' + result.error, 'danger');
            self.loadObjects();
        } else {
            self._flashSuccess(selectElement);
        }
    })
    .catch(function(error) {
        VCLU.showToast('Error: ' + error.message, 'danger');
        self.loadObjects();
    })
    .finally(function() {
        selectElement.disabled = false;
    });
};

/**
 * Bulk set bot access for selected objects
 */
AccessControl.prototype.bulkSetBotAccess = function(botId, access) {
    var self = this;

    if (this.bulkInProgress) return;

    var selectedRows = this._getSelectedRows();
    var paths = [];

    selectedRows.forEach(function(row) {
        paths.push(row.dataset.path);
    });

    if (paths.length === 0) return;

    this.bulkInProgress = true;
    this.elements.bulkProgress.style.display = 'inline';
    this._closeBotPopover();

    var completed = 0;
    var total = paths.length;

    function updateProgress() {
        self.elements.bulkProgressText.textContent = completed + '/' + total;
    }

    function processNext(index) {
        if (index >= paths.length) {
            self.bulkInProgress = false;
            self.elements.bulkProgress.style.display = 'none';
            // Reload to refresh summary badges
            self.loadObjects();
            return;
        }

        var path = paths[index];
        var row = selectedRows[index];
        row.style.backgroundColor = 'rgba(102, 126, 234, 0.2)';

        fetch('/api/access/bot', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                botId: botId,
                path: path,
                access: access
            })
        })
        .then(function(response) { return response.json(); })
        .then(function(result) {
            if (!result.error) {
                var btn = row.querySelector('.bot-popover-trigger');
                if (btn) self._flashSuccess(btn);
            }
        })
        .catch(function() {})
        .finally(function() {
            row.style.backgroundColor = '';
            completed++;
            updateProgress();
            setTimeout(function() { processNext(index + 1); }, 50);
        });
    }

    updateProgress();
    processNext(0);
};

/**
 * Bulk set access for selected objects
 */
AccessControl.prototype.bulkSetAccess = function(integration, access) {
    var self = this;

    if (this.bulkInProgress) {
        return;
    }

    var selectedRows = this._getSelectedRows();
    var selects = [];

    selectedRows.forEach(function(row) {
        var select = row.querySelector('.access-select[data-integration="' + integration + '"]');
        if (select && select.value !== access) {
            selects.push(select);
        }
    });

    if (selects.length === 0) {
        return;
    }

    this.bulkInProgress = true;
    this.elements.bulkProgress.style.display = 'inline';

    var completed = 0;
    var total = selects.length;

    function updateProgress() {
        self.elements.bulkProgressText.textContent = completed + '/' + total;
    }

    function processNext(index) {
        if (index >= selects.length) {
            self.bulkInProgress = false;
            self.elements.bulkProgress.style.display = 'none';
            // Keep selection so user can apply another action (e.g., HomeKit after MQTT)
            return;
        }

        var select = selects[index];
        var row = select.closest('tr');

        row.style.backgroundColor = 'rgba(102, 126, 234, 0.2)';
        select.disabled = true;

        fetch('/api/access', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                path: select.dataset.path,
                integration: integration,
                access: access
            })
        })
        .then(function(response) {
            return response.json();
        })
        .then(function(result) {
            if (!result.error) {
                select.value = access;
                self._flashSuccess(select);
            }
        })
        .catch(function() {})
        .finally(function() {
            select.disabled = false;
            row.style.backgroundColor = '';
            completed++;
            updateProgress();
            setTimeout(function() {
                processNext(index + 1);
            }, 50);
        });
    }

    updateProgress();
    processNext(0);
};

/**
 * Apply search and type filters
 */
AccessControl.prototype.applyFilters = function() {
    var search = this.elements.searchInput ? this.elements.searchInput.value.toLowerCase() : '';
    var typeFilter = this.elements.typeFilter ? this.elements.typeFilter.value : '';

    var rows = this.elements.tbody.querySelectorAll('tr');
    rows.forEach(function(row) {
        var matchSearch = !search ||
            row.dataset.name.indexOf(search) !== -1 ||
            row.dataset.path.toLowerCase().indexOf(search) !== -1;
        var matchType = !typeFilter || row.dataset.type === typeFilter;

        row.style.display = (matchSearch && matchType) ? '' : 'none';
    });

    this._updateCount();
    this._updateSelectionUI();
};

/**
 * Get icon class for device type
 */
AccessControl.prototype._getTypeIcon = function(type) {
    var icons = {
        'RemoteSwitch': 'bi-toggle-on',
        'DOUT': 'bi-toggle-on',
        'RemoteLight': 'bi-lightbulb',
        'DIMMER': 'bi-lightbulb',
        'RemoteCover': 'bi-border-width',
        'ROLLER': 'bi-border-width',
        'ROLLER_SH': 'bi-border-width',
        'RemoteSensor': 'bi-thermometer-half',
        'SENSOR': 'bi-thermometer-half',
        'RemoteValue': 'bi-123',
        'CLU': 'bi-cpu'
    };
    return icons[type] || 'bi-device-hdd';
};

/**
 * Update object count badge
 */
AccessControl.prototype._updateCount = function() {
    if (!this.elements.countBadge) return;

    var total = this.elements.tbody.querySelectorAll('tr').length;
    var visible = this.elements.tbody.querySelectorAll('tr:not([style*="display: none"])').length;

    if (visible === total) {
        this.elements.countBadge.textContent = total + ' objects';
    } else {
        this.elements.countBadge.textContent = visible + ' / ' + total + ' objects';
    }
};

AccessControl.prototype._showLoading = function() {
    this.elements.loading.style.display = 'block';
    this.elements.empty.style.display = 'none';
    this.elements.table.style.display = 'none';
    if (this.elements.bulkActions) {
        this.elements.bulkActions.style.display = 'none';
    }
};

AccessControl.prototype._showEmpty = function() {
    this.elements.loading.style.display = 'none';
    this.elements.empty.style.display = 'block';
    this.elements.empty.className = 'alert alert-secondary';
    this.elements.empty.innerHTML =
        '<i class="bi bi-info-circle"></i> No objects found in registry. ' +
        'Import OM files from the <a href="/grenton?tab=omfiles">OM Files</a> page first.';
    this.elements.table.style.display = 'none';
    this.elements.countBadge.textContent = '0 objects';
};

AccessControl.prototype._showError = function(message) {
    this.elements.loading.style.display = 'none';
    this.elements.empty.style.display = 'block';
    this.elements.empty.className = 'alert alert-danger';
    this.elements.empty.innerHTML =
        '<i class="bi bi-exclamation-triangle"></i> ' + VCLU.esc(message);
    this.elements.table.style.display = 'none';
};

AccessControl.prototype._flashSuccess = function(element) {
    element.style.borderColor = '#27C2A8';
    setTimeout(function() {
        element.style.borderColor = '';
    }, 1000);
};



// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    window.accessControl = new AccessControl();
    window.accessControl.init();
});
