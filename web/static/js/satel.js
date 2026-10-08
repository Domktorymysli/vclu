/**
 * SatelManager - Satel Integra integration page (connection form,
 * panel scan, live state). Uses prototype pattern for modularity.
 * CSRF is handled globally by csrf.js; helpers come from utils.js (VCLU.*).
 *
 * @param {Object} options - { i18n: {key: translation} }
 */
function SatelManager(options) {
    this.options = options || {};
    this.i18n = this.options.i18n || {};
    this.scanData = null;
    this.stateTimer = null;
    this.statusTimer = null;
    this.elements = {};
}

/**
 * Translate a key from the satel locale section (falls back to the key).
 */
SatelManager.prototype.t = function(key) {
    return this.i18n[key] || key;
};

/**
 * Initialize: cache DOM elements, bind events, start status polling.
 */
SatelManager.prototype.init = function() {
    var self = this;

    this.elements = {
        statusBadge: document.getElementById('satelStatusBadge'),
        panelType: document.getElementById('satelPanelType'),
        zoneCount: document.getElementById('satelZoneCount'),
        partitionCount: document.getElementById('satelPartitionCount'),
        outputCount: document.getElementById('satelOutputCount'),
        restartBanner: document.getElementById('satelRestartBanner'),
        restartBtn: document.getElementById('satelRestartBtn'),

        enabled: document.getElementById('satelEnabled'),
        host: document.getElementById('satelHost'),
        port: document.getElementById('satelPort'),
        userCode: document.getElementById('satelUserCode'),
        encryptionKey: document.getElementById('satelEncryptionKey'),
        clearKey: document.getElementById('satelClearKey'),
        pollInterval: document.getElementById('satelPollInterval'),
        armMode: document.getElementById('satelArmMode'),
        allowDisarm: document.getElementById('satelAllowDisarm'),
        allowForceArm: document.getElementById('satelAllowForceArm'),
        disarmWarning: document.getElementById('satelDisarmWarning'),
        zones: document.getElementById('satelZones'),
        partitions: document.getElementById('satelPartitions'),
        outputs: document.getElementById('satelOutputs'),
        tempZones: document.getElementById('satelTempZones'),
        testBtn: document.getElementById('satelTestBtn'),
        saveBtn: document.getElementById('satelSaveBtn'),
        formStatus: document.getElementById('satelFormStatus'),

        scanBtn: document.getElementById('scanBtn'),
        scanSpinner: document.getElementById('scanSpinner'),
        scanProgressBar: document.getElementById('scanProgressBar'),
        scanProgressText: document.getElementById('scanProgressText'),
        scanError: document.getElementById('scanError'),
        scanInfo: document.getElementById('scanInfo'),
        scanResults: document.getElementById('scanResults'),
        objectsSaveBtn: document.getElementById('objectsSaveBtn'),
        objectsSaveStatus: document.getElementById('objectsSaveStatus'),

        stateTab: document.getElementById('tab-state'),
        stateUnavailable: document.getElementById('stateUnavailable'),
        stateContent: document.getElementById('stateContent'),
        stateUpdated: document.getElementById('stateUpdated'),
        statePartitions: document.getElementById('statePartitions'),
        stateZones: document.getElementById('stateZones'),
        stateOutputs: document.getElementById('stateOutputs'),
        stateTempsWrap: document.getElementById('stateTempsWrap'),
        stateTemps: document.getElementById('stateTemps')
    };

    if (this.elements.saveBtn) {
        this.elements.saveBtn.addEventListener('click', function() {
            self.saveConfig(null, self.elements.formStatus);
        });
    }
    if (this.elements.testBtn) {
        this.elements.testBtn.addEventListener('click', function() {
            self.testConnection();
        });
    }
    if (this.elements.allowDisarm) {
        this.elements.allowDisarm.addEventListener('change', function() {
            self.elements.disarmWarning.classList.toggle('d-none', !this.checked);
        });
    }
    if (this.elements.scanBtn) {
        this.elements.scanBtn.addEventListener('click', function() {
            self.scan();
        });
    }
    if (this.elements.objectsSaveBtn) {
        this.elements.objectsSaveBtn.addEventListener('click', function() {
            self.saveObjects();
        });
    }
    if (this.elements.restartBtn) {
        this.elements.restartBtn.addEventListener('click', function() {
            self.restart();
        });
    }

    // Quick select buttons (All / Named / None per scan group)
    document.querySelectorAll('[data-selgroup]').forEach(function(btn) {
        btn.addEventListener('click', function() {
            self.quickSelect(btn.getAttribute('data-selgroup'), btn.getAttribute('data-sel'));
        });
    });

    // Live state refresh only while its tab is visible
    if (this.elements.stateTab) {
        this.elements.stateTab.addEventListener('shown.bs.tab', function() { self.startStateRefresh(); });
        this.elements.stateTab.addEventListener('hidden.bs.tab', function() { self.stopStateRefresh(); });
        if (this.elements.stateTab.classList.contains('active')) {
            this.startStateRefresh();
        }
    }

    // Keep ?tab= in the URL in sync with the active tab
    document.querySelectorAll('[data-bs-toggle="tab"]').forEach(function(tab) {
        tab.addEventListener('shown.bs.tab', function(e) {
            var url = new URL(window.location);
            url.searchParams.set('tab', e.target.id.replace('tab-', ''));
            history.replaceState(null, '', url);
        });
    });

    // Enable Bootstrap tooltips (info icons on the security toggles)
    if (window.bootstrap && bootstrap.Tooltip) {
        document.querySelectorAll('[data-bs-toggle="tooltip"]').forEach(function(el) {
            new bootstrap.Tooltip(el);
        });
    }

    this.refreshStatus();
    this.loadLastScan();
    this.statusTimer = setInterval(function() { self.refreshStatus(); }, 5000);
};

// ==========================================================================
// Range helpers
// ==========================================================================

/**
 * Parse a range spec like "1-16,20" into a {number: true} set.
 */
SatelManager.prototype.parseRange = function(spec) {
    var out = {};
    (spec || '').split(',').forEach(function(part) {
        part = part.trim();
        if (!part) return;
        var m = part.match(/^(\d+)\s*-\s*(\d+)$/);
        if (m) {
            for (var n = parseInt(m[1], 10); n <= parseInt(m[2], 10); n++) out[n] = true;
        } else if (/^\d+$/.test(part)) {
            out[parseInt(part, 10)] = true;
        }
    });
    return out;
};

/**
 * Compress a list of numbers into a range spec ("1-12,15").
 */
SatelManager.prototype.compressRange = function(numbers) {
    var nums = numbers.slice().sort(function(a, b) { return a - b; });
    var parts = [];
    for (var i = 0; i < nums.length; i++) {
        var start = nums[i];
        while (i + 1 < nums.length && nums[i + 1] === nums[i] + 1) i++;
        parts.push(start === nums[i] ? String(start) : start + '-' + nums[i]);
    }
    return parts.join(',');
};

// ==========================================================================
// Status card
// ==========================================================================

SatelManager.prototype.refreshStatus = function() {
    var self = this;
    var el = this.elements;
    VCLU.fetchJSON('/api/satel/status').then(function(st) {
        var badge = el.statusBadge;
        if (st.connected) {
            badge.className = 'badge bg-success';
            badge.textContent = self.t('status_connected');
        } else if (st.running) {
            badge.className = 'badge bg-warning';
            badge.textContent = self.t('status_connecting');
        } else if (st.enabled) {
            badge.className = 'badge bg-warning';
            badge.textContent = self.t('status_restart');
        } else {
            badge.className = 'badge bg-secondary';
            badge.textContent = self.t('status_disabled');
        }
        if (st.panelType) el.panelType.textContent = st.panelType;
        if (st.zones !== undefined) {
            el.zoneCount.textContent = st.zones;
            el.partitionCount.textContent = st.partitions;
            el.outputCount.textContent = st.outputs;
        }
        if (el.scanBtn) el.scanBtn.disabled = !st.connected;
    }).catch(function() {});
};

// ==========================================================================
// Connection form
// ==========================================================================

/**
 * Collect the configuration object from the form.
 * Secrets: empty field -> null (keep stored value).
 */
SatelManager.prototype.collectConfig = function(overrides) {
    var el = this.elements;
    var userCode = el.userCode.value;
    var encKey = el.encryptionKey.value;
    var clearKey = el.clearKey.checked;
    var cfg = {
        enabled: el.enabled.checked,
        host: el.host.value.trim(),
        port: parseInt(el.port.value, 10) || 7094,
        pollInterval: parseInt(el.pollInterval.value, 10) || 1000,
        armMode: parseInt(el.armMode.value, 10) || 0,
        allowDisarm: el.allowDisarm.checked,
        allowForceArm: el.allowForceArm.checked,
        zones: el.zones.value.trim(),
        partitions: el.partitions.value.trim(),
        outputs: el.outputs.value.trim(),
        tempZones: el.tempZones.value.trim(),
        userCode: userCode !== '' ? userCode : null,
        encryptionKey: clearKey ? '' : (encKey !== '' ? encKey : null)
    };
    return Object.assign(cfg, overrides || {});
};

SatelManager.prototype.saveConfig = function(overrides, statusEl) {
    var self = this;
    statusEl.textContent = this.t('saving');
    statusEl.className = 'small text-muted';
    return VCLU.fetchJSON('/api/satel/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.collectConfig(overrides))
    }).then(function(res) {
        if (res.success) {
            statusEl.textContent = self.t('saved');
            statusEl.className = 'small text-success';
            if (res.restartRequired) {
                self.elements.restartBanner.style.display = '';
            }
        } else {
            statusEl.textContent = res.error || self.t('save_failed');
            statusEl.className = 'small text-danger';
        }
        return res;
    }).catch(function(err) {
        statusEl.textContent = err.message || self.t('save_failed');
        statusEl.className = 'small text-danger';
    });
};

SatelManager.prototype.testConnection = function() {
    var self = this;
    var el = this.elements;
    var status = el.formStatus;
    var encKey = el.encryptionKey.value;
    var clearKey = el.clearKey.checked;

    el.testBtn.disabled = true;
    status.textContent = this.t('testing');
    status.className = 'small text-muted';

    VCLU.fetchJSON('/api/satel/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            host: el.host.value.trim(),
            port: parseInt(el.port.value, 10) || 7094,
            encryptionKey: clearKey ? '' : (encKey !== '' ? encKey : null)
        })
    }).then(function(res) {
        if (res.success) {
            status.textContent = 'OK: ' + res.panel;
            status.className = 'small text-success';
        } else {
            status.textContent = res.error || self.t('test_failed');
            status.className = 'small text-danger';
        }
    }).catch(function() {
        status.textContent = self.t('request_failed');
        status.className = 'small text-danger';
    }).finally(function() {
        el.testBtn.disabled = false;
    });
};

// ==========================================================================
// Panel scan
// ==========================================================================

SatelManager.prototype.scan = function() {
    var self = this;
    var el = this.elements;

    el.scanBtn.disabled = true;
    el.scanSpinner.style.display = '';
    el.scanError.style.display = 'none';
    this.showScanProgress(null);
    var progressTimer = setInterval(function() { self.refreshScanProgress(); }, 1000);

    VCLU.fetchJSON('/api/satel/scan', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
        .then(function(res) {
            self.showScanResult(res, new Date());
        })
        .catch(function(err) {
            el.scanError.textContent = err.message || self.t('scan_failed');
            el.scanError.style.display = '';
        })
        .finally(function() {
            clearInterval(progressTimer);
            el.scanBtn.disabled = false;
            el.scanSpinner.style.display = 'none';
        });
};

// Show a scan result as editable checkboxes, the current config pre-checked.
SatelManager.prototype.showScanResult = function(res, scannedAt) {
    var el = this.elements;
    this.scanData = res;
    el.scanInfo.textContent = res.panelType + ': ' +
        res.zones.length + ' × ' + this.t('zones').toLowerCase() + ', ' +
        res.partitions.length + ' × ' + this.t('partitions').toLowerCase() + ', ' +
        res.outputs.length + ' × ' + this.t('outputs').toLowerCase() +
        ' · ' + this.t('last_scan') + ' ' + scannedAt.toLocaleString();
    this.renderScanGroup('zonesBody', res.zones, this.parseRange(el.zones.value));
    this.renderScanGroup('partitionsBody', res.partitions, this.parseRange(el.partitions.value));
    this.renderScanGroup('outputsBody', res.outputs, this.parseRange(el.outputs.value));
    el.scanResults.style.display = '';
};

// Load the last saved scan so the selection can be changed without rescanning.
SatelManager.prototype.loadLastScan = function() {
    var self = this;
    if (!this.elements.scanResults) return;
    VCLU.fetchJSON('/api/satel/scan').then(function(saved) {
        if (saved && saved.result) self.showScanResult(saved.result, new Date(saved.scannedAt));
    }).catch(function() {}); // 404 = never scanned
};

SatelManager.prototype.refreshScanProgress = function() {
    var self = this;
    VCLU.fetchJSON('/api/satel/status').then(function(st) {
        if (st.scan && st.scan.running) self.showScanProgress(st.scan);
    }).catch(function() {});
};

SatelManager.prototype.showScanProgress = function(sp) {
    var el = this.elements;
    if (!el.scanProgressBar) return;
    if (!sp || !sp.total) {
        el.scanProgressBar.style.width = '0%';
        el.scanProgressText.textContent = '';
        return;
    }
    var pct = Math.min(100, Math.round(sp.done * 100 / sp.total));
    el.scanProgressBar.style.width = pct + '%';
    el.scanProgressText.textContent = this.t(sp.phase) + ' · ' + sp.done + '/' + sp.total + ' (' + pct + '%)';
};

SatelManager.prototype.renderScanGroup = function(bodyId, devices, configured) {
    var self = this;
    var html = '';
    devices.forEach(function(d) {
        var checked = configured[d.number] ? ' checked' : '';
        var nameHtml = d.name ? VCLU.esc(d.name)
            : '<span class="text-muted">' + VCLU.esc(self.t('no_name')) + '</span>';
        html += '<tr><td><input type="checkbox" class="form-check-input scan-cb" data-number="' + d.number + '"' + checked + '></td>';
        html += '<td class="font-monospace">' + d.number + '</td><td>' + nameHtml + '</td></tr>';
    });
    document.getElementById(bodyId).innerHTML = html ||
        '<tr><td colspan="3" class="text-muted p-2">' + VCLU.esc(this.t('nothing_found')) + '</td></tr>';
};

// Factory names like "Wyjście 12" / "Wejście 3" / "Output 12" mean the device was never named.
SatelManager.prototype.isDefaultName = function(name) {
    return /^(wyj[sś]cie|wej[sś]cie|output|input|strefa|zone|partycja|partition)\s*\d+$/i.test((name || '').trim());
};

SatelManager.prototype.quickSelect = function(group, mode) {
    var self = this;
    var devices = (this.scanData && this.scanData[group]) || [];
    var named = {};
    devices.forEach(function(d) { if (d.name && !self.isDefaultName(d.name)) named[d.number] = true; });
    document.getElementById(group + 'Body').querySelectorAll('.scan-cb').forEach(function(cb) {
        var n = parseInt(cb.getAttribute('data-number'), 10);
        cb.checked = mode === 'all' || (mode === 'named' && !!named[n]);
    });
};

SatelManager.prototype.collectChecked = function(bodyId) {
    var out = [];
    document.getElementById(bodyId).querySelectorAll('.scan-cb:checked').forEach(function(cb) {
        out.push(parseInt(cb.getAttribute('data-number'), 10));
    });
    return out;
};

SatelManager.prototype.saveObjects = function() {
    var el = this.elements;
    var zones = this.compressRange(this.collectChecked('zonesBody'));
    var partitions = this.compressRange(this.collectChecked('partitionsBody'));
    var outputs = this.compressRange(this.collectChecked('outputsBody'));
    el.zones.value = zones;
    el.partitions.value = partitions;
    el.outputs.value = outputs;
    this.saveConfig({ zones: zones, partitions: partitions, outputs: outputs }, el.objectsSaveStatus);
};

// ==========================================================================
// Live state
// ==========================================================================

SatelManager.prototype.chip = function(label, cls, title) {
    return '<span class="badge ' + cls + '" title="' + VCLU.esc(title || '') + '">' + VCLU.esc(label) + '</span>';
};

SatelManager.prototype.refreshState = function() {
    var self = this;
    var el = this.elements;
    VCLU.fetchJSON('/api/satel/state').then(function(st) {
        el.stateUnavailable.style.display = st.connected ? 'none' : '';
        el.stateContent.style.display = '';
        el.stateUpdated.textContent = new Date().toLocaleTimeString();

        el.statePartitions.innerHTML = (st.partitions || []).map(function(p) {
            var cls = p.alarm || p.fireAlarm ? 'bg-danger' : (p.armed ? 'bg-warning text-dark' : 'bg-success');
            var label = (p.name || (self.t('partition') + ' ' + p.number)) + ': ' +
                (p.alarm ? self.t('alarm') : p.fireAlarm ? self.t('fire')
                    : p.armed ? self.t('armed') : self.t('disarmed')) +
                (p.entryTime ? ' ' + self.t('entry') : '') + (p.exitTime ? ' ' + self.t('exit') : '');
            return self.chip(label, cls);
        }).join(' ') || '<span class="text-muted">—</span>';

        el.stateZones.innerHTML = (st.zones || []).map(function(z) {
            var cls = z.alarm || z.tamper ? 'bg-danger' : z.violated ? 'bg-warning text-dark'
                : z.bypass ? 'bg-secondary' : 'bg-success';
            var suffix = z.alarm ? ' ' + self.t('alarm') : z.tamper ? ' ' + self.t('tamper')
                : z.violated ? ' ●' : z.bypass ? ' ' + self.t('bypass') : '';
            return self.chip((z.name || (self.t('zone') + ' ' + z.number)) + suffix, cls, self.t('zone') + ' ' + z.number);
        }).join(' ') || '<span class="text-muted">—</span>';

        el.stateOutputs.innerHTML = (st.outputs || []).map(function(o) {
            return self.chip(
                (o.name || (self.t('output') + ' ' + o.number)) + ' ' + (o.active ? self.t('on') : self.t('off')),
                o.active ? 'bg-primary' : 'bg-secondary',
                self.t('output') + ' ' + o.number);
        }).join(' ') || '<span class="text-muted">—</span>';

        var temps = Object.keys(st.temps || {});
        el.stateTempsWrap.style.display = temps.length ? '' : 'none';
        el.stateTemps.innerHTML = temps.map(function(n) {
            return self.chip(self.t('zone') + ' ' + n + ': ' + st.temps[n].toFixed(1) + ' °C', 'bg-info text-dark');
        }).join(' ');
    }).catch(function() {
        el.stateUnavailable.style.display = '';
        el.stateContent.style.display = 'none';
    });
};

SatelManager.prototype.startStateRefresh = function() {
    var self = this;
    this.refreshState();
    if (!this.stateTimer) {
        this.stateTimer = setInterval(function() { self.refreshState(); }, 3000);
    }
};

SatelManager.prototype.stopStateRefresh = function() {
    if (this.stateTimer) {
        clearInterval(this.stateTimer);
        this.stateTimer = null;
    }
};

// ==========================================================================
// Restart
// ==========================================================================

SatelManager.prototype.restart = function() {
    if (!confirm(this.t('restart_confirm'))) return;

    if (this.elements.restartBtn) {
        this.elements.restartBtn.disabled = true;
    }

    fetch('/api/restart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
    }).catch(function() {});

    // Use the shared restart overlay (spinner + /api/health polling + redirect)
    // instead of a blind timed reload.
    RestartOverlay.show({
        title: this.t('restarting_title'),
        subtitle: this.t('please_wait'),
        redirectUrl: '/satel'
    });
};

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', function() {
    var pageData = window.SATEL_PAGE || {};
    window.satelManager = new SatelManager({ i18n: pageData.i18n });
    window.satelManager.init();
});
