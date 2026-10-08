/**
 * MCP Bot Management
 */
(function() {
    'use strict';

    var bots = [];
    var profiles = [];
    var editingBotId = null;

    // i18n helper — falls back to English default
    function t(key, fallback) {
        return (window.MCP_I18N && MCP_I18N[key]) || fallback || key;
    }

    function loadBots() {
        fetch('/api/mcp/bots')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                bots = data.bots || [];
                renderBots();
            });
    }

    function loadProfiles() {
        fetch('/api/access/profiles')
            .then(function(r) { return r.json(); })
            .then(function(data) {
                profiles = (data.profiles || []).map(function(p) { return p.name; }).filter(function(n) { return n.charAt(0) !== '_'; });
            });
    }

    function renderBots() {
        var container = document.getElementById('mcpBotsList');
        if (!container) return;

        if (bots.length === 0) {
            container.innerHTML = '<p class="text-muted small">' + VCLU.esc(t('no_bots', 'No bots configured.')) + '</p>';
            return;
        }

        var html = '<div class="list-group">';
        bots.forEach(function(bot) {
            var profileBadges = (bot.profiles || []).map(function(p) {
                return '<span class="badge bg-primary me-1">' + VCLU.esc(p) + '</span>';
            }).join('');

            html += '<div class="list-group-item">';
            html += '<div class="d-flex justify-content-between align-items-start">';
            html += '<div>';
            html += '<h6 class="mb-1">' + VCLU.esc(bot.name) + '</h6>';
            html += '<small class="text-muted">ID: ' + VCLU.esc(bot.id) + '</small>';
            html += '<div class="mt-1">' + VCLU.esc(t('profiles_label', 'Profiles')) + ': ' + (profileBadges || '<span class="text-muted">none</span>') + '</div>';
            var sandboxBadge = bot.devMode
                ? '<span class="badge bg-warning text-dark"><i class="bi bi-code-slash"></i> Sandbox</span>'
                : '<span class="badge bg-secondary"><i class="bi bi-lock"></i> Read-only</span>';
            var execBadge = bot.execMode
                ? ' <span class="badge bg-danger"><i class="bi bi-terminal"></i> Exec</span>'
                : '';
            html += '<div class="mt-1">' + sandboxBadge + execBadge + '</div>';
            html += '<small class="text-muted">' + VCLU.esc(t('created_label', 'Created')) + ': ' + VCLU.esc(bot.createdAt) + '</small>';
            html += '</div>';
            html += '<div class="btn-group btn-group-sm">';
            html += '<button class="btn btn-outline-primary" onclick="mcpBots.editBot(\'' + VCLU.esc(bot.id) + '\')" title="Edit"><i class="bi bi-pencil"></i></button>';
            html += '<button class="btn btn-outline-warning" onclick="mcpBots.regenerateKey(\'' + VCLU.esc(bot.id) + '\')" title="Regenerate key"><i class="bi bi-key"></i></button>';
            html += '<button class="btn btn-outline-danger" onclick="mcpBots.deleteBot(\'' + VCLU.esc(bot.id) + '\')" title="Delete"><i class="bi bi-trash"></i></button>';
            html += '</div>';
            html += '</div>';
            html += '</div>';
        });
        html += '</div>';
        container.innerHTML = html;
    }

    function showAddModal() {
        editingBotId = null;
        document.getElementById('botModalTitle').textContent = t('add_bot', 'Add Bot');
        document.getElementById('botName').value = '';
        document.getElementById('botName').disabled = false;
        document.getElementById('botDevMode').checked = false;
        document.getElementById('botExecMode').checked = false;
        document.getElementById('botKeyResult').style.display = 'none';
        document.getElementById('btnSaveBot').textContent = t('create_bot', 'Create Bot');
        document.getElementById('btnSaveBot').style.display = 'inline-block';

        renderProfileCheckboxes([]);
        new bootstrap.Modal(document.getElementById('botModal')).show();
    }

    function editBot(botId) {
        var bot = bots.find(function(b) { return b.id === botId; });
        if (!bot) return;

        editingBotId = botId;
        document.getElementById('botModalTitle').textContent = t('edit_bot', 'Edit Bot') + ': ' + bot.name;
        document.getElementById('botName').value = bot.name;
        document.getElementById('botName').disabled = false;
        document.getElementById('botDevMode').checked = !!bot.devMode;
        document.getElementById('botExecMode').checked = !!bot.execMode;
        document.getElementById('botKeyResult').style.display = 'none';
        document.getElementById('btnSaveBot').textContent = t('save_changes', 'Save Changes');
        document.getElementById('btnSaveBot').style.display = 'inline-block';

        renderProfileCheckboxes(bot.profiles || []);
        new bootstrap.Modal(document.getElementById('botModal')).show();
    }

    function renderProfileCheckboxes(selected) {
        var container = document.getElementById('botProfilesCheckboxes');
        if (!container) return;

        if (profiles.length === 0) {
            container.innerHTML = '<p class="text-muted small">' + VCLU.esc(t('loading_profiles', 'Loading profiles...')) + '</p>';
            // Reload profiles and try again
            fetch('/api/access/profiles')
                .then(function(r) { return r.json(); })
                .then(function(data) {
                    profiles = (data.profiles || []).map(function(p) { return p.name; }).filter(function(n) { return n.charAt(0) !== '_'; });
                    renderProfileCheckboxes(selected);
                });
            return;
        }

        var html = '';
        profiles.forEach(function(name) {
            var checked = selected.indexOf(name) !== -1 ? ' checked' : '';
            html += '<div class="form-check">';
            html += '<input class="form-check-input bot-profile-check" type="checkbox" id="bot-prof-' + VCLU.esc(name) + '" value="' + VCLU.esc(name) + '"' + checked + '>';
            html += '<label class="form-check-label" for="bot-prof-' + VCLU.esc(name) + '">' + VCLU.esc(name) + '</label>';
            html += '</div>';
        });
        container.innerHTML = html;
    }

    function getSelectedProfiles() {
        var selected = [];
        document.querySelectorAll('.bot-profile-check:checked').forEach(function(cb) {
            selected.push(cb.value);
        });
        return selected;
    }

    function saveBot() {
        var name = document.getElementById('botName').value.trim();
        var selectedProfiles = getSelectedProfiles();
        var devMode = document.getElementById('botDevMode').checked;
        var execMode = document.getElementById('botExecMode').checked;

        if (!name) {
            VCLU.showToast(t('bot_name_required', 'Bot name is required'), 'warning');
            return;
        }

        if (editingBotId) {
            // Update existing bot
            fetch('/api/mcp/bots/' + encodeURIComponent(editingBotId), {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: name, profiles: selectedProfiles, devMode: devMode, execMode: execMode })
            })
            .then(function(r) { return r.json(); })
            .then(function(data) {
                if (data.error) {
                    VCLU.showToast('Error: ' + data.error, 'danger');
                } else {
                    bootstrap.Modal.getInstance(document.getElementById('botModal')).hide();
                    loadBots();
                }
            });
        } else {
            // Create new bot
            fetch('/api/mcp/bots', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: name, profiles: selectedProfiles, devMode: devMode, execMode: execMode })
            })
            .then(function(r) { return r.json(); })
            .then(function(data) {
                if (data.error) {
                    VCLU.showToast('Error: ' + data.error, 'danger');
                } else {
                    // Show the API key
                    document.getElementById('botKeyDisplay').value = data.apiKey;
                    document.getElementById('botKeyResult').style.display = 'block';
                    document.getElementById('btnSaveBot').style.display = 'none';
                    loadBots();
                }
            });
        }
    }

    function deleteBot(botId) {
        if (!confirm(t('delete_bot_confirm', 'Delete this bot? Its API key will stop working immediately.'))) return;

        fetch('/api/mcp/bots/' + encodeURIComponent(botId), { method: 'DELETE' })
            .then(function(r) { return r.json(); })
            .then(function(data) {
                if (data.error) {
                    VCLU.showToast('Error: ' + data.error, 'danger');
                } else {
                    loadBots();
                }
            });
    }

    function regenerateKey(botId) {
        if (!confirm(t('regen_key_confirm', 'Regenerate API key? The old key will stop working immediately.'))) return;

        fetch('/api/mcp/bots/' + encodeURIComponent(botId) + '/regenerate', { method: 'POST' })
            .then(function(r) { return r.json(); })
            .then(function(data) {
                if (data.error) {
                    VCLU.showToast('Error: ' + data.error, 'danger');
                } else {
                    prompt(t('regen_key_prompt', 'New API key (copy now, it will not be shown again):'), data.apiKey);
                }
            });
    }

    // Expose API
    window.mcpBots = {
        editBot: editBot,
        deleteBot: deleteBot,
        regenerateKey: regenerateKey
    };

    // Init
    document.addEventListener('DOMContentLoaded', function() {
        loadBots();
        loadProfiles();

        var addBtn = document.getElementById('btnAddBot');
        if (addBtn) addBtn.addEventListener('click', showAddModal);

        var saveBtn = document.getElementById('btnSaveBot');
        if (saveBtn) saveBtn.addEventListener('click', saveBot);

        var copyBtn = document.getElementById('btnCopyBotKey');
        if (copyBtn) {
            copyBtn.addEventListener('click', function() {
                var keyInput = document.getElementById('botKeyDisplay');
                keyInput.select();
                document.execCommand('copy');
                copyBtn.textContent = t('copied', 'Copied!');
                setTimeout(function() { copyBtn.textContent = t('copy', 'Copy'); }, 2000);
            });
        }
    });
})();
