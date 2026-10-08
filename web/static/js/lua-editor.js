/**
 * LuaEditor - Lua code editor with Monaco and file explorer
 * @param {Object} options - Configuration options
 * @param {string} options.containerId - Monaco container element ID
 * @param {string} options.treeId - File tree container element ID
 */
function LuaEditor(options) {
    this.options = options || {};
    this.editor = null;
    this.currentFile = null;
    this.currentFileReadonly = false;
    this.isModified = false;
    this.fileTree = { system: [], modules: null };
    this.selectedFolder = 'modules'; // Current folder for new files/folders
    this.completions = null; // Autocompletion data
    this.registryData = null; // Dynamic registry data for completions
    this._symbolTable = {}; // Variable name → inferred type

    // Tabs
    this.openTabs = [];      // [{path, name, readonly, content, viewState, modified, type}]
    this.activeTabPath = null;
    this._validateTimer = null;
    this._searchTimer = null;
    this._suppressChangeEvent = false;

    // Drafts
    this.drafts = [];        // [{path, size, modified}]
    this.backups = [];       // [{name, path, size, created}]
    this.diffEditor = null;  // monaco.editor.IDiffEditor, created lazily

    // History
    this.historyEvents = [];
    this.historyView = 'timeline'; // 'timeline' | 'files'

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize the editor
 */
LuaEditor.prototype.init = function() {
    var self = this;

    // Cache DOM elements
    this.elements = {
        container: document.getElementById(this.options.containerId || 'monacoContainer'),
        tree: document.getElementById(this.options.treeId || 'fileTree'),
        saveBtn: document.getElementById('saveBtn'),
        status: document.getElementById('editorStatus'),
        info: document.getElementById('editorInfo'),
        noFilesMessage: document.getElementById('noFilesMessage'),
        tabsBar: document.getElementById('tabsBar'),
        breadcrumbs: document.getElementById('breadcrumbs'),
        searchPanel: document.getElementById('searchPanel'),
        searchInput: document.getElementById('searchInput'),
        searchResults: document.getElementById('searchResults'),
        outputPanel: document.getElementById('outputPanel'),
        outputContent: document.getElementById('outputContent'),
        logsContent: document.getElementById('logsContent'),
        diffContainer: document.getElementById('monacoDiffContainer'),
        historyContainer: document.getElementById('historyContainer'),
        historyTimeline: document.getElementById('historyTimeline'),
        historyBtn: document.getElementById('historyBtn'),
        promoteBtn: document.getElementById('promoteBtn'),
        deleteDraftBtn: document.getElementById('deleteDraftBtn'),
        validateDraftBtn: document.getElementById('validateDraftBtn'),
        testDraftBtn: document.getElementById('testDraftBtn'),
        saveAllBtn: document.getElementById('saveAllBtn'),
        btnReload: document.getElementById('btnReload'),
        restoreBackupBtn: document.getElementById('restoreBackupBtn')
    };

    this._activeOutputTab = 'output';
    this._logEventSource = null;

    // Initialize Monaco
    this._initMonaco(function() {
        self.loadTree();
        self._watchThemeChanges();
        self._loadCompletions();
        self._loadRegistryData();
        self._setupValidation();
        self._registerRunCodeLens();
        self._setupOutputResize();
        self._setupOutputErrorClicks();

        // Auto-refresh drafts every 30s
        setInterval(self._refreshDrafts.bind(self), 30000);
    });

    // Bind global keyboard shortcuts
    this._bindGlobalShortcuts();

    // Warn before leaving with unsaved changes in any tab
    window.addEventListener('beforeunload', function(e) {
        var hasModified = self.openTabs.some(function(t) { return t.modified; });
        if (hasModified) {
            e.preventDefault();
            e.returnValue = '';
        }
    });
};

/**
 * Initialize Monaco editor
 * @private
 */
LuaEditor.prototype._initMonaco = function(callback) {
    var self = this;

    require.config({
        paths: { vs: '/static/vendor/monaco/vs' }
    });

    require(['vs/editor/editor.main'], function() {
        self.editor = monaco.editor.create(self.elements.container, {
            value: '-- Select a file to edit',
            language: 'lua',
            theme: VCLU.isDark() ? 'vs-dark' : 'vs',
            readOnly: true,
            automaticLayout: true,
            minimap: { enabled: false },
            scrollBeyondLastLine: false,
            fontSize: 14,
            lineNumbers: 'on',
            renderWhitespace: 'selection',
            wordWrap: 'on'
        });

        if (self.elements.noFilesMessage) {
            self.elements.noFilesMessage.style.display = 'none';
        }

        // Track modifications (ignore programmatic setValue calls)
        self.editor.onDidChangeModelContent(function() {
            if (self._suppressChangeEvent) return;
            if (!self.currentFileReadonly && self.currentFile) {
                self.setModified(true);
            }
        });

        // Editor keyboard shortcuts
        self.editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, function() {
            self.saveFile();
        });

        // Setup debounced symbol table rebuild for variable type inference
        self._setupSymbolTableListener();

        if (callback) callback();
    });
};

/**
 * Watch for theme changes and update Monaco
 * @private
 */
LuaEditor.prototype._watchThemeChanges = function() {
    VCLU.onThemeChange(function(dark) {
        monaco.editor.setTheme(dark ? 'vs-dark' : 'vs');
    });
};

/**
 * Bind global keyboard shortcuts
 * @private
 */
LuaEditor.prototype._bindGlobalShortcuts = function() {
    var self = this;
    document.addEventListener('keydown', function(e) {
        if ((e.ctrlKey || e.metaKey) && e.key === 's') {
            e.preventDefault();
            self.saveFile();
        }
        if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'F') {
            e.preventDefault();
            self.toggleSearchPanel();
        }
        // Ctrl+W to close active tab
        if ((e.ctrlKey || e.metaKey) && e.key === 'w' && self.activeTabPath) {
            e.preventDefault();
            self._closeTab(self.activeTabPath);
        }
        // Ctrl+Shift+Enter to run selection or current line
        if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'Enter') {
            e.preventDefault();
            self._runSelection();
        }
    });
};

/**
 * Set modified state
 * @param {boolean} modified
 */
LuaEditor.prototype.setModified = function(modified) {
    this.isModified = modified;
    var saveBtn = this.elements.saveBtn;
    if (saveBtn) saveBtn.disabled = !modified;

    // Update tab modified state
    if (this.activeTabPath) {
        var tab = this._findTab(this.activeTabPath);
        if (tab) tab.modified = modified;
        this._renderTabs();
    }
};

/**
 * Load file tree from API
 */
LuaEditor.prototype.loadTree = function() {
    var self = this;

    Promise.all([
        fetch('/api/editor/tree').then(function(r) { return r.json(); }),
        fetch('/api/mcp/workspace/drafts').then(function(r) { return r.json(); }).catch(function() { return { drafts: [] }; }),
        fetch('/api/mcp/workspace/backups').then(function(r) { return r.json(); }).catch(function() { return { backups: [] }; })
    ])
    .then(function(results) {
        self.fileTree = results[0];
        self.drafts = (results[1] && results[1].drafts) || [];
        self.backups = (results[2] && results[2].backups) || [];
        self.renderTree();
    })
    .catch(function(error) {
        console.error('Failed to load tree:', error);
        if (self.elements.status) self.elements.status.textContent = 'Error loading files';
    });
};

/**
 * Refresh drafts list periodically (every 30s)
 * @private
 */
LuaEditor.prototype._refreshDrafts = function() {
    var self = this;

    fetch('/api/mcp/workspace/drafts')
        .then(function(r) { return r.json(); })
        .then(function(data) {
            var newDrafts = (data && data.drafts) || [];
            var oldCount = self.drafts.length;
            self.drafts = newDrafts;
            if (newDrafts.length !== oldCount) {
                self.renderTree();
            }
        })
        .catch(function() {
            // Silent fail
        });
};

/**
 * Render file tree in DOM
 */
LuaEditor.prototype.renderTree = function() {
    var self = this;
    var container = this.elements.tree;
    container.innerHTML = '';

    // Helper: create a collapsible section
    function createSection(label, startExpanded) {
        var section = document.createElement('div');
        section.className = 'file-tree-section' + (startExpanded ? '' : ' collapsed');

        var header = document.createElement('div');
        header.className = 'file-tree-section-header';
        header.innerHTML =
            '<i class="bi ' + (startExpanded ? 'bi-chevron-down' : 'bi-chevron-right') + ' section-chevron"></i>' +
            '<span>' + label + '</span>';

        header.addEventListener('click', function() {
            section.classList.toggle('collapsed');
            var chevron = header.querySelector('.section-chevron');
            chevron.className = 'bi section-chevron ' +
                (section.classList.contains('collapsed') ? 'bi-chevron-right' : 'bi-chevron-down');
        });

        var children = document.createElement('div');
        children.className = 'file-tree-section-children';

        section.appendChild(header);
        section.appendChild(children);
        return { section: section, children: children };
    }

    // System files section
    if (this.fileTree.system && this.fileTree.system.length > 0) {
        var s = createSection('System (read-only)', false);
        this.fileTree.system.forEach(function(file) {
            s.children.appendChild(self._createFileItem(file, true));
        });
        container.appendChild(s.section);
    }

    // Proxy files section
    if (this.fileTree.proxy && this.fileTree.proxy.length > 0) {
        var s = createSection('OM Proxy (read-only)', false);
        this.fileTree.proxy.forEach(function(file) {
            s.children.appendChild(self._createFileItem(file, true));
        });
        container.appendChild(s.section);
    }

    // Plugins section (read-only tree)
    if (this.fileTree.plugins && this.fileTree.plugins.children && this.fileTree.plugins.children.length > 0) {
        var s = createSection('Plugins (read-only)', false);
        this._renderReadonlyFolder(this.fileTree.plugins, s.children);
        container.appendChild(s.section);
    }

    // Drafts section (only when drafts exist)
    if (this.drafts.length > 0) {
        var s = createSection('Drafts (' + this.drafts.length + ')', true);
        s.section.classList.add('draft-section');
        this.drafts.forEach(function(draft) {
            s.children.appendChild(self._createDraftItem(draft));
        });
        container.appendChild(s.section);
    }

    // Modules section (expanded by default)
    if (this.fileTree.modules) {
        var s = createSection('Modules', true);
        this._renderFolder(this.fileTree.modules, s.children);
        container.appendChild(s.section);
    }

    // Backups section (collapsed, grouped by file path)
    if (this.backups.length > 0) {
        var s = createSection('Backups (' + this.backups.length + ')', false);
        s.section.classList.add('backup-section');
        // Add clear button to header
        var clearBtn = document.createElement('button');
        clearBtn.className = 'btn btn-sm btn-outline-warning backup-clear-btn';
        clearBtn.title = 'Delete all backups';
        clearBtn.innerHTML = '<i class="bi bi-trash3"></i>';
        clearBtn.addEventListener('click', function(e) {
            e.stopPropagation();
            self.clearBackups();
        });
        s.section.querySelector('.file-tree-section-header').appendChild(clearBtn);
        self._renderBackupTree(this.backups, s.children);
        container.appendChild(s.section);
    }
};

/**
 * Render folder recursively
 * @private
 */
LuaEditor.prototype._renderFolder = function(folder, container) {
    var self = this;
    if (!folder.children) return;

    folder.children.forEach(function(item) {
        if (item.type === 'dir') {
            var folderEl = document.createElement('div');
            folderEl.className = 'file-tree-folder';

            var header = document.createElement('div');
            header.className = 'file-tree-item folder-item';
            header.dataset.folderPath = item.path;
            header.innerHTML =
                '<i class="bi bi-chevron-down folder-chevron"></i>' +
                '<i class="bi bi-folder-fill text-warning"></i>' +
                '<span class="file-name">' + VCLU.esc(item.name) + '</span>' +
                '<button class="btn btn-link delete-btn" data-action="delete-folder" data-path="' + VCLU.esc(item.path) + '" title="Delete folder">' +
                    '<i class="bi bi-trash"></i>' +
                '</button>';

            header.addEventListener('click', function(e) {
                if (e.target.closest('.delete-btn')) return;

                // Toggle folder expand/collapse
                if (e.target.closest('.folder-chevron')) {
                    folderEl.classList.toggle('collapsed');
                    var chevron = header.querySelector('.folder-chevron');
                    chevron.className = folderEl.classList.contains('collapsed')
                        ? 'bi bi-chevron-right folder-chevron'
                        : 'bi bi-chevron-down folder-chevron';
                } else {
                    // Select this folder
                    self.selectFolder(item.path);
                }
            });

            var children = document.createElement('div');
            children.className = 'file-tree-children';
            self._renderFolder(item, children);

            folderEl.appendChild(header);
            folderEl.appendChild(children);
            container.appendChild(folderEl);
        } else {
            container.appendChild(self._createFileItem(item, false));
        }
    });
};

/**
 * Render read-only folder tree (for plugins) — no delete buttons, no folder selection
 * @private
 */
LuaEditor.prototype._renderReadonlyFolder = function(folder, container) {
    var self = this;
    if (!folder.children) return;

    folder.children.forEach(function(item) {
        if (item.type === 'dir') {
            var folderEl = document.createElement('div');
            folderEl.className = 'file-tree-folder collapsed';

            var header = document.createElement('div');
            header.className = 'file-tree-item folder-item readonly';
            header.innerHTML =
                '<i class="bi bi-chevron-right folder-chevron"></i>' +
                '<i class="bi bi-folder-fill text-warning"></i>' +
                '<span class="file-name">' + VCLU.esc(item.name) + '</span>';

            header.addEventListener('click', function() {
                folderEl.classList.toggle('collapsed');
                var chevron = header.querySelector('.folder-chevron');
                chevron.className = folderEl.classList.contains('collapsed')
                    ? 'bi bi-chevron-right folder-chevron'
                    : 'bi bi-chevron-down folder-chevron';
            });

            var children = document.createElement('div');
            children.className = 'file-tree-children';
            self._renderReadonlyFolder(item, children);

            folderEl.appendChild(header);
            folderEl.appendChild(children);
            container.appendChild(folderEl);
        } else {
            container.appendChild(self._createFileItem(item, true));
        }
    });
};

/**
 * Select a folder for new file/folder creation
 * @param {string} path
 */
LuaEditor.prototype.selectFolder = function(path) {
    this.selectedFolder = path;

    // Update visual selection
    document.querySelectorAll('.folder-item.selected').forEach(function(el) {
        el.classList.remove('selected');
    });

    var folderEl = document.querySelector('.folder-item[data-folder-path="' + path + '"]');
    if (folderEl) {
        folderEl.classList.add('selected');
    }

    // Update status to show selected folder
    if (this.elements.status) this.elements.status.textContent = 'Folder: ' + path;
};

/**
 * Get the current selected folder path
 * @returns {string}
 */
LuaEditor.prototype.getSelectedFolder = function() {
    return this.selectedFolder || 'modules';
};

/**
 * Create file item element
 * @private
 */
LuaEditor.prototype._createFileItem = function(file, readonly) {
    var self = this;
    var item = document.createElement('div');
    item.className = 'file-tree-item' + (readonly ? ' readonly' : '');
    item.dataset.path = file.path;

    var icon = readonly ? 'bi-file-lock' : 'bi-file-code';
    var renameBtn = readonly ? '' :
        '<button class="btn btn-link rename-btn" data-action="rename-file" data-path="' + VCLU.esc(file.path) + '" title="Rename file">' +
            '<i class="bi bi-pencil"></i>' +
        '</button>';
    var deleteBtn = readonly ? '' :
        '<button class="btn btn-link delete-btn" data-action="delete-file" data-path="' + VCLU.esc(file.path) + '" title="Delete file">' +
            '<i class="bi bi-trash"></i>' +
        '</button>';

    var draftBadge = '';
    if (!readonly && this.drafts.some(function(d) { return d.path === file.path; })) {
        draftBadge = '<span class="draft-badge" title="Has pending draft">●</span>';
    }

    item.innerHTML =
        '<i class="bi ' + icon + '"></i>' +
        '<span class="file-name">' + VCLU.esc(file.name) + draftBadge + '</span>' +
        '<span class="file-size">' + this._formatSize(file.size) + '</span>' +
        renameBtn + deleteBtn;

    item.addEventListener('click', function(e) {
        if (e.target.closest('.delete-btn') || e.target.closest('.rename-btn')) return;
        self.loadFile(file.path, readonly);
    });

    return item;
};

/**
 * Create draft item element for the Drafts tree section
 * @private
 */
LuaEditor.prototype._createDraftItem = function(draft) {
    var self = this;
    var item = document.createElement('div');
    item.className = 'file-tree-item draft-item';
    item.dataset.path = 'draft:' + draft.path;

    // Display path without 'modules/' prefix
    var displayName = draft.path.replace(/^modules\//, '');

    item.innerHTML =
        '<i class="bi bi-file-diff"></i>' +
        '<span class="file-name">' + VCLU.esc(displayName) + '</span>' +
        '<span class="file-size">' + this._formatSize(draft.size) + '</span>';

    item.addEventListener('click', function() {
        self.loadDraft(draft.path);
    });

    return item;
};

/**
 * Render backup items grouped by file path
 * @private
 */
LuaEditor.prototype._renderBackupTree = function(backups, container) {
    var self = this;

    // Group by file path
    var groups = {};
    backups.forEach(function(b) {
        if (!groups[b.path]) groups[b.path] = [];
        groups[b.path].push(b);
    });

    // Sort group keys
    var paths = Object.keys(groups).sort();

    paths.forEach(function(filePath) {
        var items = groups[filePath];
        var displayPath = filePath.replace(/^modules\//, '');

        // Collapsible file group
        var groupEl = document.createElement('div');
        groupEl.className = 'file-tree-folder collapsed';

        var header = document.createElement('div');
        header.className = 'file-tree-item folder-item backup-folder';
        header.innerHTML =
            '<i class="bi bi-chevron-right folder-chevron"></i>' +
            '<i class="bi bi-archive text-muted"></i>' +
            '<span class="file-name">' + VCLU.esc(displayPath) + '</span>' +
            '<span class="file-size" style="opacity:0.5">' + items.length + '</span>';

        header.addEventListener('click', function() {
            groupEl.classList.toggle('collapsed');
            var chevron = header.querySelector('.folder-chevron');
            chevron.className = groupEl.classList.contains('collapsed')
                ? 'bi bi-chevron-right folder-chevron'
                : 'bi bi-chevron-down folder-chevron';
        });

        var children = document.createElement('div');
        children.className = 'file-tree-children';

        items.forEach(function(backup) {
            var item = document.createElement('div');
            item.className = 'file-tree-item backup-item';
            item.dataset.backupName = backup.name;

            var created = new Date(backup.created);
            var dateStr = created.toLocaleDateString('pl-PL', { day: '2-digit', month: '2-digit' });
            var timeStr = created.toLocaleTimeString('pl-PL', { hour: '2-digit', minute: '2-digit', second: '2-digit' });

            item.innerHTML =
                '<i class="bi bi-clock-history text-muted"></i>' +
                '<span class="file-name">' + dateStr + ', ' + timeStr + '</span>' +
                '<span class="file-size">' + self._formatSize(backup.size) + '</span>';

            item.addEventListener('click', function() {
                self.loadBackup(backup.name, backup.path);
            });

            children.appendChild(item);
        });

        groupEl.appendChild(header);
        groupEl.appendChild(children);
        container.appendChild(groupEl);
    });
};

/**
 * Load a backup into the diff editor (backup vs current production)
 * @param {string} backupName - Backup file name
 * @param {string} filePath - Original file path
 */
LuaEditor.prototype.loadBackup = function(backupName, filePath) {
    var self = this;
    var tabPath = 'backup:' + backupName;

    // If already open, just switch
    var existing = this._findTab(tabPath);
    if (existing) {
        this._switchToTab(tabPath);
        return;
    }

    this.elements.status.textContent = 'Loading backup...';

    // Fetch backup content and current production content
    Promise.all([
        fetch('/api/mcp/workspace/backup/' + encodeURIComponent(backupName)).then(function(r) { return r.json(); }),
        fetch('/api/editor/file/' + filePath).then(function(r) { return r.json(); }).catch(function() { return { content: '' }; })
    ])
    .then(function(results) {
        var backupData = results[0];
        var prodData = results[1];

        if (backupData.error) {
            alert('Failed to load backup: ' + backupData.error);
            self.elements.status.textContent = 'Error';
            return;
        }

        // Save current tab viewState before switching
        if (self.activeTabPath) {
            var oldTab = self._findTab(self.activeTabPath);
            if (oldTab && oldTab.type !== 'draft' && oldTab.type !== 'backup' && self.editor) {
                oldTab.viewState = self.editor.saveViewState();
                oldTab.content = self.editor.getValue();
            }
        }

        // Parse date from backup name for tab title
        var dateLabel = backupName;
        if (backupName.length > 15) {
            var y = backupName.substring(0, 4);
            var mo = backupName.substring(4, 6);
            var d = backupName.substring(6, 8);
            var h = backupName.substring(9, 11);
            var mi = backupName.substring(11, 13);
            dateLabel = d + '.' + mo + ' ' + h + ':' + mi;
        }

        var fileName = filePath.split('/').pop();

        self.openTabs.push({
            path: tabPath,
            name: fileName + ' (' + dateLabel + ')',
            type: 'backup',
            backupName: backupName,
            backupPath: filePath,
            backupContent: backupData.content || '',
            prodContent: prodData.content || '',
            readonly: true,
            modified: false
        });

        self._switchToTab(tabPath);
        self.elements.status.textContent = 'Backup: ' + filePath;
    })
    .catch(function(error) {
        alert('Failed to load backup: ' + error.message);
        self.elements.status.textContent = 'Error';
    });
};

/**
 * Restore the active backup to production
 */
LuaEditor.prototype.restoreBackup = function() {
    var tab = this._findTab(this.activeTabPath);
    if (!tab || tab.type !== 'backup') return;

    if (!confirm('Restore this backup to production?\n\nFile: ' + tab.backupPath + '\nThis will overwrite the current production file and reload Lua runtime.')) return;

    var self = this;

    fetch('/api/mcp/workspace/rollback', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ backupName: tab.backupName })
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to restore: ' + data.error);
            return;
        }

        // Close the backup tab
        self._removeTab(tab.path);
        self.loadTree();

        // Show output
        self._showOutputPanel('output');
        if (self.elements.outputContent) self.elements.outputContent.innerHTML +=
            '<span class="run-cmd">\u2713 Restored: ' + VCLU.esc(tab.backupPath) +
            ' from backup ' + VCLU.esc(tab.backupName) + '</span>\n';
    })
    .catch(function(error) {
        alert('Failed to restore: ' + error.message);
    });
};

/**
 * Clear all backups
 */
LuaEditor.prototype.clearBackups = function() {
    if (!confirm('Delete all ' + this.backups.length + ' backups?\n\nThis cannot be undone.')) return;

    var self = this;
    fetch('/api/mcp/workspace/backups', { method: 'DELETE' })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                alert('Failed to clear backups: ' + data.error);
                return;
            }
            // Close any open backup tabs
            self.tabs.forEach(function(tab) {
                if (tab.type === 'backup') self._removeTab(tab.path);
            });
            self.loadTree();
        })
        .catch(function(error) {
            alert('Failed to clear backups: ' + error.message);
        });
};

/**
 * Load file into editor
 * @param {string} path
 * @param {boolean} readonly
 */
LuaEditor.prototype.loadFile = function(path, readonly) {
    var self = this;

    // If already open in a tab, just switch
    var existing = this._findTab(path);
    if (existing) {
        this._switchToTab(path);
        return;
    }

    if (this.elements.status) this.elements.status.textContent = 'Loading ' + path + '...';

    fetch('/api/editor/file/' + path)
        .then(function(response) { return response.json(); })
        .then(function(data) {
            if (data.error) {
                if (self.editor) self.editor.setValue('-- Error: ' + data.error);
                if (self.elements.status) self.elements.status.textContent = 'Error';
                return;
            }

            var isReadonly = readonly || data.readonly;
            var fileName = path.split('/').pop();

            // Save current tab viewState before switching
            if (self.activeTabPath) {
                var oldTab = self._findTab(self.activeTabPath);
                if (oldTab && oldTab.type !== 'draft' && self.editor) {
                    oldTab.viewState = self.editor.saveViewState();
                }
            }

            // Add new tab
            self.openTabs.push({
                path: path,
                name: fileName,
                readonly: isReadonly,
                content: data.content,
                viewState: null,
                modified: false,
                type: 'file'
            });

            // Ensure normal editor is visible (may have been on diff)
            if (self.elements.container) self.elements.container.style.display = '';
            if (self.elements.diffContainer) self.elements.diffContainer.style.display = 'none';

            self.activeTabPath = path;
            self.currentFile = path;
            self.currentFileReadonly = isReadonly;
            if (self.editor) {
                self._suppressChangeEvent = true;
                self.editor.setValue(data.content);
                self._suppressChangeEvent = false;
                self.editor.updateOptions({ readOnly: isReadonly });

                // Set language based on file extension
                var lang = path.endsWith('.css') ? 'css' : 'lua';
                var model = self.editor.getModel();
                if (model) monaco.editor.setModelLanguage(model, lang);
            }

            // Update active state in tree
            document.querySelectorAll('.file-tree-item').forEach(function(item) {
                item.classList.toggle('active', item.dataset.path === path);
            });

            self.setModified(false);
            self._renderTabs();
            self._updateToolbarForTab(self._findTab(path));

            // Hide no-files message
            if (self.elements.noFilesMessage) {
                self.elements.noFilesMessage.style.display = 'none';
            }

            var langLabel = (path.endsWith('.css') ? 'css' : 'lua').toUpperCase();
            var lines = data.content.split('\n').length;
            if (self.elements.status) self.elements.status.textContent = path;
            if (self.elements.info) self.elements.info.textContent = langLabel + ' | ' + lines + ' lines | ' + self._formatSize(data.size);
        })
        .catch(function(error) {
            console.error('Failed to load file:', error.message, '\n', error.stack);
            if (self.editor) self.editor.setValue('-- Error loading file: ' + error.message + '\n-- ' + (error.stack || '').split('\n').slice(0, 5).join('\n-- '));
            if (self.elements.status) self.elements.status.textContent = 'Error';
        });
};

/**
 * Load a draft into the diff editor
 * @param {string} path - Draft path (e.g. "modules/init.lua")
 */
LuaEditor.prototype.loadDraft = function(path) {
    var self = this;
    var tabPath = 'draft:' + path;

    // If already open, just switch
    var existing = this._findTab(tabPath);
    if (existing) {
        this._switchToTab(tabPath);
        return;
    }

    this.elements.status.textContent = 'Loading draft ' + path + '...';

    fetch('/api/mcp/workspace/diff/' + encodeURIComponent(path))
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                alert('Failed to load draft: ' + data.error);
                self.elements.status.textContent = 'Error';
                return;
            }

            // Save current tab viewState before switching
            if (self.activeTabPath) {
                var oldTab = self._findTab(self.activeTabPath);
                if (oldTab && oldTab.type !== 'draft' && self.editor) {
                    oldTab.viewState = self.editor.saveViewState();
                    oldTab.content = self.editor.getValue();
                }
            }

            var fileName = path.split('/').pop();

            self.openTabs.push({
                path: tabPath,
                name: fileName,
                type: 'draft',
                draftPath: path,
                draftContent: data.draft || '',
                prodContent: data.production || '',
                readonly: true,
                modified: false
            });

            self._switchToTab(tabPath);
            self.elements.status.textContent = 'Draft: ' + path;
        })
        .catch(function(error) {
            console.error('Failed to load draft:', error);
            alert('Failed to load draft: ' + error.message);
            self.elements.status.textContent = 'Error';
        });
};

/**
 * Save current file
 */
LuaEditor.prototype.saveFile = function() {
    var self = this;

    if (!this.currentFile || this.currentFileReadonly) return;

    var content = this.editor.getValue();
    this.elements.status.textContent = 'Saving...';

    fetch('/api/editor/file/' + this.currentFile, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: content })
    })
    .then(function(response) { return response.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to save: ' + data.error);
            self.elements.status.textContent = 'Error saving';
            return;
        }

        // Update tab content
        var tab = self._findTab(self.currentFile);
        if (tab) tab.content = content;

        self.setModified(false);
        self.elements.status.textContent = 'Saved!';
        setTimeout(function() {
            self.elements.status.textContent = self.currentFile;
        }, 2000);

        // Refresh tree to update sizes
        self.loadTree();
    })
    .catch(function(error) {
        console.error('Failed to save:', error);
        alert('Failed to save: ' + error.message);
        self.elements.status.textContent = 'Error saving';
    });
};

/**
 * Save all modified tabs
 */
LuaEditor.prototype.saveAll = function() {
    var self = this;

    // Save current editor content to active tab first
    if (this.activeTabPath) {
        var activeTab = this._findTab(this.activeTabPath);
        if (activeTab && !activeTab.readonly) {
            activeTab.content = this.editor.getValue();
        }
    }

    // Collect modified, non-readonly tabs
    var toSave = this.openTabs.filter(function(tab) {
        return tab.modified && !tab.readonly;
    });

    if (toSave.length === 0) {
        this.elements.status.textContent = 'Nothing to save';
        setTimeout(function() {
            self.elements.status.textContent = self.currentFile || 'Ready';
        }, 1500);
        return;
    }

    this.elements.status.textContent = 'Saving ' + toSave.length + ' file(s)...';

    var saved = 0;
    var errors = [];

    toSave.forEach(function(tab) {
        fetch('/api/editor/file/' + tab.path, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content: tab.content })
        })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                errors.push(tab.name + ': ' + data.error);
            } else {
                tab.modified = false;
                saved++;
            }
        })
        .catch(function(err) {
            errors.push(tab.name + ': ' + err.message);
        })
        .finally(function() {
            if (saved + errors.length === toSave.length) {
                // All done
                self._renderTabs();
                if (self.activeTabPath) {
                    var active = self._findTab(self.activeTabPath);
                    if (active) {
                        self.isModified = active.modified;
                        self.elements.saveBtn.disabled = !active.modified;
                    }
                }
                if (errors.length > 0) {
                    alert('Some files failed to save:\n' + errors.join('\n'));
                }
                self.elements.status.textContent = 'Saved ' + saved + '/' + toSave.length + ' files';
                setTimeout(function() {
                    self.elements.status.textContent = self.currentFile || 'Ready';
                }, 2000);
                self.loadTree();
            }
        });
    });
};

/**
 * Create new file
 * @param {string} filename
 */
LuaEditor.prototype.createFile = function(filename) {
    var self = this;

    if (!filename) return;

    var hasExt = /\.(lua|css)$/i.test(filename);
    var name = hasExt ? filename : filename + '.lua';
    var basePath = this.getSelectedFolder();
    var path = basePath + '/' + name;
    var defaultContent = name.endsWith('.css')
        ? '/* ' + name + ' — custom dashboard styles */\n\n'
        : '-- ' + name + '\n\n';

    fetch('/api/editor/file', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            path: path,
            content: defaultContent
        })
    })
    .then(function(response) { return response.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to create file: ' + data.error);
            return;
        }

        self.loadTree();
        self.loadFile(path, false);
    })
    .catch(function(error) {
        alert('Failed to create file: ' + error.message);
    });
};

/**
 * Create new folder
 * @param {string} foldername
 */
LuaEditor.prototype.createFolder = function(foldername) {
    var self = this;

    if (!foldername) return;

    var basePath = this.getSelectedFolder();
    var path = basePath + '/' + foldername;

    fetch('/api/editor/folder', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: path })
    })
    .then(function(response) { return response.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to create folder: ' + data.error);
            return;
        }

        self.loadTree();
    })
    .catch(function(error) {
        alert('Failed to create folder: ' + error.message);
    });
};

/**
 * Delete file
 * @param {string} path
 */
LuaEditor.prototype.deleteFile = function(path) {
    var self = this;

    if (!confirm('Delete ' + path + '?')) return;

    fetch('/api/editor/file/' + path, { method: 'DELETE' })
        .then(function(response) { return response.json(); })
        .then(function(data) {
            if (data.error) {
                alert('Failed to delete: ' + data.error);
                return;
            }

            // Close tab if open
            self._removeTab(path);
            self.loadTree();
        })
        .catch(function(error) {
            alert('Failed to delete: ' + error.message);
        });
};

/**
 * Delete folder
 * @param {string} path
 */
LuaEditor.prototype.deleteFolder = function(path) {
    var self = this;

    if (!confirm('Delete folder ' + path + '? (must be empty)')) return;

    fetch('/api/editor/folder/' + path, { method: 'DELETE' })
        .then(function(response) { return response.json(); })
        .then(function(data) {
            if (data.error) {
                alert('Failed to delete: ' + data.error);
                return;
            }
            self.loadTree();
        })
        .catch(function(error) {
            alert('Failed to delete: ' + error.message);
        });
};

/**
 * Reload Lua runtime
 */
LuaEditor.prototype.reloadLua = function() {
    var self = this;

    if (!confirm('Reload Lua runtime? This will restart all scripts.')) return;

    // Show logs tab so user sees reload progress
    this._showOutputPanel('logs');

    this.elements.status.textContent = 'Reloading Lua...';

    fetch('/api/lua/reload', { method: 'POST' })
        .then(function(response) { return response.json(); })
        .then(function(data) {
            if (data.error) {
                alert('Failed to reload: ' + data.error);
                self.elements.status.textContent = 'Reload failed';
                return;
            }

            self.elements.status.textContent = 'Lua reloaded!';
            setTimeout(function() {
                self.elements.status.textContent = self.currentFile || 'Ready';
            }, 3000);
        })
        .catch(function(error) {
            alert('Failed to reload: ' + error.message);
            self.elements.status.textContent = 'Reload failed';
        });
};

/**
 * Format file size for display
 * @private
 */
LuaEditor.prototype._formatSize = function(bytes) {
    if (!bytes) return '0 B';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
};

/**
 * Handle delegated click events
 */
LuaEditor.prototype.handleTreeAction = function(e) {
    var btn = e.target.closest('[data-action]');
    if (!btn) return;

    var action = btn.dataset.action;
    var path = btn.dataset.path;

    e.stopPropagation();

    if (action === 'delete-file') {
        this.deleteFile(path);
    } else if (action === 'delete-folder') {
        this.deleteFolder(path);
    } else if (action === 'rename-file') {
        var fileName = path.split('/').pop();
        document.getElementById('renameFileOldPath').value = path;
        document.getElementById('renameFileName').value = fileName;
        new bootstrap.Modal(document.getElementById('renameFileModal')).show();
    }
};

// --- Tab Management ---

/**
 * Find tab by path
 * @private
 */
LuaEditor.prototype._findTab = function(path) {
    for (var i = 0; i < this.openTabs.length; i++) {
        if (this.openTabs[i].path === path) return this.openTabs[i];
    }
    return null;
};

/**
 * Switch to an existing tab
 * @private
 */
LuaEditor.prototype._switchToTab = function(path) {
    if (path === this.activeTabPath) return;

    // Save current tab viewState (only for non-draft tabs)
    if (this.activeTabPath) {
        var oldTab = this._findTab(this.activeTabPath);
        if (oldTab && oldTab.type !== 'draft' && oldTab.type !== 'history' && oldTab.type !== 'backup' && this.editor) {
            oldTab.viewState = this.editor.saveViewState();
            oldTab.content = this.editor.getValue();
        }
    }

    var tab = this._findTab(path);
    if (!tab) return;

    this.activeTabPath = path;
    this.isModified = tab.modified;

    var el = this.elements;

    if (tab.type === 'history') {
        // Show history, hide editors
        if (el.container) el.container.style.display = 'none';
        if (el.diffContainer) el.diffContainer.style.display = 'none';
        if (el.historyContainer) el.historyContainer.style.display = '';

        this.currentFile = null;
        this.currentFileReadonly = true;
        if (el.saveBtn) el.saveBtn.disabled = true;
        if (el.status) el.status.textContent = 'History';
        if (el.info) el.info.textContent = '';

        this._loadHistory();
    } else if (tab.type === 'draft' || tab.type === 'backup') {
        // Show diff editor, hide normal editor + history
        if (el.container) el.container.style.display = 'none';
        if (el.diffContainer) el.diffContainer.style.display = '';
        if (el.historyContainer) el.historyContainer.style.display = 'none';

        this.currentFile = null;
        this.currentFileReadonly = true;

        // Create diff editor lazily
        if (!this.diffEditor && el.diffContainer) {
            this.diffEditor = monaco.editor.createDiffEditor(el.diffContainer, {
                readOnly: true,
                renderSideBySide: true,
                minimap: { enabled: false },
                originalEditable: false,
                enableSplitViewResizing: true,
                automaticLayout: true,
                theme: VCLU.isDark() ? 'vs-dark' : 'vs',
                scrollBeyondLastLine: false
            });
        }

        // Dispose previous diff models
        if (this.diffEditor) {
            var prevModel = this.diffEditor.getModel();
            if (prevModel) {
                if (prevModel.original) prevModel.original.dispose();
                if (prevModel.modified) prevModel.modified.dispose();
            }
        }

        if (tab.type === 'backup') {
            if (this.diffEditor) {
                this.diffEditor.setModel({
                    original: monaco.editor.createModel(tab.backupContent, 'lua'),
                    modified: monaco.editor.createModel(tab.prodContent, 'lua')
                });
            }
            if (el.status) el.status.textContent = 'Backup: ' + tab.backupPath;
            if (el.info) el.info.textContent = 'Backup \u2190 vs Production \u2192';
        } else {
            if (this.diffEditor) {
                this.diffEditor.setModel({
                    original: monaco.editor.createModel(tab.prodContent, 'lua'),
                    modified: monaco.editor.createModel(tab.draftContent, 'lua')
                });
            }
            if (el.status) el.status.textContent = 'Draft: ' + tab.draftPath;
            if (el.info) el.info.textContent = 'Diff';
        }

        if (el.saveBtn) el.saveBtn.disabled = true;
    } else {
        // Show normal editor, hide diff editor + history
        if (el.container) el.container.style.display = '';
        if (el.diffContainer) el.diffContainer.style.display = 'none';
        if (el.historyContainer) el.historyContainer.style.display = 'none';

        this.currentFile = path;
        this.currentFileReadonly = tab.readonly;

        if (this.editor) {
            this._suppressChangeEvent = true;
            this.editor.setValue(tab.content);
            this._suppressChangeEvent = false;
            this.editor.updateOptions({ readOnly: tab.readonly });

            if (tab.viewState) {
                this.editor.restoreViewState(tab.viewState);
            }
        }

        if (el.saveBtn) el.saveBtn.disabled = !tab.modified;

        var lines = tab.content.split('\n').length;
        if (el.status) el.status.textContent = path;
        if (el.info) el.info.textContent = 'Lua | ' + lines + ' lines';
    }

    // Update active state in tree
    document.querySelectorAll('.file-tree-item').forEach(function(item) {
        item.classList.toggle('active', item.dataset.path === path);
    });

    this._renderTabs();
    this._updateToolbarForTab(tab);
};

/**
 * Close a tab
 * @private
 */
LuaEditor.prototype._closeTab = function(path) {
    var tab = this._findTab(path);
    if (!tab) return;

    if (tab.modified && !confirm('Unsaved changes in ' + tab.name + '. Close anyway?')) {
        return;
    }

    var idx = this.openTabs.indexOf(tab);
    this.openTabs.splice(idx, 1);

    if (path === this.activeTabPath) {
        if (this.openTabs.length > 0) {
            // Switch to neighbor
            var newIdx = Math.min(idx, this.openTabs.length - 1);
            this.activeTabPath = null; // Force _switchToTab to run
            this._switchToTab(this.openTabs[newIdx].path);
        } else {
            // No tabs left — show normal editor
            this.activeTabPath = null;
            this.currentFile = null;
            this.currentFileReadonly = false;
            this.isModified = false;
            if (this.elements.container) this.elements.container.style.display = '';
            if (this.elements.diffContainer) this.elements.diffContainer.style.display = 'none';
            if (this.elements.historyContainer) this.elements.historyContainer.style.display = 'none';
            this._suppressChangeEvent = true;
            if (this.editor) this.editor.setValue('-- Select a file to edit');
            this._suppressChangeEvent = false;
            if (this.editor) this.editor.updateOptions({ readOnly: true });
            if (this.elements.saveBtn) this.elements.saveBtn.disabled = true;
            if (this.elements.status) this.elements.status.textContent = 'Ready';
            if (this.elements.info) this.elements.info.textContent = 'Lua';
            this._renderBreadcrumbs(null);
            this._updateToolbarForTab(null);
            document.querySelectorAll('.file-tree-item').forEach(function(item) {
                item.classList.remove('active');
            });
        }
    }

    this._renderTabs();
};

/**
 * Remove tab without confirmation (for file deletion)
 * @private
 */
LuaEditor.prototype._removeTab = function(path) {
    var tab = this._findTab(path);
    if (!tab) return;

    var idx = this.openTabs.indexOf(tab);
    this.openTabs.splice(idx, 1);

    if (path === this.activeTabPath) {
        if (this.openTabs.length > 0) {
            var newIdx = Math.min(idx, this.openTabs.length - 1);
            this.activeTabPath = null; // Force _switchToTab to run
            this._switchToTab(this.openTabs[newIdx].path);
        } else {
            this.activeTabPath = null;
            this.currentFile = null;
            this.elements.container.style.display = '';
            this.elements.diffContainer.style.display = 'none';
            this.elements.historyContainer.style.display = 'none';
            this._suppressChangeEvent = true;
            this.editor.setValue('-- File deleted');
            this._suppressChangeEvent = false;
            this.editor.updateOptions({ readOnly: true });
            this.elements.saveBtn.disabled = true;
            this._renderBreadcrumbs(null);
            this._updateToolbarForTab(null);
        }
    }

    this._renderTabs();
};

/**
 * Render tabs bar
 * @private
 */
LuaEditor.prototype._renderTabs = function() {
    var self = this;
    var bar = this.elements.tabsBar;
    if (!bar) return;

    bar.innerHTML = '';

    this.openTabs.forEach(function(tab) {
        var el = document.createElement('div');
        var classes = 'editor-tab';
        if (tab.path === self.activeTabPath) classes += ' active';
        if (tab.type === 'draft') classes += ' draft-tab';
        if (tab.type === 'backup') classes += ' backup-tab';
        el.className = classes;

        var icon = '';
        if (tab.type === 'draft') icon = '<i class="bi bi-file-diff" style="font-size:0.75rem;margin-right:3px;color:#27C2A8"></i>';
        else if (tab.type === 'history') icon = '<i class="bi bi-clock-history" style="font-size:0.75rem;margin-right:3px"></i>';
        else if (tab.type === 'backup') icon = '<i class="bi bi-archive" style="font-size:0.75rem;margin-right:3px;color:var(--bs-warning)"></i>';
        var label = icon + VCLU.esc(tab.name);
        if (tab.modified) label += ' <span class="tab-modified">*</span>';
        if (tab.readonly && tab.type !== 'draft') label += ' <span style="opacity:0.5;font-size:0.7em">RO</span>';

        el.innerHTML = label +
            '<button class="close-tab" title="Close">&times;</button>';

        el.addEventListener('click', function(e) {
            if (e.target.closest('.close-tab')) {
                self._closeTab(tab.path);
            } else {
                self._switchToTab(tab.path);
            }
        });

        bar.appendChild(el);
    });
};

// --- Breadcrumbs ---

/**
 * Render breadcrumbs for a file path
 * @private
 */
LuaEditor.prototype._renderBreadcrumbs = function(path) {
    var el = this.elements.breadcrumbs;
    if (!el) return;

    if (!path) {
        el.innerHTML = '<span class="breadcrumb-segment" style="opacity:0.5">Select a file</span>';
        return;
    }

    var self = this;
    var parts = path.split('/');
    var html = '';

    for (var i = 0; i < parts.length; i++) {
        if (i > 0) html += '<span class="breadcrumb-sep">&rsaquo;</span>';
        var isLast = (i === parts.length - 1);
        var segment = VCLU.esc(parts[i]);
        if (isLast) {
            html += '<span class="breadcrumb-segment" style="font-weight:500">' + segment + '</span>';
            if (this.currentFileReadonly) {
                html += ' <span class="badge bg-secondary" style="font-size:0.65rem">read-only</span>';
            }
        } else {
            html += '<span class="breadcrumb-segment">' + segment + '</span>';
        }
    }

    el.innerHTML = html;
};

/**
 * Update toolbar buttons for the active tab (normal vs draft)
 * @private
 * @param {Object|null} tab
 */
LuaEditor.prototype._updateToolbarForTab = function(tab) {
    var isDraft = tab && tab.type === 'draft';
    var isBackup = tab && tab.type === 'backup';
    var isHistory = tab && tab.type === 'history';
    var hideNormal = isDraft || isHistory || isBackup;
    var el = this.elements;

    // Helper: safely set display
    function show(elem, visible) {
        if (elem) elem.style.display = visible ? '' : 'none';
    }

    // Normal buttons
    show(el.saveBtn, !hideNormal);
    show(el.saveAllBtn, !hideNormal);
    show(el.btnReload, !hideNormal);
    show(el.historyBtn, !isHistory);

    // Draft buttons
    show(el.validateDraftBtn, isDraft);
    show(el.testDraftBtn, isDraft);
    show(el.promoteBtn, isDraft);
    show(el.deleteDraftBtn, isDraft);

    // Backup buttons
    show(el.restoreBackupBtn, isBackup);

    // Breadcrumbs
    if (!el.breadcrumbs) return;
    if (isHistory) {
        el.breadcrumbs.innerHTML =
            '<span class="breadcrumb-segment" style="font-weight:500"><i class="bi bi-clock-history"></i> History</span>';
    } else if (isBackup) {
        var backupFile = tab.backupPath.split('/').pop();
        el.breadcrumbs.innerHTML =
            '<span class="breadcrumb-segment" style="color:var(--bs-warning);font-weight:500"><i class="bi bi-archive"></i> Backup</span>' +
            '<span class="breadcrumb-sep">&rsaquo;</span>' +
            '<span class="breadcrumb-segment" style="font-weight:500">' + VCLU.esc(backupFile) + '</span>' +
            '<span class="breadcrumb-sep">&rsaquo;</span>' +
            '<span class="breadcrumb-segment" style="opacity:0.7">' + VCLU.esc(tab.name.match(/\(([^)]+)\)/)?.[1] || '') + '</span>';
    } else if (isDraft) {
        var fileName = tab.draftPath.split('/').pop();
        el.breadcrumbs.innerHTML =
            '<span class="breadcrumb-segment" style="color:#27C2A8;font-weight:500">Draft</span>' +
            '<span class="breadcrumb-sep">&rsaquo;</span>' +
            '<span class="breadcrumb-segment" style="font-weight:500">' + VCLU.esc(fileName) + '</span>';
    } else if (tab) {
        this._renderBreadcrumbs(tab.path);
    }
};

/**
 * Promote the active draft to production
 */
LuaEditor.prototype.promoteDraft = function() {
    var tab = this._findTab(this.activeTabPath);
    if (!tab || tab.type !== 'draft') return;

    if (!confirm('Promote to production? Current file will be backed up.')) return;

    var self = this;
    var draftPath = tab.draftPath;

    fetch('/api/mcp/workspace/promote', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: draftPath })
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to promote: ' + data.error);
            return;
        }

        // Close the draft tab
        self._removeTab(tab.path);
        self.loadTree();

        // Show output
        self._showOutputPanel('output');
        self.elements.outputContent.innerHTML +=
            '<span class="run-cmd">\u2713 Promoted: ' + VCLU.esc(draftPath) + '</span>\n';
    })
    .catch(function(error) {
        alert('Failed to promote: ' + error.message);
    });
};

/**
 * Delete the active draft
 */
LuaEditor.prototype.deleteDraft = function() {
    var tab = this._findTab(this.activeTabPath);
    if (!tab || tab.type !== 'draft') return;

    if (!confirm('Delete this draft?')) return;

    var self = this;
    var draftPath = tab.draftPath;

    fetch('/api/mcp/workspace/drafts/' + encodeURIComponent(draftPath), {
        method: 'DELETE'
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to delete draft: ' + data.error);
            return;
        }

        self._removeTab(tab.path);
        self.loadTree();
    })
    .catch(function(error) {
        alert('Failed to delete draft: ' + error.message);
    });
};

/**
 * Validate the active draft file (syntax + blocklist) and show markers
 */
LuaEditor.prototype.validateDraft = function() {
    var tab = this._findTab(this.activeTabPath);
    if (!tab || tab.type !== 'draft') return;

    var self = this;
    var draftPath = tab.draftPath;

    this.elements.status.textContent = 'Validating...';
    this.elements.validateDraftBtn.disabled = true;

    fetch('/api/mcp/workspace/validate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: draftPath })
    })
    .then(function(r) { return r.json(); })
    .then(function(report) {
        self.elements.validateDraftBtn.disabled = false;

        if (report.error) {
            self._showOutputPanel('output');
            self.elements.outputContent.innerHTML =
                '<span class="run-error">Validate error: ' + VCLU.esc(report.error) + '</span>';
            self.elements.status.textContent = 'Validation error';
            return;
        }

        // Set markers on the modified (draft) model of the diff editor
        var model = self._getDraftModel();
        if (model) {
            var markers = self._buildMarkers(report.errors, report.warnings);
            monaco.editor.setModelMarkers(model, 'lua-validate', markers);
        }

        // Show result in output panel
        self._showOutputPanel('output');
        var html = '';
        if (report.valid) {
            html += '<span class="run-cmd">\u2713 Validation passed: ' + VCLU.esc(draftPath) + '</span>\n';
        } else {
            html += '<span class="run-error">\u2717 Validation failed: ' + VCLU.esc(draftPath) + '</span>\n';
        }
        (report.errors || []).forEach(function(msg) {
            var parsed = self._parseValidationMessage(msg);
            html += self._formatClickableError(parsed, draftPath) + '\n';
        });
        (report.warnings || []).forEach(function(msg) {
            var parsed = self._parseValidationMessage(msg);
            html += '<span class="run-cmd">  \u26a0 Line ' + parsed.line + ': ' + VCLU.esc(parsed.message) + '</span>\n';
        });
        self.elements.outputContent.innerHTML = html;

        self.elements.status.textContent = report.valid ? 'Valid' : 'Validation failed';
        setTimeout(function() {
            self.elements.status.textContent = 'Draft: ' + draftPath;
        }, 3000);
    })
    .catch(function(error) {
        self.elements.validateDraftBtn.disabled = false;
        self._showOutputPanel('output');
        self.elements.outputContent.innerHTML =
            '<span class="run-error">Validate error: ' + VCLU.esc(error.message) + '</span>';
        self.elements.status.textContent = 'Validation error';
    });
};

/**
 * Test the active draft in the sandbox and show results + markers
 */
LuaEditor.prototype.testDraft = function() {
    var tab = this._findTab(this.activeTabPath);
    if (!tab || tab.type !== 'draft') return;

    var self = this;
    var draftPath = tab.draftPath;

    this.elements.status.textContent = 'Testing in sandbox...';
    this.elements.testDraftBtn.disabled = true;

    fetch('/api/mcp/workspace/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: draftPath })
    })
    .then(function(r) { return r.json(); })
    .then(function(result) {
        self.elements.testDraftBtn.disabled = false;

        if (result.error) {
            self._showOutputPanel('output');
            self.elements.outputContent.innerHTML =
                '<span class="run-error">Test error: ' + VCLU.esc(result.error) + '</span>';
            self.elements.status.textContent = 'Test error';
            return;
        }

        // Set error markers on the draft model
        var model = self._getDraftModel();
        if (model && result.errors && result.errors.length > 0) {
            var markers = self._buildMarkers(result.errors, []);
            monaco.editor.setModelMarkers(model, 'lua-test', markers);
        } else if (model) {
            monaco.editor.setModelMarkers(model, 'lua-test', []);
        }

        // Build output
        self._showOutputPanel('output');
        var html = '';

        if (result.success) {
            html += '<span class="run-cmd">\u2713 Sandbox test passed: ' + VCLU.esc(draftPath) +
                    ' (' + (result.duration_ms || 0) + 'ms)</span>\n';
        } else {
            html += '<span class="run-error">\u2717 Sandbox test failed: ' + VCLU.esc(draftPath) +
                    ' (' + (result.duration_ms || 0) + 'ms)</span>\n';
        }

        // Errors (clickable)
        if (result.errors && result.errors.length > 0) {
            html += '\n<span style="font-weight:600">Errors:</span>\n';
            result.errors.forEach(function(msg) {
                var parsed = self._parseValidationMessage(msg);
                html += self._formatClickableError(parsed, draftPath) + '\n';
            });
        }

        // State changes
        if (result.state_changes && result.state_changes.length > 0) {
            html += '\n<span style="font-weight:600">State changes:</span>\n';
            result.state_changes.forEach(function(sc) {
                html += '  <span class="run-cmd">' + VCLU.esc(sc.path) +
                        '</span>: ' + JSON.stringify(sc.old_value) + ' \u2192 ' + JSON.stringify(sc.new_value) + '\n';
            });
        }

        // Blocked calls
        if (result.blocked_calls && result.blocked_calls.length > 0) {
            html += '\n<span style="font-weight:600">Blocked calls:</span>\n';
            result.blocked_calls.forEach(function(bc) {
                html += '  <span style="opacity:0.7">' + VCLU.esc(bc.type) +
                        '</span> ' + VCLU.esc(JSON.stringify(bc.args)) + '\n';
            });
        }

        // Logs
        if (result.logs && result.logs.length > 0) {
            html += '\n<span style="font-weight:600">Logs:</span>\n';
            result.logs.forEach(function(log) {
                html += '  ' + VCLU.esc(log) + '\n';
            });
        }

        self.elements.outputContent.innerHTML = html;

        self.elements.status.textContent = result.success ? 'Test passed' : 'Test failed';
        setTimeout(function() {
            self.elements.status.textContent = 'Draft: ' + draftPath;
        }, 3000);
    })
    .catch(function(error) {
        self.elements.testDraftBtn.disabled = false;
        self._showOutputPanel('output');
        self.elements.outputContent.innerHTML =
            '<span class="run-error">Test error: ' + VCLU.esc(error.message) + '</span>';
        self.elements.status.textContent = 'Test error';
    });
};

/**
 * Get the draft (modified) model from the diff editor, or null
 * @private
 * @returns {monaco.editor.ITextModel|null}
 */
LuaEditor.prototype._getDraftModel = function() {
    if (!this.diffEditor) return null;
    var diffModel = this.diffEditor.getModel();
    return diffModel ? diffModel.modified : null;
};

/**
 * Build Monaco markers from error and warning messages
 * @private
 */
LuaEditor.prototype._buildMarkers = function(errors, warnings) {
    var self = this;
    var markers = [];

    (errors || []).forEach(function(msg) {
        var parsed = self._parseValidationMessage(msg);
        markers.push({
            severity: monaco.MarkerSeverity.Error,
            message: parsed.message,
            startLineNumber: parsed.line,
            startColumn: parsed.col,
            endLineNumber: parsed.line,
            endColumn: parsed.col + 20
        });
    });

    (warnings || []).forEach(function(msg) {
        var parsed = self._parseValidationMessage(msg);
        markers.push({
            severity: monaco.MarkerSeverity.Warning,
            message: parsed.message,
            startLineNumber: parsed.line,
            startColumn: 1,
            endLineNumber: parsed.line,
            endColumn: 1000
        });
    });

    return markers;
};

/**
 * Format an error as a clickable HTML element that jumps to the line in the diff editor
 * @private
 */
LuaEditor.prototype._formatClickableError = function(parsed, filePath) {
    return '<span class="run-error output-error-link" data-line="' + parsed.line + '" data-col="' + parsed.col +
           '" style="cursor:pointer;text-decoration:underline">' +
           '  \u2717 Line ' + parsed.line + ':' + parsed.col + ': ' +
           VCLU.esc(parsed.message) + '</span>';
};

/**
 * Setup click handler on output panel for clickable error lines
 * @private
 */
LuaEditor.prototype._setupOutputErrorClicks = function() {
    var self = this;
    if (!this.elements.outputContent) return;
    this.elements.outputContent.addEventListener('click', function(e) {
        var link = e.target.closest('.output-error-link');
        if (!link) return;

        var line = parseInt(link.dataset.line, 10);
        var col = parseInt(link.dataset.col, 10) || 1;
        if (!line) return;

        // Jump in the diff editor's modified side
        if (self.diffEditor) {
            var modifiedEditor = self.diffEditor.getModifiedEditor();
            if (modifiedEditor) {
                modifiedEditor.revealLineInCenter(line);
                modifiedEditor.setPosition({ lineNumber: line, column: col });
                modifiedEditor.focus();
            }
        }
    });
};

// --- Syntax Validation ---

/**
 * Setup debounced validation on editor content changes
 * @private
 */
LuaEditor.prototype._setupValidation = function() {
    var self = this;

    this.editor.onDidChangeModelContent(function() {
        if (self.currentFileReadonly) return;
        if (self._validateTimer) clearTimeout(self._validateTimer);
        self._validateTimer = setTimeout(function() {
            self._validateContent();
        }, 500);
    });
};

/**
 * Validate current editor content via backend
 * @private
 */
LuaEditor.prototype._validateContent = function() {
    var self = this;
    if (!this.editor || this.currentFileReadonly) return;

    var content = this.editor.getValue();
    if (!content || content.length < 3) {
        monaco.editor.setModelMarkers(this.editor.getModel(), 'lua', []);
        return;
    }

    fetch('/api/editor/validate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: content })
    })
    .then(function(r) { return r.json(); })
    .then(function(report) {
        var markers = [];

        (report.errors || []).forEach(function(msg) {
            var parsed = self._parseValidationMessage(msg);
            markers.push({
                severity: monaco.MarkerSeverity.Error,
                message: parsed.message,
                startLineNumber: parsed.line,
                startColumn: parsed.col,
                endLineNumber: parsed.line,
                endColumn: parsed.col + 20
            });
        });

        (report.warnings || []).forEach(function(msg) {
            var parsed = self._parseValidationMessage(msg);
            markers.push({
                severity: monaco.MarkerSeverity.Warning,
                message: parsed.message,
                startLineNumber: parsed.line,
                startColumn: 1,
                endLineNumber: parsed.line,
                endColumn: 1000
            });
        });

        monaco.editor.setModelMarkers(self.editor.getModel(), 'lua', markers);
    })
    .catch(function() {
        // Silent fail — don't disrupt editing
    });
};

/**
 * Parse line/col from validation error message
 * Formats: "syntax error: draft:LINE:COL: message" or "line N: message"
 * @private
 */
LuaEditor.prototype._parseValidationMessage = function(msg) {
    // gopher-lua format: "syntax error: draft:LINE:COL: message"
    var m = msg.match(/:(\d+):(\d+):\s*(.+)/);
    if (m) return { line: parseInt(m[1], 10), col: parseInt(m[2], 10), message: m[3] };

    // "line N: message" format
    var m2 = msg.match(/line\s+(\d+):\s*(.+)/i);
    if (m2) return { line: parseInt(m2[1], 10), col: 1, message: m2[2] };

    // Fallback
    return { line: 1, col: 1, message: msg };
};

// --- Search in Files ---

/**
 * Toggle search panel visibility
 */
LuaEditor.prototype.toggleSearchPanel = function(show) {
    var panel = this.elements.searchPanel;
    if (!panel) return;

    if (typeof show === 'undefined') {
        show = !panel.classList.contains('visible');
    }

    panel.classList.toggle('visible', show);
    if (show) {
        var self = this;
        this.elements.searchInput.focus();
        // Wire up input listener if not already done
        if (!this._searchWired) {
            this._searchWired = true;
            this.elements.searchInput.addEventListener('input', function() {
                var q = self.elements.searchInput.value.trim();
                if (self._searchTimer) clearTimeout(self._searchTimer);
                if (q.length < 2) {
                    self.elements.searchResults.innerHTML = '';
                    return;
                }
                self._searchTimer = setTimeout(function() {
                    self._performSearch(q);
                }, 300);
            });
        }
    }
};

/**
 * Perform search via backend
 * @private
 */
LuaEditor.prototype._performSearch = function(query) {
    var self = this;

    fetch('/api/editor/search?q=' + encodeURIComponent(query))
        .then(function(r) { return r.json(); })
        .then(function(results) {
            self._renderSearchResults(results || []);
        })
        .catch(function() {
            self.elements.searchResults.innerHTML = '<div style="padding:4px;opacity:0.5">Search failed</div>';
        });
};

/**
 * Render search results
 * @private
 */
LuaEditor.prototype._renderSearchResults = function(results) {
    var self = this;
    var container = this.elements.searchResults;
    container.innerHTML = '';

    if (results.length === 0) {
        container.innerHTML = '<div style="padding:4px;opacity:0.5">No results</div>';
        return;
    }

    results.forEach(function(r) {
        var item = document.createElement('div');
        item.className = 'search-result-item';

        var fileName = r.path.split('/').pop();
        item.innerHTML =
            '<span class="search-result-path">' + VCLU.esc(fileName) + ':' + r.lineNumber + '</span>' +
            '<span class="search-result-line">' + VCLU.esc(r.line) + '</span>';

        item.addEventListener('click', function() {
            self.loadFile(r.path, false);
            // After file loads, reveal line
            setTimeout(function() {
                if (self.editor && self.currentFile === r.path) {
                    self.editor.revealLineInCenter(r.lineNumber);
                    self.editor.setPosition({ lineNumber: r.lineNumber, column: 1 });
                    self.editor.focus();
                }
            }, 200);
        });

        container.appendChild(item);
    });
};

// --- Rename File ---

/**
 * Rename file via backend
 */
LuaEditor.prototype.renameFile = function(oldPath, newName) {
    var self = this;

    if (!newName) return;
    if (!/\.(lua|css)$/i.test(newName)) newName += '.lua';

    // Build new path: same directory, new name
    var parts = oldPath.split('/');
    parts[parts.length - 1] = newName;
    var newPath = parts.join('/');

    fetch('/api/editor/rename', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ oldPath: oldPath, newPath: newPath })
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            alert('Failed to rename: ' + data.error);
            return;
        }

        // Update tab if open
        var tab = self._findTab(oldPath);
        if (tab) {
            tab.path = newPath;
            tab.name = newName;
            if (self.activeTabPath === oldPath) {
                self.activeTabPath = newPath;
                self.currentFile = newPath;
                self._renderBreadcrumbs(newPath);
                self.elements.status.textContent = newPath;
            }
            self._renderTabs();
        }

        self.loadTree();
    })
    .catch(function(error) {
        alert('Failed to rename: ' + error.message);
    });
};

// --- Run Function (CodeLens + Run Selection) ---

/**
 * Register CodeLens provider that shows "▶ Run" above global functions
 * @private
 */
LuaEditor.prototype._registerRunCodeLens = function() {
    var self = this;

    // Register global commands that CodeLens will invoke on click
    monaco.editor.registerCommand('lua.run', function(accessor, funcName) {
        self._runFunction(funcName + '()');
    });
    monaco.editor.registerCommand('lua.runWithArgs', function(accessor, funcName, args) {
        var input = prompt('Arguments for ' + funcName + '(' + args + '):', '');
        if (input === null) return;
        self._runFunction(funcName + '(' + input + ')');
    });

    monaco.languages.registerCodeLensProvider('lua', {
        provideCodeLenses: function(model) {
            var lenses = [];
            var lineCount = model.getLineCount();

            for (var i = 1; i <= lineCount; i++) {
                var line = model.getLineContent(i);

                // Skip local functions (not callable from outside)
                if (/^\s*local\s+function/.test(line)) continue;

                // Match global function definitions: function name(args)
                var m = line.match(/^function\s+(\w[\w.:]*)?\s*\(([^)]*)\)/);
                if (!m) continue;

                var funcName = m[1] || '';
                if (!funcName) continue;
                var args = (m[2] || '').trim();
                var hasArgs = args.length > 0;

                lenses.push({
                    range: {
                        startLineNumber: i, startColumn: 1,
                        endLineNumber: i, endColumn: 1
                    },
                    command: {
                        id: hasArgs ? 'lua.runWithArgs' : 'lua.run',
                        title: hasArgs ? '\u25b6 Run...' : '\u25b6 Run',
                        arguments: [funcName, args]
                    }
                });
            }
            return { lenses: lenses, dispose: function() {} };
        },
        resolveCodeLens: function(model, codeLens) {
            return codeLens;
        }
    });
};

/**
 * Run a Lua script via the backend and show result in output panel
 * @param {string} script - Lua code to execute
 */
LuaEditor.prototype._runFunction = function(script) {
    var self = this;
    var panel = this.elements.outputPanel;
    var content = this.elements.outputContent;
    if (!content) return;

    this._showOutputPanel('output');
    content.innerHTML = '<span style="opacity:0.5">Running: ' + VCLU.esc(script) + '...</span>';

    fetch('/api/editor/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ script: script })
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        if (data.error) {
            content.innerHTML =
                '<span class="run-cmd">\u25b6 ' + VCLU.esc(script) + '</span>\n' +
                '<span class="run-error">Error: ' + VCLU.esc(data.error) + '</span>';
        } else {
            var result = data.result;
            if (result === undefined || result === null || result === '') result = '(nil)';
            content.innerHTML =
                '<span class="run-cmd">\u25b6 ' + VCLU.esc(script) + '</span>\n' +
                VCLU.esc(String(result));
        }
    })
    .catch(function(err) {
        content.innerHTML =
            '<span class="run-cmd">\u25b6 ' + VCLU.esc(script) + '</span>\n' +
            '<span class="run-error">Network error: ' + VCLU.esc(err.message) + '</span>';
    });
};

/**
 * Run selected text or current line (Ctrl+Shift+Enter)
 * @private
 */
LuaEditor.prototype._runSelection = function() {
    if (!this.editor || !this.currentFile) return;

    var selection = this.editor.getSelection();
    var model = this.editor.getModel();
    var script;

    if (selection && !selection.isEmpty()) {
        script = model.getValueInRange(selection);
    } else {
        // Run current line
        var lineNumber = this.editor.getPosition().lineNumber;
        script = model.getLineContent(lineNumber);
    }

    script = script.trim();
    if (!script) return;

    this._runFunction(script);
};

/**
 * Setup drag-to-resize on the output panel
 * @private
 */
LuaEditor.prototype._setupOutputResize = function() {
    var handle = document.getElementById('outputResize');
    var panel = this.elements.outputPanel;
    if (!handle || !panel) return;

    var startY, startH;

    function onMouseMove(e) {
        var delta = startY - e.clientY;
        var newH = Math.min(Math.max(startH + delta, 60), window.innerHeight * 0.5);
        panel.style.height = newH + 'px';
    }

    function onMouseUp() {
        handle.classList.remove('dragging');
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
        document.body.style.userSelect = '';
        document.body.style.cursor = '';
    }

    handle.addEventListener('mousedown', function(e) {
        e.preventDefault();
        startY = e.clientY;
        startH = panel.offsetHeight;
        handle.classList.add('dragging');
        document.body.style.userSelect = 'none';
        document.body.style.cursor = 'ns-resize';
        document.addEventListener('mousemove', onMouseMove);
        document.addEventListener('mouseup', onMouseUp);
    });

    // Click on output header when collapsed → expand
    var outputHeader = panel.querySelector('.editor-output-header');
    if (outputHeader) {
        outputHeader.addEventListener('click', function(e) {
            if (panel.classList.contains('collapsed') && !e.target.closest('button')) {
                panel.classList.remove('collapsed');
            }
        });
    }
};

// --- Output Panel Tabs (Output + Lua Logs) ---

/**
 * Switch between Output and Logs tabs
 */
LuaEditor.prototype.switchOutputTab = function(tabName) {
    this._activeOutputTab = tabName;

    // Expand panel if collapsed
    if (this.elements.outputPanel) this.elements.outputPanel.classList.remove('collapsed');

    // Update tab buttons
    document.querySelectorAll('[data-output-tab]').forEach(function(btn) {
        btn.classList.toggle('active', btn.dataset.outputTab === tabName);
    });

    // Toggle content panels
    if (this.elements.outputContent) this.elements.outputContent.style.display = (tabName === 'output') ? '' : 'none';
    if (this.elements.logsContent) this.elements.logsContent.style.display = (tabName === 'logs') ? '' : 'none';

    // Connect/disconnect SSE for logs
    if (tabName === 'logs') {
        this._connectLogStream();
    } else {
        this._disconnectLogStream();
    }
};

/**
 * Toggle the output panel open/closed
 */
LuaEditor.prototype.closeOutputPanel = function() {
    var panel = this.elements.outputPanel;
    if (!panel) return;
    if (panel.classList.contains('collapsed')) {
        panel.classList.remove('collapsed');
        // Reconnect logs if on logs tab
        if (this._activeOutputTab === 'logs') {
            this._connectLogStream();
        }
    } else {
        panel.classList.add('collapsed');
        this._disconnectLogStream();
    }
};

/**
 * Clear the active output tab
 */
LuaEditor.prototype.clearOutputTab = function() {
    if (this._activeOutputTab === 'output') {
        if (this.elements.outputContent) this.elements.outputContent.innerHTML = '';
    } else {
        if (this.elements.logsContent) this.elements.logsContent.innerHTML = '';
    }
};

/**
 * Show the output panel, optionally switching to a tab
 */
LuaEditor.prototype._showOutputPanel = function(tabName) {
    if (this.elements.outputPanel) this.elements.outputPanel.classList.remove('collapsed');
    if (tabName) this.switchOutputTab(tabName);
};

/**
 * Connect to SSE debug stream and filter for [LUA] messages
 * @private
 */
LuaEditor.prototype._connectLogStream = function() {
    if (this._logEventSource) return; // already connected

    var self = this;
    var logsEl = this.elements.logsContent;

    this._logEventSource = new EventSource('/api/debug/stream');

    this._logEventSource.onmessage = function(e) {
        try {
            var entry = JSON.parse(e.data);
            // Filter: only LUA source
            if (!entry.source || entry.source.toUpperCase() !== 'LUA') return;

            var time = '';
            if (entry.time) {
                var d = new Date(entry.time);
                time = d.toLocaleTimeString('en-GB', { hour12: false });
            }

            var line = document.createElement('div');
            line.className = 'log-line';
            if (entry.level === 'error') line.className += ' log-error';
            if (entry.level === 'warning') line.className += ' log-warning';

            line.innerHTML =
                '<span class="log-time">' + VCLU.esc(time) + '</span> ' +
                '<span class="log-msg">' + VCLU.esc(entry.message) + '</span>';

            logsEl.appendChild(line);

            // Auto-scroll to bottom
            logsEl.scrollTop = logsEl.scrollHeight;

            // Limit to 500 lines
            while (logsEl.children.length > 500) {
                logsEl.removeChild(logsEl.firstChild);
            }
        } catch (err) {
            // ignore parse errors
        }
    };

    this._logEventSource.onerror = function() {
        // Will auto-reconnect via EventSource
    };
};

/**
 * Disconnect SSE log stream
 * @private
 */
LuaEditor.prototype._disconnectLogStream = function() {
    if (this._logEventSource) {
        this._logEventSource.close();
        this._logEventSource = null;
    }
};

/**
 * Load autocompletion data
 * @private
 */
LuaEditor.prototype._loadCompletions = function() {
    var self = this;

    fetch('/static/js/lua-completions.json')
        .then(function(response) { return response.json(); })
        .then(function(data) {
            self.completions = data;
            self._registerCompletionProvider();
            self._registerHoverProvider();
            self._registerDefinitionProvider();
            console.log('Loaded completions:', Object.keys(data.methods).length, 'modules');
        })
        .catch(function(error) {
            console.error('Failed to load completions:', error);
        });
};

/**
 * Register Monaco completion provider
 * @private
 */
LuaEditor.prototype._registerCompletionProvider = function() {
    var self = this;

    monaco.languages.registerCompletionItemProvider('lua', {
        triggerCharacters: ['.', ':', '"', "'"],

        provideCompletionItems: function(model, position) {
            var textUntilPosition = model.getValueInRange({
                startLineNumber: position.lineNumber,
                startColumn: 1,
                endLineNumber: position.lineNumber,
                endColumn: position.column
            });

            var word = model.getWordUntilPosition(position);
            var range = {
                startLineNumber: position.lineNumber,
                endLineNumber: position.lineNumber,
                startColumn: word.startColumn,
                endColumn: word.endColumn
            };

            // Check if we're inside a string argument for registry functions
            var registrySuggestions = self._getRegistrySuggestions(textUntilPosition, range);
            if (registrySuggestions) {
                return registrySuggestions;
            }

            // Check if we're after a "." or ":"
            var match = textUntilPosition.match(/(\w+)[.:]$/);
            if (match) {
                var objName = match[1];
                var methods = self.completions.methods[objName] || [];

                // If no methods found for simple name, try type inference
                if (methods.length === 0) {
                    var inferredType = self._inferReturnType(textUntilPosition);
                    if (!inferredType) {
                        // Fall back to symbol table lookup
                        inferredType = self._symbolTable[objName] || null;
                    }
                    if (inferredType) {
                        methods = self.completions.methods[inferredType] || [];
                    }
                }

                if (methods.length > 0) {
                    return {
                        suggestions: methods.map(function(m) {
                            var kind = m.kind === 'constant'
                                ? monaco.languages.CompletionItemKind.Constant
                                : monaco.languages.CompletionItemKind.Method;
                            return {
                                label: m.label,
                                kind: kind,
                                detail: m.detail || '',
                                documentation: m.documentation || '',
                                insertText: m.insertText || m.label,
                                insertTextRules: m.insertTextRules || 0,
                                range: range
                            };
                        })
                    };
                }
            }

            // Also check for function call patterns like _:get("path"):
            var funcCallMatch = textUntilPosition.match(/\)[.:]$/);
            if (funcCallMatch) {
                var inferredType = self._inferReturnType(textUntilPosition);
                if (inferredType) {
                    var methods = self.completions.methods[inferredType] || [];
                    if (methods.length > 0) {
                        return {
                            suggestions: methods.map(function(m) {
                                var kind = m.kind === 'constant'
                                    ? monaco.languages.CompletionItemKind.Constant
                                    : monaco.languages.CompletionItemKind.Method;
                                return {
                                    label: m.label,
                                    kind: kind,
                                    detail: m.detail || '',
                                    documentation: m.documentation || '',
                                    insertText: m.insertText || m.label,
                                    insertTextRules: m.insertTextRules || 0,
                                    range: range
                                };
                            })
                        };
                    }
                }
            }

            // Global completions (modules, functions, keywords)
            var suggestions = [];

            // Add globals (modules and functions)
            if (self.completions.globals) {
                self.completions.globals.forEach(function(g) {
                    var kind;
                    switch (g.kind) {
                        case 'module':
                            kind = monaco.languages.CompletionItemKind.Module;
                            break;
                        case 'function':
                            kind = monaco.languages.CompletionItemKind.Function;
                            break;
                        default:
                            kind = monaco.languages.CompletionItemKind.Variable;
                    }

                    suggestions.push({
                        label: g.label,
                        kind: kind,
                        detail: g.detail || '',
                        documentation: g.documentation || '',
                        insertText: g.insertText || g.label,
                        insertTextRules: g.insertTextRules || 0,
                        range: range
                    });
                });
            }

            // Add keywords
            if (self.completions.keywords) {
                self.completions.keywords.forEach(function(kw) {
                    suggestions.push({
                        label: kw,
                        kind: monaco.languages.CompletionItemKind.Keyword,
                        insertText: kw,
                        range: range
                    });
                });
            }

            return { suggestions: suggestions };
        }
    });
};

/**
 * Load registry data for dynamic completions
 * @private
 */
LuaEditor.prototype._loadRegistryData = function() {
    var self = this;

    fetch('/api/registry/list')
        .then(function(response) { return response.json(); })
        .then(function(data) {
            // Parse registry data - extract paths, names, tags, and types
            self.registryData = {
                paths: [],
                names: [],
                tags: [],
                objects: data || [],
                pathToType: {},  // Map path -> type for type inference
                nameToType: {}   // Map name -> type for type inference
            };

            // Extract unique values and build type maps
            var tagSet = {};
            (data || []).forEach(function(obj) {
                if (obj.path) {
                    self.registryData.paths.push(obj.path);
                    if (obj.type) {
                        self.registryData.pathToType[obj.path] = obj.type;
                    }
                }
                if (obj.name) {
                    self.registryData.names.push(obj.name);
                    if (obj.type) {
                        self.registryData.nameToType[obj.name] = obj.type;
                    }
                }
                if (obj.tags && Array.isArray(obj.tags)) {
                    obj.tags.forEach(function(tag) {
                        tagSet[tag] = true;
                    });
                }
            });

            self.registryData.tags = Object.keys(tagSet);
            console.log('Loaded registry:', self.registryData.paths.length, 'objects,', self.registryData.tags.length, 'tags');
        })
        .catch(function(error) {
            console.error('Failed to load registry data:', error);
            self.registryData = { paths: [], names: [], tags: [], objects: [], pathToType: {}, nameToType: {} };
        });
};

/**
 * Get registry suggestions for string arguments
 * @private
 */
LuaEditor.prototype._getRegistrySuggestions = function(textUntilPosition, range) {
    if (!this.registryData) return null;

    // Patterns for registry functions with string arguments
    // _:get("..., _.get("..., etc.
    var getPattern = /_[.:](get|inspect|sync|event)\s*\(\s*["']([^"']*)$/;
    var findPattern = /_[.:]find\s*\(\s*["']([^"']*)$/;
    var tagPattern = /_[.:]byTags?\s*\(\s*["']([^"']*)$/;

    var match;

    // Check for _.get, _.inspect, _.sync, _.event - suggest paths and names
    match = textUntilPosition.match(getPattern);
    if (match) {
        var partial = match[2] || '';
        var suggestions = [];

        // Add paths
        this.registryData.paths.forEach(function(path) {
            if (path.toLowerCase().indexOf(partial.toLowerCase()) !== -1 || partial === '') {
                suggestions.push({
                    label: path,
                    kind: monaco.languages.CompletionItemKind.Reference,
                    detail: 'Path',
                    insertText: path,
                    range: {
                        startLineNumber: range.startLineNumber,
                        endLineNumber: range.endLineNumber,
                        startColumn: range.endColumn - partial.length,
                        endColumn: range.endColumn
                    }
                });
            }
        });

        // Add names (shorter identifiers)
        this.registryData.names.forEach(function(name) {
            if (name.toLowerCase().indexOf(partial.toLowerCase()) !== -1 || partial === '') {
                suggestions.push({
                    label: name,
                    kind: monaco.languages.CompletionItemKind.Value,
                    detail: 'Name',
                    insertText: name,
                    range: {
                        startLineNumber: range.startLineNumber,
                        endLineNumber: range.endLineNumber,
                        startColumn: range.endColumn - partial.length,
                        endColumn: range.endColumn
                    }
                });
            }
        });

        return { suggestions: suggestions };
    }

    // Check for _.find - suggest pattern examples
    match = textUntilPosition.match(findPattern);
    if (match) {
        var partial = match[1] || '';
        var suggestions = [];

        // Suggest common patterns based on actual paths
        var patternExamples = {};
        this.registryData.paths.forEach(function(path) {
            var parts = path.split('.');
            if (parts.length >= 2) {
                // CLU prefix pattern
                patternExamples[parts[0] + '%%.'] = 'All objects in ' + parts[0];
                // Object type pattern
                var objType = parts[1].replace(/[0-9]+/g, '');
                if (objType) {
                    patternExamples['%.' + objType] = 'All ' + objType + ' objects';
                }
            }
        });

        Object.keys(patternExamples).forEach(function(pattern) {
            if (pattern.indexOf(partial) !== -1 || partial === '') {
                suggestions.push({
                    label: pattern,
                    kind: monaco.languages.CompletionItemKind.Snippet,
                    detail: patternExamples[pattern],
                    insertText: pattern,
                    range: {
                        startLineNumber: range.startLineNumber,
                        endLineNumber: range.endLineNumber,
                        startColumn: range.endColumn - partial.length,
                        endColumn: range.endColumn
                    }
                });
            }
        });

        // Also add exact paths
        this.registryData.paths.forEach(function(path) {
            if (path.toLowerCase().indexOf(partial.toLowerCase()) !== -1) {
                suggestions.push({
                    label: path,
                    kind: monaco.languages.CompletionItemKind.Reference,
                    detail: 'Path',
                    insertText: path.replace(/([.%])/g, '%%$1'), // Escape for Lua pattern
                    range: {
                        startLineNumber: range.startLineNumber,
                        endLineNumber: range.endLineNumber,
                        startColumn: range.endColumn - partial.length,
                        endColumn: range.endColumn
                    }
                });
            }
        });

        return { suggestions: suggestions };
    }

    // Check for _.byTag, _.byTags - suggest tags
    match = textUntilPosition.match(tagPattern);
    if (match) {
        var partial = match[1] || '';
        var suggestions = [];

        this.registryData.tags.forEach(function(tag) {
            if (tag.toLowerCase().indexOf(partial.toLowerCase()) !== -1 || partial === '') {
                suggestions.push({
                    label: tag,
                    kind: monaco.languages.CompletionItemKind.Enum,
                    detail: 'Tag',
                    insertText: tag,
                    range: {
                        startLineNumber: range.startLineNumber,
                        endLineNumber: range.endLineNumber,
                        startColumn: range.endColumn - partial.length,
                        endColumn: range.endColumn
                    }
                });
            }
        });

        return { suggestions: suggestions };
    }

    return null;
};

/**
 * Infer return type from registry function calls
 * Detects patterns like: _.get("path"). or _.byTag("tag").
 * @private
 * @param {string} textUntilPosition - Text before cursor
 * @returns {string|null} - Inferred type name or null
 */
LuaEditor.prototype._inferReturnType = function(textUntilPosition) {
    if (!this.registryData) return null;

    // Pattern: _:get("path") or _.get('path') followed by . or :
    var getMatch = textUntilPosition.match(/_[.:]get\s*\(\s*["']([^"']+)["']\s*\)[.:]$/);
    if (getMatch) {
        var pathOrName = getMatch[1];
        // Look up by path first, then by name
        var objType = this.registryData.pathToType[pathOrName] || this.registryData.nameToType[pathOrName];
        if (objType) {
            return objType;
        }
        // Fallback: return generic RemoteObject methods
        return 'RemoteObject';
    }

    // Pattern: _:byTag("tag") or _.byTags("tag1", "tag2") followed by . or :
    var tagMatch = textUntilPosition.match(/_[.:]byTags?\s*\([^)]+\)[.:]$/);
    if (tagMatch) {
        return 'RemoteGroup';
    }

    // Pattern: _:find("pattern") - returns table, suggest table methods
    var findMatch = textUntilPosition.match(/_[.:]find\s*\([^)]+\)[.:]$/);
    if (findMatch) {
        // _.find returns a table (array), not a single object
        return null; // No specific methods - user should iterate
    }

    return null;
};

/**
 * Rebuild symbol table from editor content (debounced, not per-keystroke)
 * Scans for local VAR = _:get("path") and local VAR = _:byTag(...) patterns
 * @private
 * @param {Object} model - Monaco editor model
 */
LuaEditor.prototype._rebuildSymbolTable = function(model) {
    if (!this.registryData || !model) return;

    var table = {};
    var lineCount = model.getLineCount();

    var getPattern = /local\s+(\w+)\s*=\s*_[.:]get\s*\(\s*["']([^"']+)["']\s*\)/;
    var tagPattern = /local\s+(\w+)\s*=\s*_[.:]byTags?\s*\(/;

    for (var i = 1; i <= lineCount; i++) {
        var line = model.getLineContent(i);

        var getMatch = line.match(getPattern);
        if (getMatch) {
            var varName = getMatch[1];
            var pathOrName = getMatch[2];
            var objType = this.registryData.pathToType[pathOrName] || this.registryData.nameToType[pathOrName];
            table[varName] = objType || 'RemoteObject';
            continue;
        }

        var tagMatch = line.match(tagPattern);
        if (tagMatch) {
            table[tagMatch[1]] = 'RemoteGroup';
        }
    }

    this._symbolTable = table;
};

/**
 * Setup symbol table rebuild listener on editor content changes
 * @private
 */
LuaEditor.prototype._setupSymbolTableListener = function() {
    var self = this;
    var timer = null;

    this.editor.onDidChangeModelContent(function() {
        if (timer) clearTimeout(timer);
        timer = setTimeout(function() {
            self._rebuildSymbolTable(self.editor.getModel());
        }, 300);
    });
};

/**
 * Resolve the symbol at a given position to a completion entry.
 * Returns {entry, moduleName} or null.
 * @private
 */
LuaEditor.prototype._resolveSymbolAt = function(model, position) {
    if (!this.completions) return null;

    var word = model.getWordAtPosition(position);
    if (!word) return null;

    var wordText = word.word;
    var lineContent = model.getLineContent(position.lineNumber);
    var beforeWord = lineContent.substring(0, word.startColumn - 1);

    // Check for Module.method or Module:method
    var dotMatch = beforeWord.match(/(\w+)[.:]$/);
    if (dotMatch) {
        var moduleName = dotMatch[1];
        var methods = this.completions.methods[moduleName];

        // Try symbol table type inference
        if (!methods || methods.length === 0) {
            var inferredType = this._symbolTable[moduleName];
            if (inferredType) {
                moduleName = inferredType;
                methods = this.completions.methods[moduleName];
            }
        }

        if (methods) {
            for (var i = 0; i < methods.length; i++) {
                if (methods[i].label === wordText) {
                    return { entry: methods[i], moduleName: moduleName };
                }
            }
        }
        return null;
    }

    // Check globals
    if (this.completions.globals) {
        for (var i = 0; i < this.completions.globals.length; i++) {
            if (this.completions.globals[i].label === wordText) {
                return { entry: this.completions.globals[i], moduleName: null };
            }
        }
    }

    return null;
};

/**
 * Register Monaco hover provider — shows documentation on mouse hover
 * @private
 */
LuaEditor.prototype._registerHoverProvider = function() {
    var self = this;

    monaco.languages.registerHoverProvider('lua', {
        provideHover: function(model, position) {
            var resolved = self._resolveSymbolAt(model, position);
            if (!resolved) return null;

            var entry = resolved.entry;
            var word = model.getWordAtPosition(position);
            if (!word) return null;

            var title = resolved.moduleName
                ? resolved.moduleName + '.' + entry.label
                : entry.label;

            var contents = [];
            if (entry.detail) {
                contents.push({ value: '```lua\n' + title + ' ' + entry.detail + '\n```' });
            } else {
                contents.push({ value: '```lua\n' + title + '\n```' });
            }
            if (entry.documentation) {
                contents.push({ value: entry.documentation });
            }

            return {
                range: new monaco.Range(
                    position.lineNumber, word.startColumn,
                    position.lineNumber, word.endColumn
                ),
                contents: contents
            };
        }
    });
};

/**
 * Get or create an in-memory Monaco model with formatted documentation
 * for a given module name.
 * @private
 */
LuaEditor.prototype._getDocModel = function(moduleName) {
    if (!this._docModels) this._docModels = {};
    if (this._docModels[moduleName]) return this._docModels[moduleName];

    var uri = monaco.Uri.parse('inmemory://docs/' + moduleName + '.lua');
    var existing = monaco.editor.getModel(uri);
    if (existing) {
        this._docModels[moduleName] = existing;
        return existing;
    }

    var lines = [];
    lines.push('-- ' + moduleName + ' — API Reference');
    lines.push('-- ' + Array(moduleName.length + 21).join('='));
    lines.push('');

    // Module description from globals
    var globalEntry = null;
    if (this.completions.globals) {
        for (var i = 0; i < this.completions.globals.length; i++) {
            if (this.completions.globals[i].label === moduleName) {
                globalEntry = this.completions.globals[i];
                break;
            }
        }
    }
    if (globalEntry && globalEntry.documentation) {
        lines.push('-- ' + globalEntry.documentation);
        lines.push('');
    }

    var methods = this.completions.methods[moduleName] || [];
    var constants = [];
    var funcs = [];
    for (var i = 0; i < methods.length; i++) {
        if (methods[i].kind === 'constant') {
            constants.push(methods[i]);
        } else {
            funcs.push(methods[i]);
        }
    }

    if (constants.length > 0) {
        lines.push('-- Constants');
        lines.push('-- ---------');
        for (var i = 0; i < constants.length; i++) {
            var c = constants[i];
            var line = moduleName + '.' + c.label;
            if (c.detail) line += '  ' + c.detail;
            if (c.documentation) line += '  -- ' + c.documentation;
            lines.push(line);
        }
        lines.push('');
    }

    if (funcs.length > 0) {
        lines.push('-- Methods');
        lines.push('-- -------');
        for (var i = 0; i < funcs.length; i++) {
            var m = funcs[i];
            lines.push('function ' + moduleName + ':' + m.label + (m.detail || '()'));
            if (m.documentation) {
                lines.push('  -- ' + m.documentation);
            }
            lines.push('end');
            lines.push('');
        }
    }

    var model = monaco.editor.createModel(lines.join('\n'), 'lua', uri);
    this._docModels[moduleName] = model;
    return model;
};

/**
 * Register Monaco definition provider — enables Cmd+Click / Ctrl+Click
 * to peek into generated API documentation.
 * @private
 */
LuaEditor.prototype._registerDefinitionProvider = function() {
    var self = this;

    monaco.languages.registerDefinitionProvider('lua', {
        provideDefinition: function(model, position) {
            var resolved = self._resolveSymbolAt(model, position);
            if (!resolved) return null;

            var entry = resolved.entry;
            var moduleName = resolved.moduleName;

            // Global module name → open its doc
            if (!moduleName && entry.kind === 'module') {
                moduleName = entry.label;
            }

            if (!moduleName || !self.completions.methods[moduleName]) return null;

            var docModel = self._getDocModel(moduleName);

            // Find the target line
            var targetLine = 1;
            if (resolved.moduleName) {
                var searchText = entry.kind === 'constant'
                    ? moduleName + '.' + entry.label
                    : 'function ' + moduleName + ':' + entry.label;

                var lineCount = docModel.getLineCount();
                for (var i = 1; i <= lineCount; i++) {
                    if (docModel.getLineContent(i).indexOf(searchText) !== -1) {
                        targetLine = i;
                        break;
                    }
                }
            }

            return {
                uri: docModel.uri,
                range: new monaco.Range(targetLine, 1, targetLine, 1)
            };
        }
    });
};

// ===================== History Tab =====================

/**
 * Open or switch to the History tab
 */
LuaEditor.prototype.openHistory = function() {
    var existing = this._findTab('history:');
    if (existing) {
        this._switchToTab('history:');
        return;
    }

    this.openTabs.push({
        path: 'history:',
        name: 'History',
        type: 'history',
        readonly: true,
        modified: false,
        content: ''
    });
    this._switchToTab('history:');
};

/**
 * Load history events from the server
 * @private
 */
LuaEditor.prototype._loadHistory = function() {
    var self = this;
    var timeline = this.elements.historyTimeline;
    if (!timeline) return;

    timeline.innerHTML = '<div class="history-empty"><i class="bi bi-hourglass-split"></i>Loading...</div>';

    fetch('/api/mcp/workspace/history?limit=200')
        .then(function(r) { return r.json(); })
        .then(function(data) {
            self.historyEvents = data.events || [];
            self._populateHistoryFilters();
            self._renderHistoryView();

            // Bind filter + view toggle events (once)
            if (!self._historyBound) {
                self._historyBound = true;

                document.querySelectorAll('#historyViewToggle button').forEach(function(btn) {
                    btn.addEventListener('click', function() {
                        document.querySelectorAll('#historyViewToggle button').forEach(function(b) { b.classList.remove('active'); });
                        btn.classList.add('active');
                        self.historyView = btn.dataset.view;
                        self._renderHistoryView();
                    });
                });

                var fileFilter = document.getElementById('historyFileFilter');
                var actorFilter = document.getElementById('historyActorFilter');
                if (fileFilter) fileFilter.addEventListener('change', function() { self._renderHistoryView(); });
                if (actorFilter) actorFilter.addEventListener('change', function() { self._renderHistoryView(); });
            }
        })
        .catch(function(err) {
            timeline.innerHTML = '<div class="history-empty"><i class="bi bi-exclamation-triangle"></i>Failed to load history</div>';
        });
};

/**
 * Populate file and actor filter dropdowns
 * @private
 */
LuaEditor.prototype._populateHistoryFilters = function() {
    var files = {};
    var actors = {};

    this.historyEvents.forEach(function(ev) {
        if (ev.path) files[ev.path] = true;
        if (ev.paths) ev.paths.forEach(function(p) { files[p] = true; });
        var actor = ev.actor || ev.bot_name || '';
        if (actor) actors[actor] = true;
    });

    var fileSelect = document.getElementById('historyFileFilter');
    var actorSelect = document.getElementById('historyActorFilter');

    if (fileSelect) {
        var curFile = fileSelect.value;
        fileSelect.innerHTML = '<option value="">All files</option>';
        Object.keys(files).sort().forEach(function(f) {
            var opt = document.createElement('option');
            opt.value = f;
            opt.textContent = f;
            fileSelect.appendChild(opt);
        });
        fileSelect.value = curFile;
    }

    if (actorSelect) {
        var curActor = actorSelect.value;
        actorSelect.innerHTML = '<option value="">All actors</option>';
        Object.keys(actors).sort().forEach(function(a) {
            var opt = document.createElement('option');
            opt.value = a;
            opt.textContent = a;
            actorSelect.appendChild(opt);
        });
        actorSelect.value = curActor;
    }
};

/**
 * Get filtered events based on current filter selections
 * @private
 */
LuaEditor.prototype._getFilteredEvents = function() {
    var fileFilter = (document.getElementById('historyFileFilter') || {}).value || '';
    var actorFilter = (document.getElementById('historyActorFilter') || {}).value || '';
    var events = this.historyEvents;

    if (fileFilter) {
        events = events.filter(function(ev) {
            if (ev.path === fileFilter) return true;
            if (ev.paths && ev.paths.indexOf(fileFilter) !== -1) return true;
            return false;
        });
    }

    if (actorFilter) {
        events = events.filter(function(ev) {
            return (ev.actor || ev.bot_name || '') === actorFilter;
        });
    }

    return events;
};

/**
 * Render the currently selected history view
 * @private
 */
LuaEditor.prototype._renderHistoryView = function() {
    var events = this._getFilteredEvents();
    if (this.historyView === 'files') {
        this._renderHistoryFiles(events);
    } else {
        this._renderHistoryTimeline(events);
    }
};

/**
 * Get icon info for an event type
 * @private
 */
LuaEditor.prototype._historyIcon = function(ev) {
    switch (ev.type) {
        case 'draft_write':
            return { icon: 'bi-pencil', cls: '', label: 'Draft' };
        case 'draft_test':
            return ev.success
                ? { icon: 'bi-play-circle', cls: 'test-ok', label: 'Test OK' }
                : { icon: 'bi-play-circle', cls: 'test-fail', label: 'Test fail' };
        case 'draft_validate':
            return ev.success
                ? { icon: 'bi-check2-circle', cls: 'validate-ok', label: 'Valid' }
                : { icon: 'bi-check2-circle', cls: 'validate-fail', label: 'Invalid' };
        case 'promote':
            return { icon: 'bi-check-lg', cls: 'promote', label: 'Promoted' };
        case 'draft_delete':
            return { icon: 'bi-trash', cls: 'delete', label: 'Deleted' };
        default:
            return { icon: 'bi-circle', cls: '', label: ev.type };
    }
};

/**
 * Render timeline view (chronological)
 * @private
 */
LuaEditor.prototype._renderHistoryTimeline = function(events) {
    var self = this;
    var timeline = this.elements.historyTimeline;
    if (!timeline) return;

    if (!events || events.length === 0) {
        timeline.innerHTML = '<div class="history-empty"><i class="bi bi-clock-history"></i>No history events yet</div>';
        return;
    }

    var html = '';
    var lastDate = '';

    events.forEach(function(ev) {
        var d = new Date(ev.time);
        var dateStr = d.toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' });
        var timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });

        // Date separator
        if (dateStr !== lastDate) {
            html += '<div class="history-date">' + VCLU.esc(dateStr) + '</div>';
            lastDate = dateStr;
        }

        var info = self._historyIcon(ev);
        var filePath = ev.path || (ev.paths && ev.paths.length > 0 ? ev.paths.join(', ') : '');

        html += '<div class="history-event">';
        html += '<span class="history-event-time">' + timeStr + '</span>';
        html += '<span class="history-event-icon ' + info.cls + '"><i class="bi ' + info.icon + '"></i></span>';
        html += '<div class="history-event-body">';
        html += '<span class="history-event-label">' + VCLU.esc(info.label) + '</span>';
        if (filePath) {
            html += '<span class="history-event-file">' + VCLU.esc(filePath) + '</span>';
        }
        var evActor = ev.actor || ev.bot_name || '';
        if (evActor) {
            html += '<span class="history-event-actor">by ' + VCLU.esc(evActor) + '</span>';
        }
        if (ev.details || ev.backup) {
            html += '<div class="history-event-details">';
            if (ev.details) html += VCLU.esc(ev.details);
            if (ev.backup) {
                html += ' <button class="btn-link" data-backup="' + VCLU.esc(ev.backup) +
                    '" data-path="' + VCLU.esc(ev.path || '') + '">[View diff]</button>';
            }
            html += '</div>';
        }
        html += '</div></div>';
    });

    timeline.innerHTML = html;

    // Bind [View diff] buttons
    timeline.querySelectorAll('[data-backup]').forEach(function(btn) {
        btn.addEventListener('click', function(e) {
            e.preventDefault();
            self._viewBackupDiff(btn.dataset.backup, btn.dataset.path);
        });
    });
};

/**
 * Render files (pipeline) view
 * @private
 */
LuaEditor.prototype._renderHistoryFiles = function(events) {
    var self = this;
    var timeline = this.elements.historyTimeline;
    if (!timeline) return;

    if (!events || events.length === 0) {
        timeline.innerHTML = '<div class="history-empty"><i class="bi bi-clock-history"></i>No history events yet</div>';
        return;
    }

    // Group events by file path (chronological within each group)
    var byFile = {};
    var fileOrder = [];

    // Reverse to get chronological order (events come newest-first)
    var chrono = events.slice().reverse();

    chrono.forEach(function(ev) {
        var paths = [];
        if (ev.path) paths.push(ev.path);
        if (ev.paths) {
            ev.paths.forEach(function(p) {
                if (paths.indexOf(p) === -1) paths.push(p);
            });
        }
        if (paths.length === 0) paths.push('(unknown)');

        paths.forEach(function(p) {
            if (!byFile[p]) {
                byFile[p] = [];
                fileOrder.push(p);
            }
            byFile[p].push(ev);
        });
    });

    // Sort: active drafts (no promote) first, then by last event time desc
    fileOrder.sort(function(a, b) {
        var aPromoted = byFile[a].some(function(e) { return e.type === 'promote'; });
        var bPromoted = byFile[b].some(function(e) { return e.type === 'promote'; });
        if (aPromoted !== bPromoted) return aPromoted ? 1 : -1;
        var aLast = new Date(byFile[a][byFile[a].length - 1].time);
        var bLast = new Date(byFile[b][byFile[b].length - 1].time);
        return bLast - aLast;
    });

    var html = '';

    fileOrder.forEach(function(filePath) {
        var evts = byFile[filePath];
        var hasPromote = evts.some(function(e) { return e.type === 'promote'; });

        html += '<div class="history-file-card' + (hasPromote ? '' : ' active') + '">';
        html += '<div class="history-file-name">' + VCLU.esc(filePath) + '</div>';
        html += '<div class="history-pipeline">';

        evts.forEach(function(ev, idx) {
            if (idx > 0) html += '<span class="history-pipeline-arrow">&rarr;</span>';

            var info = self._historyIcon(ev);
            var stepCls = '';
            if (ev.type === 'promote') stepCls = 'promoted';
            else if (ev.type === 'draft_test' && ev.success) stepCls = 'success';
            else if (ev.type === 'draft_test' && !ev.success) stepCls = 'fail';
            else if (ev.type === 'draft_validate' && !ev.success) stepCls = 'fail';

            var d = new Date(ev.time);
            var timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
            var pipeActor = ev.actor || ev.bot_name || '';
            var actorStr = pipeActor ? ', ' + pipeActor : '';

            html += '<span class="history-pipeline-step ' + stepCls + '">';
            html += '<i class="bi ' + info.icon + '"></i> ';
            html += VCLU.esc(info.label);
            html += ' <small>(' + timeStr + actorStr + ')</small>';
            html += '</span>';
        });

        html += '</div></div>';
    });

    timeline.innerHTML = html;
};

/**
 * View backup diff — fetch backup content and current production, open diff tab
 * @private
 */
LuaEditor.prototype._viewBackupDiff = function(backupName, filePath) {
    var self = this;

    Promise.all([
        fetch('/api/mcp/workspace/backup/' + encodeURIComponent(backupName)).then(function(r) { return r.json(); }),
        filePath ? fetch('/api/editor/file/' + encodeURIComponent(filePath)).then(function(r) { return r.json(); }) : Promise.resolve({ content: '' })
    ])
    .then(function(results) {
        var backup = results[0];
        var current = results[1];

        if (backup.error) {
            alert('Failed to load backup: ' + backup.error);
            return;
        }

        var path = backup.path || filePath || backupName;
        var tabPath = 'backup:' + backupName;

        // Check if tab already open
        var existing = self._findTab(tabPath);
        if (existing) {
            self._switchToTab(tabPath);
            return;
        }

        // Open as backup diff tab
        self.openTabs.push({
            path: tabPath,
            name: 'Backup: ' + (path.split('/').pop() || backupName),
            type: 'backup',
            readonly: true,
            modified: false,
            content: '',
            backupName: backupName,
            backupPath: path,
            backupContent: backup.content || '',
            prodContent: current.content || ''
        });
        self._switchToTab(tabPath);
    })
    .catch(function(err) {
        alert('Failed to load diff: ' + err.message);
    });
};
