// MCP Dashboard JavaScript
(function() {
    'use strict';

    var refreshInterval = null;
    var AUTO_REFRESH_MS = 10000;

    // Format timestamp for activity table
    function formatTime(ts) {
        var d = new Date(ts);
        return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    }

    // --- Status & Stats ---

    function loadStatus() {
        fetch('/api/mcp/status')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                // Status badge
                var badge = document.getElementById('mcpStatusBadge');
                if (badge) {
                    if (data.enabled) {
                        badge.className = 'badge bg-success';
                        badge.textContent = (window.MCP_I18N && MCP_I18N.enabled) || 'Enabled';
                    } else {
                        badge.className = 'badge bg-secondary';
                        badge.textContent = (window.MCP_I18N && MCP_I18N.disabled) || 'Disabled';
                    }
                }

                // Counters
                var el;
                el = document.getElementById('mcpTotalRequests');
                if (el) el.textContent = data.totalRequests || 0;
                el = document.getElementById('mcpTotalErrors');
                if (el) el.textContent = data.totalErrors || 0;
                el = document.getElementById('mcpActiveSessions');
                if (el) el.textContent = data.activeSessions || 0;

            })
            .catch(function(err) {
                console.error('MCP status error:', err);
            });
    }

    function loadStats() {
        fetch('/api/mcp/stats')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                if (!data.latencyBuckets) return;
                var buckets = data.latencyBuckets;
                var keys = ['<10ms', '<50ms', '<100ms', '<500ms', '<1s', '>1s'];
                for (var i = 0; i < keys.length; i++) {
                    var el = document.querySelector('[data-bucket="' + i + '"]');
                    if (el) el.textContent = buckets[keys[i]] || 0;
                }
            })
            .catch(function(err) {
                console.error('MCP stats error:', err);
            });
    }

    function loadActivity() {
        fetch('/api/mcp/activity?limit=50')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var tbody = document.getElementById('mcpActivityBody');
                if (!tbody) return;

                var activity = data.activity || [];
                if (activity.length === 0) {
                    var noAct = (window.MCP_I18N && MCP_I18N.no_activity) || 'No activity yet';
                    tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted">' + noAct + '</td></tr>';
                    return;
                }

                // Reverse so newest first
                activity.reverse();

                var html = '';
                for (var i = 0; i < activity.length; i++) {
                    var a = activity[i];
                    var statusBadge = a.success
                        ? '<span class="badge bg-success">OK</span>'
                        : '<span class="badge bg-danger" title="' + VCLU.esc(a.error) + '">ERR</span>';
                    html += '<tr>' +
                        '<td class="text-nowrap ps-3"><small>' + VCLU.esc(formatTime(a.time)) + '</small></td>' +
                        '<td class="text-nowrap"><code>' + VCLU.esc(a.tool) + '</code></td>' +
                        '<td class="text-nowrap"><small>' + VCLU.esc(a.path || '-') + '</small></td>' +
                        '<td>' + statusBadge + '</td>' +
                        '<td class="text-nowrap"><small>' + (a.duration_ms != null ? a.duration_ms.toFixed(1) + 'ms' : '-') + '</small></td>' +
                        '<td class="text-nowrap pe-3"><small>' + VCLU.esc(a.remote_ip || '-') + '</small></td>' +
                        '</tr>';
                }
                tbody.innerHTML = html;
            })
            .catch(function(err) {
                console.error('MCP activity error:', err);
            });
    }

    // --- Configuration ---

    function bindToggle() {
        var checkbox = document.getElementById('mcpEnabled');
        if (checkbox) {
            checkbox.addEventListener('change', function() {
                var enabled = checkbox.checked;
                fetch('/api/mcp/save', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ enabled: enabled })
                })
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    if (data.error) {
                        VCLU.showToast('Error: ' + data.error, 'danger');
                        checkbox.checked = !enabled;
                        return;
                    }
                    loadStatus();
                })
                .catch(function(err) {
                    VCLU.showToast('Error: ' + err.message, 'danger');
                    checkbox.checked = !enabled;
                });
            });
        }

        var devModeCheckbox = document.getElementById('mcpDevMode');
        if (devModeCheckbox) {
            devModeCheckbox.addEventListener('change', function() {
                var devMode = devModeCheckbox.checked;
                fetch('/api/mcp/save', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ devModeEnabled: devMode })
                })
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    if (data.error) {
                        VCLU.showToast('Error: ' + data.error, 'danger');
                        devModeCheckbox.checked = !devMode;
                        return;
                    }
                    loadStatus();
                })
                .catch(function(err) {
                    VCLU.showToast('Error: ' + err.message, 'danger');
                    devModeCheckbox.checked = !devMode;
                });
            });
        }
    }


    // --- Connection guide URL ---

    function updateConnectionURL() {
        var base = window.location.protocol + '//' + window.location.host;
        var els = document.querySelectorAll('.mcp-url');
        for (var i = 0; i < els.length; i++) {
            els[i].textContent = base;
        }
    }

    // --- Init ---

    function refreshAll() {
        loadStatus();
        loadStats();
        loadActivity();
    }

    function init() {
        updateConnectionURL();
        bindToggle();

        // Initial load
        refreshAll();

        // Auto-refresh
        refreshInterval = setInterval(refreshAll, AUTO_REFRESH_MS);

        // Manual refresh button
        var refreshBtn = document.getElementById('btnRefreshActivity');
        if (refreshBtn) {
            refreshBtn.addEventListener('click', function() {
                refreshAll();
            });
        }
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
