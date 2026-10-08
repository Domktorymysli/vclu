// MCP Workspace Dashboard — audit log, incidents, agent context
(function() {
    'use strict';

    function formatTime(ts) {
        var d = new Date(ts);
        return d.toLocaleString([], {
            month: '2-digit', day: '2-digit',
            hour: '2-digit', minute: '2-digit', second: '2-digit'
        });
    }

    // --- Audit Log ---

    function loadAuditLog() {
        fetch('/api/mcp/workspace/audit?limit=50')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var entries = data.entries || [];
                var tbody = document.getElementById('auditBody');
                var table = document.getElementById('auditTable');
                var empty = document.getElementById('auditEmpty');
                if (!tbody) return;

                if (entries.length === 0) {
                    table.style.display = 'none';
                    empty.style.display = '';
                    return;
                }

                table.style.display = '';
                empty.style.display = 'none';

                var html = '';
                for (var i = 0; i < entries.length; i++) {
                    var e = entries[i];
                    var statusBadge = e.success
                        ? '<span class="badge bg-success">OK</span>'
                        : '<span class="badge bg-danger">ERR</span>';
                    var pathStr = e.paths ? e.paths.join(', ') : (e.path || '-');
                    html += '<tr>' +
                        '<td class="ps-3"><small>' + VCLU.esc(formatTime(e.time)) + '</small></td>' +
                        '<td><code>' + VCLU.esc(e.tool) + '</code></td>' +
                        '<td><small>' + VCLU.esc(pathStr) + '</small></td>' +
                        '<td><small>' + VCLU.esc(e.bot_name || '-') + '</small></td>' +
                        '<td>' + statusBadge + '</td>' +
                        '<td class="pe-3"><small>' + VCLU.esc(e.details || '') + '</small></td>' +
                        '</tr>';
                }
                tbody.innerHTML = html;
            })
            .catch(function(err) {
                console.error('Audit log error:', err);
            });
    }

    // --- Incidents ---

    function loadIncidents() {
        fetch('/api/mcp/incidents')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var incidents = data.incidents || [];
                var tbody = document.getElementById('incidentsBody');
                var table = document.getElementById('incidentsTable');
                var empty = document.getElementById('incidentsEmpty');
                if (!tbody) return;

                if (incidents.length === 0) {
                    table.style.display = 'none';
                    empty.style.display = '';
                    return;
                }

                table.style.display = '';
                empty.style.display = 'none';

                var html = '';
                for (var i = 0; i < incidents.length; i++) {
                    var inc = incidents[i];
                    var triggerBadge = inc.trigger === 'rollback'
                        ? '<span class="badge bg-danger">' + VCLU.esc(inc.trigger) + '</span>'
                        : inc.trigger === 'watchdog'
                        ? '<span class="badge bg-warning text-dark">' + VCLU.esc(inc.trigger) + '</span>'
                        : '<span class="badge bg-secondary">' + VCLU.esc(inc.trigger) + '</span>';
                    html += '<tr>' +
                        '<td class="ps-3"><small>' + VCLU.esc(formatTime(inc.timestamp)) + '</small></td>' +
                        '<td>' + triggerBadge + '</td>' +
                        '<td><small>' + VCLU.esc(inc.error_msg || '') + '</small></td>' +
                        '</tr>';
                }
                tbody.innerHTML = html;
            })
            .catch(function(err) {
                console.error('Incidents error:', err);
            });
    }

    // --- Agent Context ---

    function loadAgentContext() {
        fetch('/api/mcp/agent-context')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var content = data.content || '';
                var pre = document.getElementById('agentContextContent');
                var empty = document.getElementById('agentContextEmpty');
                if (!pre) return;

                if (!content) {
                    pre.style.display = 'none';
                    empty.style.display = '';
                    return;
                }

                pre.style.display = '';
                empty.style.display = 'none';
                pre.textContent = content;
            })
            .catch(function(err) {
                console.error('Agent context error:', err);
            });
    }

    function clearAgentContext() {
        if (!confirm('Clear all agent notes? This cannot be undone.')) return;
        fetch('/api/mcp/agent-context', { method: 'DELETE' })
            .then(function(r) { return r.json(); })
            .then(function() { loadAgentContext(); })
            .catch(function(err) { alert('Error: ' + err.message); });
    }

    // --- Init ---

    function init() {
        loadAuditLog();
        loadIncidents();
        loadAgentContext();

        // Refresh buttons
        var btn;
        btn = document.getElementById('btnRefreshAudit');
        if (btn) btn.addEventListener('click', loadAuditLog);
        btn = document.getElementById('btnRefreshIncidents');
        if (btn) btn.addEventListener('click', loadIncidents);
        btn = document.getElementById('btnClearAgentContext');
        if (btn) btn.addEventListener('click', clearAgentContext);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
