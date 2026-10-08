/**
 * ToolsConsole - Interactive terminal-style console for vCLU
 * Supports local vCLU and remote CLU Lua execution with JSON tree rendering
 */
function ToolsConsole(options) {
    this.options = options || {};
    this.translations = this.options.translations || {};
    this.editor = null;
    this.target = 'local';
    this.selectedCLU = '';
    this.commandHistory = [];
    this.historyIndex = -1;
    this.cellCounter = 0;

    // DOM elements (cached after init)
    this.elements = {};
}

/**
 * Initialize the console
 */
ToolsConsole.prototype.init = function() {
    var self = this;

    this.elements = {
        consoleLog: document.getElementById('consoleLog'),
        emptyHint: document.getElementById('emptyHint'),
        cluSelect: document.getElementById('cluSelect'),
        targetToggle: document.getElementById('targetToggle'),
        runBtn: document.getElementById('runBtn'),
        clearBtn: document.getElementById('clearBtn'),
        monacoContainer: document.getElementById('monacoContainer')
    };

    this._initMonaco(function() {
        self._watchThemeChanges();
    });
    this._bindEvents();
    this._loadCLUs();
};

/**
 * Initialize Monaco editor
 */
ToolsConsole.prototype._initMonaco = function(callback) {
    var self = this;

    require.config({
        paths: { vs: '/static/vendor/monaco/vs' }
    });

    require(['vs/editor/editor.main'], function() {
        self.editor = monaco.editor.create(self.elements.monacoContainer, {
            value: '',
            language: 'lua',
            theme: VCLU.isDark() ? 'vs-dark' : 'vs',
            minimap: { enabled: false },
            lineNumbers: 'off',
            glyphMargin: false,
            folding: false,
            lineDecorationsWidth: 8,
            lineNumbersMinChars: 0,
            renderLineHighlight: 'none',
            scrollBeyondLastLine: false,
            automaticLayout: true,
            wordWrap: 'on',
            fontSize: 14,
            padding: { top: 8, bottom: 8 },
            scrollbar: {
                vertical: 'auto',
                horizontal: 'hidden',
                verticalScrollbarSize: 8
            },
            overviewRulerLanes: 0,
            hideCursorInOverviewRuler: true,
            overviewRulerBorder: false,
            placeholder: 'return 1 + 1'
        });

        // Ctrl+Enter / Cmd+Enter to execute
        self.editor.addCommand(
            monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter,
            function() { self.execute(); }
        );

        // Alt+Up for history
        self.editor.addCommand(
            monaco.KeyMod.Alt | monaco.KeyCode.UpArrow,
            function() { self._navigateHistory(-1); }
        );

        // Alt+Down for history
        self.editor.addCommand(
            monaco.KeyMod.Alt | monaco.KeyCode.DownArrow,
            function() { self._navigateHistory(1); }
        );

        // Auto-resize editor based on content
        self.editor.onDidChangeModelContent(function() {
            self._autoResizeEditor();
        });

        if (callback) callback();
    });
};

/**
 * Auto-resize editor from 1 to ~8 lines based on content
 */
ToolsConsole.prototype._autoResizeEditor = function() {
    if (!this.editor) return;
    var lineCount = this.editor.getModel().getLineCount();
    var clampedLines = Math.max(3, Math.min(lineCount + 1, 8));
    var lineHeight = this.editor.getOption(monaco.editor.EditorOption.lineHeight);
    var padding = 16; // top + bottom padding
    var newHeight = clampedLines * lineHeight + padding;
    newHeight = Math.max(60, Math.min(newHeight, 200));
    this.elements.monacoContainer.style.height = newHeight + 'px';
    this.editor.layout();
};

/**
 * Bind DOM events
 */
ToolsConsole.prototype._bindEvents = function() {
    var self = this;

    // Run button
    this.elements.runBtn.addEventListener('click', function() {
        self.execute();
    });

    // Clear button
    this.elements.clearBtn.addEventListener('click', function() {
        self._clear();
    });

    // Target toggle
    var buttons = this.elements.targetToggle.querySelectorAll('.btn');
    buttons.forEach(function(btn) {
        btn.addEventListener('click', function() {
            buttons.forEach(function(b) { b.classList.remove('active'); });
            btn.classList.add('active');
            self.target = btn.dataset.target;
            self.elements.cluSelect.style.display = self.target === 'clu' ? '' : 'none';
        });
    });

    // CLU select change
    this.elements.cluSelect.addEventListener('change', function() {
        self.selectedCLU = this.value;
    });
};

/**
 * Load CLUs from API
 */
ToolsConsole.prototype._loadCLUs = function() {
    var self = this;
    var select = this.elements.cluSelect;

    fetch('/api/clus')
        .then(function(response) {
            if (!response.ok) throw new Error('Failed to load CLUs');
            return response.json();
        })
        .then(function(clus) {
            select.innerHTML = '';
            if (!clus || clus.length === 0) {
                select.innerHTML = '<option value="">' + self.translations.no_clus + '</option>';
                return;
            }
            clus.forEach(function(clu) {
                var option = document.createElement('option');
                option.value = clu.ip;
                option.textContent = clu.name + ' (' + clu.ip + ')';
                if (clu.isLocal) option.textContent += ' [LOCAL]';
                select.appendChild(option);
            });
            self.selectedCLU = select.value;
        })
        .catch(function() {
            select.innerHTML = '<option value="">Error loading CLUs</option>';
        });
};

/**
 * Execute the current editor content
 */
ToolsConsole.prototype.execute = function() {
    var self = this;
    var script = this.editor ? this.editor.getValue().trim() : '';

    if (!script) return;

    // Push to history
    if (this.commandHistory.length === 0 || this.commandHistory[this.commandHistory.length - 1] !== script) {
        this.commandHistory.push(script);
    }
    this.historyIndex = -1;

    // Create cell
    var cellId = ++this.cellCounter;
    var targetLabel = this.target === 'local' ? 'Local vCLU' : ('CLU ' + this.selectedCLU);
    this._addCell(cellId, script, targetLabel);
    this._setCellLoading(cellId);

    // Clear editor
    if (this.editor) {
        this.editor.setValue('');
        this.editor.focus();
    }

    // Hide empty hint
    if (this.elements.emptyHint) {
        this.elements.emptyHint.style.display = 'none';
    }

    // Execute
    var promise;
    if (this.target === 'local') {
        promise = this._executeLocal(script);
    } else {
        if (!this.selectedCLU) {
            this._setCellResult(cellId, { error: this.translations.no_target });
            return;
        }
        promise = this._executeCLU(script, this.selectedCLU);
    }

    promise
        .then(function(data) {
            self._setCellResult(cellId, data);
        })
        .catch(function(err) {
            self._setCellResult(cellId, { error: self.translations.network_error + ': ' + err.message });
        });
};

/**
 * Execute on local vCLU
 */
ToolsConsole.prototype._executeLocal = function(script) {
    return fetch('/api/editor/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ script: script })
    }).then(function(response) {
        return response.json().then(function(data) {
            if (!response.ok) {
                return { error: data.error || 'Unknown error' };
            }
            return data;
        });
    });
};

/**
 * Execute on remote CLU
 */
ToolsConsole.prototype._executeCLU = function(script, ip) {
    return fetch('/api/clu/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ targetIP: ip, script: script })
    }).then(function(response) {
        return response.json().then(function(data) {
            if (!response.ok) {
                return { error: data.error || 'Unknown error' };
            }
            return data;
        });
    });
};

/**
 * Add a cell to the console log
 */
ToolsConsole.prototype._addCell = function(id, command, targetLabel) {
    var cell = document.createElement('div');
    cell.className = 'console-cell';
    cell.id = 'cell-' + id;

    var now = new Date();
    var timestamp = now.toLocaleTimeString();

    cell.innerHTML =
        '<div class="cell-header">' +
            '<span class="cell-timestamp">' + VCLU.esc(timestamp) + '</span>' +
            '<span class="cell-target">' + VCLU.esc(targetLabel) + '</span>' +
        '</div>' +
        '<div class="cell-command">' +
            '<span class="cell-prompt">&gt;</span>' +
            '<pre>' + VCLU.esc(command) + '</pre>' +
        '</div>' +
        '<div class="cell-result" id="cell-result-' + id + '"></div>';

    this.elements.consoleLog.appendChild(cell);
    this._scrollToBottom();
};

/**
 * Show loading spinner in cell result
 */
ToolsConsole.prototype._setCellLoading = function(id) {
    var el = document.getElementById('cell-result-' + id);
    if (!el) return;
    el.innerHTML =
        '<div class="result-loading">' +
            '<span class="spinner-border spinner-border-sm" role="status"></span>' +
            '<span>' + VCLU.esc(this.translations.executing || 'Executing...') + '</span>' +
        '</div>';
};

/**
 * Set the result of a cell
 */
ToolsConsole.prototype._setCellResult = function(id, data) {
    var el = document.getElementById('cell-result-' + id);
    if (!el) return;

    var self = this;
    el.innerHTML = '';

    // Error case
    if (data.error) {
        el.classList.add('result-error');
        var pre = document.createElement('pre');
        pre.textContent = data.error;
        el.appendChild(pre);
        this._addCopyButton(el, data.error);
        this._scrollToBottom();
        return;
    }

    // Get result text
    var resultText = '';
    if (data.result !== undefined && data.result !== null) {
        resultText = typeof data.result === 'string' ? data.result : JSON.stringify(data.result, null, 2);
    } else if (data.output !== undefined && data.output !== null) {
        resultText = typeof data.output === 'string' ? data.output : JSON.stringify(data.output, null, 2);
    }

    // Nil / empty result
    if (resultText === '' || resultText === 'nil') {
        el.classList.add('result-nil');
        var pre = document.createElement('pre');
        pre.textContent = this.translations.nil_result || 'nil';
        el.appendChild(pre);
        this._scrollToBottom();
        return;
    }

    el.classList.add('result-success');

    // Try to parse as JSON and render tree
    var parsed = this._tryParseJSON(resultText);
    if (parsed !== null && typeof parsed === 'object') {
        var tree = this._renderJsonTree(parsed, 0);
        el.appendChild(tree);
        this._addCopyButton(el, JSON.stringify(parsed, null, 2));
    } else {
        var pre = document.createElement('pre');
        pre.textContent = resultText;
        el.appendChild(pre);
        this._addCopyButton(el, resultText);

        // Collapse long output
        if (this._isLongOutput(resultText)) {
            var contentWrapper = el.querySelector('pre');
            contentWrapper.classList.add('result-collapsed');
            var toggle = document.createElement('span');
            toggle.className = 'result-toggle';
            toggle.textContent = this.translations.show_more || 'Show more...';
            toggle.addEventListener('click', function() {
                var isCollapsed = contentWrapper.classList.contains('result-collapsed');
                contentWrapper.classList.toggle('result-collapsed');
                toggle.textContent = isCollapsed
                    ? (self.translations.show_less || 'Show less')
                    : (self.translations.show_more || 'Show more...');
            });
            el.appendChild(toggle);
        }
    }

    this._scrollToBottom();
};

/**
 * Try to parse a string as JSON
 */
ToolsConsole.prototype._tryParseJSON = function(text) {
    if (!text || typeof text !== 'string') return null;
    var trimmed = text.trim();
    if ((trimmed[0] === '{' && trimmed[trimmed.length - 1] === '}') ||
        (trimmed[0] === '[' && trimmed[trimmed.length - 1] === ']')) {
        try {
            return JSON.parse(trimmed);
        } catch (e) {
            return null;
        }
    }
    return null;
};

/**
 * Render a collapsible JSON tree
 */
ToolsConsole.prototype._renderJsonTree = function(obj, depth) {
    var self = this;
    var container = document.createElement('div');
    container.className = 'json-tree' + (depth === 0 ? ' json-root' : '');

    var isArray = Array.isArray(obj);
    var keys = isArray ? obj : Object.keys(obj);
    var len = isArray ? obj.length : keys.length;

    if (len === 0) {
        var bracket = document.createElement('span');
        bracket.className = 'json-bracket';
        bracket.textContent = isArray ? '[]' : '{}';
        container.appendChild(bracket);
        return container;
    }

    var expanded = depth < 2;

    // Opening bracket with toggle
    var header = document.createElement('span');
    var toggle = document.createElement('span');
    toggle.className = 'json-toggle ' + (expanded ? 'json-expanded' : 'json-collapsed');
    header.appendChild(toggle);

    var openBracket = document.createElement('span');
    openBracket.className = 'json-bracket';
    openBracket.textContent = isArray ? '[' : '{';
    header.appendChild(openBracket);

    // Preview text (shown when collapsed)
    var preview = document.createElement('span');
    preview.className = 'json-preview';
    preview.textContent = ' ' + len + (isArray ? ' items' : ' keys') + '...';
    preview.style.display = expanded ? 'none' : 'inline';
    header.appendChild(preview);

    container.appendChild(header);

    // Children
    var children = document.createElement('div');
    children.className = 'json-children' + (expanded ? '' : ' json-hidden');

    var entries = isArray ? obj.map(function(v, i) { return [i, v]; }) : Object.keys(obj).map(function(k) { return [k, obj[k]]; });

    entries.forEach(function(entry, idx) {
        var key = entry[0];
        var value = entry[1];
        var item = document.createElement('div');
        item.className = 'json-item';

        // Key
        var keySpan = document.createElement('span');
        keySpan.className = 'json-key';
        keySpan.textContent = isArray ? key + ': ' : '"' + key + '": ';
        item.appendChild(keySpan);

        // Value
        if (value === null) {
            var nullSpan = document.createElement('span');
            nullSpan.className = 'json-null';
            nullSpan.textContent = 'null';
            item.appendChild(nullSpan);
        } else if (typeof value === 'object') {
            var subtree = self._renderJsonTree(value, depth + 1);
            item.appendChild(subtree);
        } else if (typeof value === 'string') {
            var strSpan = document.createElement('span');
            strSpan.className = 'json-string';
            strSpan.textContent = '"' + value + '"';
            item.appendChild(strSpan);
        } else if (typeof value === 'number') {
            var numSpan = document.createElement('span');
            numSpan.className = 'json-number';
            numSpan.textContent = String(value);
            item.appendChild(numSpan);
        } else if (typeof value === 'boolean') {
            var boolSpan = document.createElement('span');
            boolSpan.className = 'json-boolean';
            boolSpan.textContent = String(value);
            item.appendChild(boolSpan);
        } else {
            var otherSpan = document.createElement('span');
            otherSpan.textContent = String(value);
            item.appendChild(otherSpan);
        }

        // Comma
        if (idx < entries.length - 1) {
            var comma = document.createElement('span');
            comma.className = 'json-bracket';
            comma.textContent = ',';
            item.appendChild(comma);
        }

        children.appendChild(item);
    });

    container.appendChild(children);

    // Closing bracket
    var closeBracket = document.createElement('span');
    closeBracket.className = 'json-bracket';
    closeBracket.textContent = isArray ? ']' : '}';
    container.appendChild(closeBracket);

    // Toggle click handler
    toggle.addEventListener('click', function() {
        var isExpanded = toggle.classList.contains('json-expanded');
        toggle.classList.toggle('json-expanded');
        toggle.classList.toggle('json-collapsed');
        children.classList.toggle('json-hidden');
        preview.style.display = isExpanded ? 'inline' : 'none';
    });

    return container;
};

/**
 * Add a copy button to a result cell
 */
ToolsConsole.prototype._addCopyButton = function(el, text) {
    var self = this;
    var btn = document.createElement('button');
    btn.className = 'result-copy-btn';
    btn.innerHTML = '<i class="bi bi-clipboard"></i>';
    btn.title = 'Copy';
    btn.addEventListener('click', function() {
        self._copyToClipboard(text, btn);
    });
    el.appendChild(btn);
};

/**
 * Check if output is long enough to collapse
 */
ToolsConsole.prototype._isLongOutput = function(text) {
    if (!text) return false;
    if (text.length > 500) return true;
    var lines = text.split('\n');
    return lines.length > 10;
};

/**
 * Copy text to clipboard
 */
ToolsConsole.prototype._copyToClipboard = function(text, btn) {
    var self = this;
    if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function() {
            btn.classList.add('copied');
            btn.innerHTML = '<i class="bi bi-check"></i>';
            setTimeout(function() {
                btn.classList.remove('copied');
                btn.innerHTML = '<i class="bi bi-clipboard"></i>';
            }, 1500);
        });
    }
};

/**
 * Navigate command history with Alt+Up/Down
 */
ToolsConsole.prototype._navigateHistory = function(direction) {
    if (this.commandHistory.length === 0) return;

    if (this.historyIndex === -1) {
        // Starting from current (newest) position
        if (direction === -1) {
            this.historyIndex = this.commandHistory.length - 1;
        } else {
            return;
        }
    } else {
        this.historyIndex += direction;
        if (this.historyIndex < 0) this.historyIndex = 0;
        if (this.historyIndex >= this.commandHistory.length) {
            this.historyIndex = -1;
            if (this.editor) this.editor.setValue('');
            return;
        }
    }

    if (this.editor && this.historyIndex >= 0) {
        this.editor.setValue(this.commandHistory[this.historyIndex]);
        // Move cursor to end
        var model = this.editor.getModel();
        var lastLine = model.getLineCount();
        var lastCol = model.getLineMaxColumn(lastLine);
        this.editor.setPosition({ lineNumber: lastLine, column: lastCol });
    }
};

/**
 * Auto-scroll console log to bottom
 */
ToolsConsole.prototype._scrollToBottom = function() {
    var log = this.elements.consoleLog;
    requestAnimationFrame(function() {
        log.scrollTop = log.scrollHeight;
    });
};

/**
 * Clear all cells
 */
ToolsConsole.prototype._clear = function() {
    this.elements.consoleLog.innerHTML = '';
    this.cellCounter = 0;

    // Restore empty hint
    var hint = document.createElement('div');
    hint.className = 'console-empty-hint';
    hint.id = 'emptyHint';
    hint.innerHTML = '<i class="bi bi-terminal"></i><p>' +
        VCLU.esc(this.translations.empty_hint || 'Type a Lua expression and press Ctrl+Enter to execute') +
        '</p>';
    this.elements.consoleLog.appendChild(hint);
    this.elements.emptyHint = hint;
};

/**
 * Watch for theme changes and update Monaco theme
 */
ToolsConsole.prototype._watchThemeChanges = function() {
    VCLU.onThemeChange(function(dark) {
        monaco.editor.setTheme(dark ? 'vs-dark' : 'vs');
    });
};


// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    window.toolsConsole = new ToolsConsole({
        translations: window.toolsTranslations || {}
    });
    window.toolsConsole.init();
});
