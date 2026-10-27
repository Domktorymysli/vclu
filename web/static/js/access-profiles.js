/**
 * ProfileManager - Manages access control profiles
 */
(function() {
    'use strict';

    var profiles = [];

    function loadAll() {
        fetch('/api/access/profiles')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                profiles = data.profiles || [];
                renderProfiles();
            });
    }

    function renderProfiles() {
        var container = document.getElementById('profilesList');
        if (!container) return;

        if (profiles.length === 0) {
            container.innerHTML = '<p class="text-muted">No profiles configured.</p>';
            return;
        }

        var html = '<div class="list-group">';
        profiles.forEach(function(p) {
            var objectCount = p.objects ? Object.keys(p.objects).length : 0;
            var badgeClass = p.default === 'full' ? 'bg-success' : p.default === 'readonly' ? 'bg-warning' : 'bg-secondary';

            html += '<div class="list-group-item">';
            html += '<div class="d-flex justify-content-between align-items-start">';
            html += '<div style="min-width:0; flex:1;">';
            html += '<h6 class="mb-1">' + VCLU.esc(p.name);
            if (p.builtin) {
                html += ' <span class="badge bg-info">built-in</span>';
            }
            html += '</h6>';

            // Default level + override breakdown by level
            html += '<div class="mb-1"><small class="text-muted">Default: <span class="badge ' + badgeClass + '">' + VCLU.esc(p.default) + '</span>';
            if (objectCount > 0) {
                var counts = { full: 0, readonly: 0, hidden: 0 };
                Object.values(p.objects).forEach(function(lvl) { counts[lvl] = (counts[lvl] || 0) + 1; });
                var parts = [];
                if (counts.full > 0) parts.push('<span class="badge bg-success">' + counts.full + ' Full</span>');
                if (counts.readonly > 0) parts.push('<span class="badge bg-warning">' + counts.readonly + ' Readonly</span>');
                if (counts.hidden > 0) parts.push('<span class="badge bg-secondary">' + counts.hidden + ' Hidden</span>');
                if (parts.length > 0) {
                    html += ' &middot; ' + parts.join(' ');
                }
            }
            html += '</small></div>';

            // Used by
            var usedParts = [];
            if (p.usedByIntegrations && p.usedByIntegrations.length > 0) {
                p.usedByIntegrations.forEach(function(intg) {
                    var icon = intg === 'mqtt' ? 'bi-house-heart' : intg === 'homekit' ? 'bi-apple' : intg === 'remote' ? 'bi-globe' : 'bi-robot';
                    usedParts.push('<span class="badge bg-outline-secondary border me-1"><i class="bi ' + icon + '"></i> ' + VCLU.esc(intg) + '</span>');
                });
            }
            if (p.usedByBots && p.usedByBots.length > 0) {
                p.usedByBots.forEach(function(bot) {
                    usedParts.push('<span class="badge bg-outline-secondary border me-1"><i class="bi bi-robot"></i> ' + VCLU.esc(bot) + '</span>');
                });
            }
            if (usedParts.length > 0) {
                html += '<div class="mb-1"><small class="text-muted">Used by: </small>' + usedParts.join('') + '</div>';
            }

            html += '</div>';

            // Action buttons
            html += '<div class="btn-group btn-group-sm ms-2">';
            if (!p.builtin) {
                html += '<button class="btn btn-outline-primary" onclick="profileManager.showEditPage(\'' + VCLU.esc(p.name) + '\')" title="Edit"><i class="bi bi-pencil"></i></button>';
                html += '<button class="btn btn-outline-danger" onclick="profileManager.deleteProfile(\'' + VCLU.esc(p.name) + '\')" title="Delete"><i class="bi bi-trash"></i></button>';
            }
            html += '</div>';

            html += '</div>'; // d-flex
            html += '</div>'; // list-group-item
        });
        html += '</div>';
        container.innerHTML = html;
    }

    function showAddPage() {
        window.location.href = '/access/profiles/edit';
    }

    function showEditPage(name) {
        window.location.href = '/access/profiles/edit?name=' + encodeURIComponent(name);
    }

    function deleteProfile(name) {
        // Build usage description
        var profile = profiles.find(function(p) { return p.name === name; });
        var usageDesc = '';
        if (profile) {
            var parts = [];
            if (profile.usedByIntegrations && profile.usedByIntegrations.length > 0) {
                parts.push('integrations: ' + profile.usedByIntegrations.join(', '));
            }
            if (profile.usedByBots && profile.usedByBots.length > 0) {
                parts.push('bots: ' + profile.usedByBots.join(', '));
            }
            if (parts.length > 0) {
                usageDesc = '\n\nCurrently used by ' + parts.join(' and ') + '. They will be switched to full-access (remote access to no-access).';
            }
        }

        if (!confirm('Delete profile "' + name + '"?' + usageDesc)) return;

        fetch('/api/access/profiles/' + encodeURIComponent(name), {
            method: 'DELETE'
        })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast('Error: ' + data.error, 'danger');
            } else {
                loadAll();
            }
        });
    }

    function resetToDefaults() {
        if (!confirm('Reset all profiles and assignments to defaults?\n\nAll custom profiles will be deleted and all integrations will get full access, except remote access, which gets none.')) return;

        fetch('/api/access/reset', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
        })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast('Error: ' + data.error, 'danger');
            } else {
                loadAll();
            }
        });
    }

    // Expose API
    window.profileManager = {
        loadAll: loadAll,
        showAddPage: showAddPage,
        showEditPage: showEditPage,
        deleteProfile: deleteProfile,
        resetToDefaults: resetToDefaults
    };

    // Load on page ready
    document.addEventListener('DOMContentLoaded', function() {
        loadAll();
    });
})();
