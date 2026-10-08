/**
 * Profile Editor with Object Matrix
 * Shows all objects with per-object access level selects for a single profile.
 */
(function() {
    'use strict';

    var profileName = (typeof PROFILE_EDIT_NAME === 'string') ? PROFILE_EDIT_NAME : '';
    var isNew = !profileName;
    var i18n = (typeof ACCESS_I18N === 'object') ? ACCESS_I18N : {};

    function t(key, fallback) {
        return i18n[key] || fallback || key;
    }

    var objects = [];
    var profile = null;
    var changes = {};
    var defaultChanged = false;
    var saving = false;

    var typeIcons = {
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

    function getTypeIcon(type) {
        return typeIcons[type] || 'bi-device-hdd';
    }

    function init() {
        var title = isNew
            ? t('new_profile', 'New Profile')
            : t('edit_profile', 'Edit Profile') + ': ' + profileName;
        document.getElementById('pageTitle').textContent = title;
        document.getElementById('cardTitle').textContent = isNew
            ? t('new_profile', 'New Profile')
            : profileName;

        if (!isNew) {
            document.getElementById('profileName').value = profileName;
            document.getElementById('profileName').disabled = true;
        }

        document.getElementById('btnSave').addEventListener('click', save);
        document.getElementById('searchFilter').addEventListener('input', applyFilters);
        document.getElementById('typeFilter').addEventListener('change', applyFilters);
        document.getElementById('profileDefault').addEventListener('change', onDefaultChange);

        document.getElementById('btnSetAllFull').addEventListener('click', function() { bulkSet('full'); });
        document.getElementById('btnSetAllReadonly').addEventListener('click', function() { bulkSet('readonly'); });
        document.getElementById('btnSetAllHidden').addEventListener('click', function() { bulkSet('hidden'); });
        document.getElementById('btnSetAllDefault').addEventListener('click', function() { bulkSet(''); });

        window.addEventListener('beforeunload', function(e) {
            if (!saving && hasChanges()) {
                e.preventDefault();
                e.returnValue = '';
            }
        });

        load();
    }

    function hasChanges() {
        if (isNew) {
            return document.getElementById('profileName').value.trim() !== '';
        }
        return defaultChanged || Object.keys(changes).length > 0;
    }

    function load() {
        var fetches = [
            fetch('/api/access').then(function(r) { return r.json(); })
        ];

        if (!isNew) {
            fetches.push(
                fetch('/api/access/profiles').then(function(r) { return r.json(); })
            );
        }

        Promise.all(fetches).then(function(results) {
            objects = results[0].objects || [];

            if (!isNew && results[1]) {
                var profiles = results[1].profiles || [];
                profile = profiles.find(function(p) { return p.name === profileName; });
                if (!profile) {
                    VCLU.showToast(t('not_found', 'Profile not found.'), 'danger');
                    window.location.href = '/access/profiles';
                    return;
                }
                document.getElementById('profileDefault').value = profile.default || 'full';
            } else {
                profile = { name: '', default: 'hidden', objects: {} };
            }

            render();
        }).catch(function(err) {
            document.getElementById('objectsLoading').style.display = 'none';
            document.getElementById('objectsEmpty').style.display = 'block';
            document.getElementById('objectsEmpty').innerHTML =
                '<i class="bi bi-exclamation-triangle"></i> ' + VCLU.esc(err.message);
        });
    }

    function render() {
        document.getElementById('objectsLoading').style.display = 'none';

        if (objects.length === 0) {
            document.getElementById('objectsEmpty').style.display = 'block';
            document.getElementById('objectsTable').style.display = 'none';
            document.getElementById('objectCount').textContent = '0';
            return;
        }

        document.getElementById('objectsEmpty').style.display = 'none';
        document.getElementById('objectsTable').style.display = 'block';

        var tbody = document.getElementById('objectsTableBody');
        tbody.innerHTML = '';

        var profileObjects = (profile && profile.objects) ? profile.objects : {};
        var defaultLevel = document.getElementById('profileDefault').value;

        objects.forEach(function(obj) {
            var row = document.createElement('tr');
            var displayName = obj.name || obj.path.split('.').pop();
            var currentLevel = profileObjects[obj.path];

            row.dataset.path = obj.path;
            row.dataset.type = obj.type || '';
            row.dataset.name = displayName.toLowerCase();

            var icon = getTypeIcon(obj.type);

            row.innerHTML =
                '<td><i class="bi ' + icon + '"></i> ' + VCLU.esc(displayName) +
                    '<br><code class="small text-muted">' + VCLU.esc(obj.path) + '</code></td>' +
                '<td><small>' + VCLU.esc(obj.type) + '</small></td>' +
                '<td class="text-center">' + createSelect(obj.path, currentLevel, defaultLevel) + '</td>';

            tbody.appendChild(row);
        });

        populateTypeFilter();
        updateCount();
        updateChangeCount();
    }

    function createSelect(path, currentLevel, defaultLevel) {
        var isOverride = currentLevel !== undefined;
        var selectedValue = isOverride ? currentLevel : '';

        var defaultLabel = t('default_label', 'Default') + ' (' + levelLabel(defaultLevel) + ')';

        var html = '<select class="form-select form-select-sm profile-access-select" ' +
                   'style="width: auto; margin: 0 auto;' +
                   (isOverride ? ' border-color: #667EEA;' : '') + '" ' +
                   'data-path="' + VCLU.esc(path) + '">';

        html += '<option value=""' + (selectedValue === '' ? ' selected' : '') + '>' + VCLU.esc(defaultLabel) + '</option>';
        html += '<option value="full"' + (selectedValue === 'full' ? ' selected' : '') + '>' + t('level_full', 'Full') + '</option>';
        html += '<option value="readonly"' + (selectedValue === 'readonly' ? ' selected' : '') + '>' + t('level_readonly', 'Readonly') + '</option>';
        html += '<option value="hidden"' + (selectedValue === 'hidden' ? ' selected' : '') + '>' + t('level_hidden', 'Hidden') + '</option>';

        html += '</select>';
        return html;
    }

    function levelLabel(level) {
        if (level === 'full') return t('level_full', 'Full');
        if (level === 'readonly') return t('level_readonly', 'Readonly');
        if (level === 'hidden') return t('level_hidden', 'Hidden');
        return level;
    }

    function populateTypeFilter() {
        var filter = document.getElementById('typeFilter');
        if (!filter) return;

        var types = {};
        objects.forEach(function(obj) {
            if (obj.type) {
                types[obj.type] = (types[obj.type] || 0) + 1;
            }
        });

        var currentValue = filter.value;
        filter.innerHTML = '<option value="">' + t('all_types', 'All types') + '</option>';

        Object.keys(types).sort().forEach(function(type) {
            var option = document.createElement('option');
            option.value = type;
            option.textContent = type + ' (' + types[type] + ')';
            if (type === currentValue) option.selected = true;
            filter.appendChild(option);
        });
    }

    function applyFilters() {
        var search = document.getElementById('searchFilter').value.toLowerCase();
        var typeFilter = document.getElementById('typeFilter').value;

        var rows = document.getElementById('objectsTableBody').querySelectorAll('tr');
        rows.forEach(function(row) {
            var matchSearch = !search ||
                row.dataset.name.indexOf(search) !== -1 ||
                row.dataset.path.toLowerCase().indexOf(search) !== -1;
            var matchType = !typeFilter || row.dataset.type === typeFilter;
            row.style.display = (matchSearch && matchType) ? '' : 'none';
        });

        updateCount();
    }

    function updateCount() {
        var badge = document.getElementById('objectCount');
        if (!badge) return;

        var objLabel = t('objects_label', 'objects');
        var total = document.getElementById('objectsTableBody').querySelectorAll('tr').length;
        var visible = document.getElementById('objectsTableBody').querySelectorAll('tr:not([style*="display: none"])').length;

        badge.textContent = (visible === total) ? total + ' ' + objLabel : visible + ' / ' + total + ' ' + objLabel;
    }

    // --- Change tracking ---

    document.addEventListener('change', function(e) {
        if (!e.target.classList.contains('profile-access-select')) return;

        var path = e.target.dataset.path;
        var value = e.target.value;

        var originalObjects = (profile && profile.objects) ? profile.objects : {};
        var originalValue = originalObjects[path];
        var originalIsDefault = originalValue === undefined;

        if (value === '') {
            if (originalIsDefault) {
                delete changes[path];
            } else {
                changes[path] = null;
            }
            e.target.style.borderColor = '';
        } else {
            if (!originalIsDefault && value === originalValue) {
                delete changes[path];
                e.target.style.borderColor = '#667EEA';
            } else if (originalIsDefault && value === document.getElementById('profileDefault').value) {
                delete changes[path];
                e.target.style.borderColor = '';
            } else {
                changes[path] = value;
                e.target.style.borderColor = '#667EEA';
            }
        }

        updateChangeCount();
    });

    function onDefaultChange() {
        var originalDefault = (profile && profile.default) ? profile.default : 'hidden';
        var newDefault = document.getElementById('profileDefault').value;
        defaultChanged = (newDefault !== originalDefault);

        var label = t('default_label', 'Default') + ' (' + levelLabel(newDefault) + ')';
        document.querySelectorAll('.profile-access-select').forEach(function(select) {
            var firstOption = select.querySelector('option[value=""]');
            if (firstOption) {
                firstOption.textContent = label;
            }
        });

        updateChangeCount();
    }

    function updateChangeCount() {
        var count = Object.keys(changes).length + (defaultChanged ? 1 : 0);
        if (isNew) count = 1;

        var badge = document.getElementById('changeCount');
        var btn = document.getElementById('btnSave');

        if (count > 0 || isNew) {
            badge.style.display = 'inline';
            badge.textContent = isNew ? t('new_label', 'new') : count + ' ' + (count === 1 ? t('change', 'change') : t('changes', 'change(s)'));
            btn.disabled = false;
        } else {
            badge.style.display = 'none';
            btn.disabled = true;
        }
    }

    // --- Bulk operations ---

    function bulkSet(level) {
        var rows = document.getElementById('objectsTableBody').querySelectorAll('tr:not([style*="display: none"])');
        rows.forEach(function(row) {
            var select = row.querySelector('.profile-access-select');
            if (select && select.value !== level) {
                select.value = level;
                select.dispatchEvent(new Event('change', { bubbles: true }));
            }
        });
    }

    // --- Save ---

    function save() {
        var name = document.getElementById('profileName').value.trim();
        if (!name) {
            VCLU.showToast(t('profile_name_required', 'Profile name is required.'), 'warning');
            document.getElementById('profileName').focus();
            return;
        }

        var defaultLevel = document.getElementById('profileDefault').value;

        var originalObjects = (profile && profile.objects) ? profile.objects : {};
        var newObjects = {};

        for (var path in originalObjects) {
            newObjects[path] = originalObjects[path];
        }

        for (var changedPath in changes) {
            if (changes[changedPath] === null) {
                delete newObjects[changedPath];
            } else {
                newObjects[changedPath] = changes[changedPath];
            }
        }

        for (var p in newObjects) {
            if (newObjects[p] === defaultLevel) {
                delete newObjects[p];
            }
        }

        var btn = document.getElementById('btnSave');
        btn.disabled = true;
        btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span> ' + t('saving', 'Saving...');

        saving = true;

        fetch('/api/access/profiles', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                name: name,
                default: defaultLevel,
                objects: newObjects
            })
        })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                VCLU.showToast('Error: ' + data.error, 'danger');
                saving = false;
                btn.disabled = false;
                btn.innerHTML = '<i class="bi bi-check-lg"></i> ' + t('save', 'Save');
                return;
            }

            changes = {};
            defaultChanged = false;

            if (isNew) {
                window.location.href = '/access/profiles/edit?name=' + encodeURIComponent(name);
            } else {
                profile.objects = newObjects;
                profile.default = defaultLevel;
                updateChangeCount();
                btn.innerHTML = '<i class="bi bi-check-lg"></i> ' + t('saved', 'Saved!');
                setTimeout(function() {
                    btn.innerHTML = '<i class="bi bi-check-lg"></i> ' + t('save', 'Save');
                }, 2000);
            }
        })
        .catch(function(err) {
            VCLU.showToast('Error: ' + err.message, 'danger');
            saving = false;
            btn.disabled = false;
            btn.innerHTML = '<i class="bi bi-check-lg"></i> ' + t('save', 'Save');
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
