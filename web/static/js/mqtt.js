/**
 * MqttManager - MQTT broker, client and access management
 * Uses prototype pattern for modularity
 * @param {Object} options - Configuration options
 */
function MqttManager(options) {
    this.options = options || {};
    this.refreshInterval = this.options.refreshInterval || 5000;
    this.intervalId = null;
    this.elements = {};
}

/**
 * Initialize MQTT manager
 */
MqttManager.prototype.init = function() {
    var self = this;

    // Cache DOM elements — Broker
    this.elements = {
        brokerEnabled: document.getElementById('brokerEnabled'),
        brokerConfigFields: document.getElementById('brokerConfigFields'),
        brokerStatusBadge: document.getElementById('brokerStatusBadge'),
        brokerError: document.getElementById('brokerError'),
        brokerErrorText: document.getElementById('brokerErrorText'),
        saveBrokerBtn: document.getElementById('saveBrokerBtn'),
        statClients: document.getElementById('statClients'),
        statMessages: document.getElementById('statMessages'),
        statSubscriptions: document.getElementById('statSubscriptions'),
        statRetained: document.getElementById('statRetained'),
        brokerClientsLoading: document.getElementById('brokerClientsLoading'),
        brokerClientsEmpty: document.getElementById('brokerClientsEmpty'),
        brokerClientsTable: document.getElementById('brokerClientsTable'),
        brokerClientsTableBody: document.getElementById('brokerClientsTableBody'),

        // Client tab
        clientEnabled: document.getElementById('clientEnabled'),
        clientConfigFields: document.getElementById('clientConfigFields'),
        clientStatusBadge: document.getElementById('clientStatusBadge'),
        clientStatusError: document.getElementById('clientStatusError'),
        clientStatusErrorText: document.getElementById('clientStatusErrorText'),
        saveClientBtn: document.getElementById('saveClientBtn'),
        testClientBtn: document.getElementById('testClientBtn'),

        // Lua clients
        clientsLoading: document.getElementById('clientsLoading'),
        clientsEmpty: document.getElementById('clientsEmpty'),
        clientsTable: document.getElementById('clientsTable'),
        clientsTableBody: document.getElementById('clientsTableBody'),
        clientStatTotal: document.getElementById('clientStatTotal'),
        clientStatConnected: document.getElementById('clientStatConnected'),
        clientStatDropped: document.getElementById('clientStatDropped'),

        // HA Integration
        haCommandsBtn: document.getElementById('haCommandsBtn'),
        haResult: document.getElementById('haResult'),

        // Test
        testTopic: document.getElementById('testTopic'),
        testPayload: document.getElementById('testPayload'),
        testResult: document.getElementById('testResult')
    };

    // Bind broker toggle
    if (this.elements.brokerEnabled) {
        this.elements.brokerEnabled.addEventListener('change', function() {
            self.toggleBrokerConfig(this.checked);
        });
    }


    // Load initial data for visible tab
    this.loadBrokerStats();
    this.loadBrokerClients();
    this.loadClientStatus();
    this.loadClientStats();

    // Start auto-refresh
    this.startAutoRefresh();

    // Update tab in URL when switching
    document.querySelectorAll('[data-bs-toggle="tab"]').forEach(function(tab) {
        tab.addEventListener('shown.bs.tab', function(e) {
            var tabId = e.target.id.replace('tab-', '');
            var url = new URL(window.location);
            url.searchParams.set('tab', tabId);
            history.replaceState(null, '', url);
        });
        tab.addEventListener('click', function() {
            var tabId = this.id.replace('tab-', '');
            var url = new URL(window.location);
            url.searchParams.set('tab', tabId);
            history.replaceState(null, '', url);
        });
    });
};

// ==================== BROKER TAB ====================

MqttManager.prototype.toggleBrokerConfig = function(show) {
    if (this.elements.brokerConfigFields) {
        this.elements.brokerConfigFields.style.display = show ? 'block' : 'none';
    }
};

MqttManager.prototype.loadBrokerStats = function() {
    var self = this;
    fetch('/api/mqtt/broker/stats')
        .then(function(r) { return r.json(); })
        .then(function(stats) { self._updateBrokerStatus(stats); })
        .catch(function() {
            if (self.elements.brokerStatusBadge) {
                self.elements.brokerStatusBadge.className = 'badge bg-danger';
                self.elements.brokerStatusBadge.textContent = 'Error';
            }
        });
};

MqttManager.prototype._updateBrokerStatus = function(stats) {
    var badge = this.elements.brokerStatusBadge;
    var errorDiv = this.elements.brokerError;
    if (!badge) return;

    if (stats.running) {
        badge.className = 'badge bg-success';
        badge.innerHTML = '<i class="bi bi-check-circle"></i> Running on :' + stats.port;
        if (errorDiv) errorDiv.style.display = 'none';
    } else {
        badge.className = 'badge bg-secondary';
        badge.innerHTML = '<i class="bi bi-stop-circle"></i> Stopped';
        if (stats.lastError && errorDiv) {
            errorDiv.style.display = 'block';
            this.elements.brokerErrorText.textContent = stats.lastError;
        }
    }

    if (this.elements.statClients) this.elements.statClients.textContent = stats.clientsConnected || 0;
    if (this.elements.statMessages) this.elements.statMessages.textContent = (stats.messagesReceived || 0) + ' / ' + (stats.messagesSent || 0);
    if (this.elements.statSubscriptions) this.elements.statSubscriptions.textContent = stats.subscriptions || 0;
    if (this.elements.statRetained) this.elements.statRetained.textContent = stats.retainedMessages || 0;
};

MqttManager.prototype.loadBrokerClients = function() {
    var self = this;
    fetch('/api/mqtt/broker/clients')
        .then(function(r) { return r.json(); })
        .then(function(data) { self._renderBrokerClients(data.clients || []); })
        .catch(function() {
            if (self.elements.brokerClientsLoading) self.elements.brokerClientsLoading.style.display = 'none';
            if (self.elements.brokerClientsEmpty) {
                self.elements.brokerClientsEmpty.style.display = 'block';
                self.elements.brokerClientsEmpty.className = 'alert alert-danger';
                self.elements.brokerClientsEmpty.innerHTML = '<i class="bi bi-exclamation-triangle"></i> Error loading broker clients';
            }
        });
};

MqttManager.prototype._renderBrokerClients = function(clients) {
    if (this.elements.brokerClientsLoading) this.elements.brokerClientsLoading.style.display = 'none';
    if (!clients || clients.length === 0) {
        if (this.elements.brokerClientsEmpty) this.elements.brokerClientsEmpty.style.display = 'block';
        if (this.elements.brokerClientsTable) this.elements.brokerClientsTable.style.display = 'none';
        return;
    }
    if (this.elements.brokerClientsEmpty) this.elements.brokerClientsEmpty.style.display = 'none';
    if (this.elements.brokerClientsTable) this.elements.brokerClientsTable.style.display = 'block';

    var self = this;
    this.elements.brokerClientsTableBody.innerHTML = '';
    clients.forEach(function(client) {
        var row = document.createElement('tr');
        var statusClass = client.connected ? 'text-success' : 'text-danger';
        var statusIcon = client.connected ? 'bi-check-circle-fill' : 'bi-x-circle-fill';
        var statusText = client.connected ? 'Connected' : 'Disconnected';
        var subs = client.subscriptions ? client.subscriptions.join(', ') : '-';
        row.innerHTML =
            '<td><code>' + VCLU.esc(client.id) + '</code></td>' +
            '<td><code>' + VCLU.esc(client.remoteAddr || '-') + '</code></td>' +
            '<td>' + (client.username || '<em class="text-muted">anonymous</em>') + '</td>' +
            '<td><small>' + VCLU.esc(subs) + '</small></td>' +
            '<td class="' + statusClass + '"><i class="bi ' + statusIcon + '"></i> ' + statusText + '</td>';
        self.elements.brokerClientsTableBody.appendChild(row);
    });
};

MqttManager.prototype.saveBrokerConfig = function() {
    var self = this;
    var btn = this.elements.saveBrokerBtn;
    if (!btn) return;
    btn.disabled = true;
    btn.innerHTML = '<i class="bi bi-hourglass-split"></i> Saving...';

    var data = {
        enabled: document.getElementById('brokerEnabled').checked,
        port: parseInt(document.getElementById('brokerPort').value) || 1883,
        wsPort: parseInt(document.getElementById('brokerWSPort').value) || 0,
        username: document.getElementById('brokerUsername').value,
        password: document.getElementById('brokerPassword').value
    };

    fetch('/api/mqtt/broker/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
    })
    .then(function(r) { return r.json(); })
    .then(function(result) {
        if (result.error) {
            VCLU.showToast('Error: ' + result.error, 'danger');
        } else {
            VCLU.showToast('Broker settings saved!', 'success');
            self.loadBrokerStats();
        }
    })
    .catch(function(error) { VCLU.showToast('Error: ' + error.message, 'danger'); })
    .finally(function() {
        btn.disabled = false;
        btn.innerHTML = '<i class="bi bi-save"></i> Save Broker Settings';
    });
};

// ==================== CLIENT TAB ====================

MqttManager.prototype.loadClientStatus = function() {
    var self = this;
    fetch('/api/mqtt/client/config')
        .then(function(r) { return r.json(); })
        .then(function(data) { self._updateClientStatus(data); })
        .catch(function() {
            if (self.elements.clientStatusBadge) {
                self.elements.clientStatusBadge.className = 'badge bg-danger';
                self.elements.clientStatusBadge.textContent = 'Error';
            }
        });
};

MqttManager.prototype._updateClientStatus = function(data) {
    var badge = this.elements.clientStatusBadge;
    var errorDiv = this.elements.clientStatusError;
    if (!badge) return;

    if (errorDiv) errorDiv.style.display = 'none';

    if (!data.enabled) {
        badge.className = 'badge bg-secondary';
        badge.innerHTML = '<i class="bi bi-stop-circle"></i> Disabled';
    } else if (data.status && data.status.connected) {
        var hostStr = (data.status.host || data.host || 'localhost').replace('tcp://', '');
        badge.className = 'badge bg-success';
        badge.innerHTML = '<i class="bi bi-check-circle"></i> Connected to ' + VCLU.esc(hostStr + ':' + (data.status.port || data.port));
    } else {
        badge.className = 'badge bg-danger';
        badge.innerHTML = '<i class="bi bi-x-circle"></i> Disconnected';
        if (data.status && data.status.lastError && errorDiv) {
            errorDiv.style.display = 'block';
            this.elements.clientStatusErrorText.textContent = data.status.lastError;
        }
    }
};

MqttManager.prototype.saveClientConfig = function() {
    var self = this;
    var btn = this.elements.saveClientBtn;
    if (!btn) return;
    btn.disabled = true;
    btn.innerHTML = '<i class="bi bi-hourglass-split"></i> Saving...';

    var data = {
        enabled: document.getElementById('clientEnabled').checked,
        host: document.getElementById('clientHost').value,
        port: parseInt(document.getElementById('clientPort').value) || 1883,
        clientId: document.getElementById('clientClientId').value,
        username: document.getElementById('clientUsername').value,
        password: document.getElementById('clientPassword').value,
        haIntegration: document.getElementById('clientHAIntegration').checked
    };

    fetch('/api/mqtt/client/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
    })
    .then(function(r) { return r.json(); })
    .then(function(result) {
        if (result.error && !result.success) {
            VCLU.showToast('Error: ' + result.error, 'danger');
        } else if (result.connected) {
            VCLU.showToast('Connected!', 'success');
        } else if (result.error) {
            VCLU.showToast('Saved, but connection failed: ' + result.error, 'warning');
        } else {
            VCLU.showToast('Client settings saved', 'success');
        }
        self.loadClientStatus();
        self.loadClientStats();
    })
    .catch(function(error) { VCLU.showToast('Error: ' + error.message, 'danger'); })
    .finally(function() {
        btn.disabled = false;
        btn.innerHTML = '<i class="bi bi-save"></i> Save';
    });
};

MqttManager.prototype.testClientConnection = function() {
    var self = this;
    var btn = this.elements.testClientBtn;
    if (!btn) return;
    btn.disabled = true;
    btn.innerHTML = '<i class="bi bi-hourglass-split"></i> Testing...';

    var data = {
        host: document.getElementById('clientHost').value || 'localhost',
        port: parseInt(document.getElementById('clientPort').value) || 1883,
        username: document.getElementById('clientUsername').value,
        password: document.getElementById('clientPassword').value
    };

    fetch('/api/mqtt/client/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
    })
    .then(function(r) { return r.json(); })
    .then(function(result) {
        if (result.success) {
            VCLU.showToast('Connection successful!', 'success');
        } else {
            VCLU.showToast('Connection failed: ' + (result.error || 'unknown error'), 'danger');
        }
    })
    .catch(function(error) { VCLU.showToast('Error: ' + error.message, 'danger'); })
    .finally(function() {
        btn.disabled = false;
        btn.innerHTML = '<i class="bi bi-lightning"></i> Test Connection';
    });
};

MqttManager.prototype.loadClientStats = function() {
    var self = this;
    fetch('/api/mqtt/clients/stats')
        .then(function(r) { return r.json(); })
        .then(function(stats) { self._renderClientStats(stats); })
        .catch(function() {
            if (self.elements.clientsLoading) self.elements.clientsLoading.style.display = 'none';
            if (self.elements.clientsEmpty) {
                self.elements.clientsEmpty.style.display = 'block';
                self.elements.clientsEmpty.innerHTML = '<i class="bi bi-exclamation-triangle"></i> Error loading client stats';
            }
        });
};

MqttManager.prototype._renderClientStats = function(stats) {
    if (this.elements.clientsLoading) this.elements.clientsLoading.style.display = 'none';

    if (this.elements.clientStatTotal) this.elements.clientStatTotal.textContent = stats.totalClients || 0;
    if (this.elements.clientStatConnected) this.elements.clientStatConnected.textContent = stats.connectedClients || 0;
    if (this.elements.clientStatDropped) this.elements.clientStatDropped.textContent = stats.droppedMessages || 0;

    if (!stats.clients || stats.clients.length === 0) {
        if (this.elements.clientsEmpty) this.elements.clientsEmpty.style.display = 'block';
        if (this.elements.clientsTable) this.elements.clientsTable.style.display = 'none';
        return;
    }

    if (this.elements.clientsEmpty) this.elements.clientsEmpty.style.display = 'none';
    if (this.elements.clientsTable) this.elements.clientsTable.style.display = 'block';

    var self = this;
    this.elements.clientsTableBody.innerHTML = '';
    stats.clients.forEach(function(client) {
        var row = document.createElement('tr');
        var statusClass = client.connected ? 'text-success' : 'text-danger';
        var statusIcon = client.connected ? 'bi-check-circle-fill' : 'bi-x-circle-fill';
        var statusText = client.connected ? 'Connected' : (client.lastError || 'Disconnected');
        row.innerHTML =
            '<td><code>' + VCLU.esc(client.instanceId) + '</code></td>' +
            '<td><code>' + VCLU.esc(client.url + ':' + client.port) + '</code></td>' +
            '<td><code>' + VCLU.esc(client.clientId) + '</code></td>' +
            '<td class="' + statusClass + '"><i class="bi ' + statusIcon + '"></i> ' + VCLU.esc(statusText) + '</td>' +
            '<td>' + client.messageCount + '</td>' +
            '<td>' + (client.subscriptions ? client.subscriptions.length : 0) + '</td>';
        self.elements.clientsTableBody.appendChild(row);
    });
};

// ==================== HA + TEST ====================

MqttManager.prototype.testPublish = function() {
    var topic = this.elements.testTopic ? this.elements.testTopic.value : '';
    var payload = this.elements.testPayload ? this.elements.testPayload.value : '';
    var resultDiv = this.elements.testResult;

    fetch('/api/mqtt/test/publish', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ topic: topic, payload: payload })
    })
    .then(function(r) { return r.json(); })
    .then(function(result) {
        if (!resultDiv) return;
        resultDiv.style.display = 'block';
        if (result.error) {
            resultDiv.className = 'alert alert-danger';
            resultDiv.innerHTML = '<i class="bi bi-x-circle"></i> ' + result.error;
        } else {
            resultDiv.className = 'alert alert-success';
            resultDiv.innerHTML = '<i class="bi bi-check-circle"></i> Message published to <code>' + VCLU.esc(topic) + '</code>';
        }
    })
    .catch(function(error) {
        if (!resultDiv) return;
        resultDiv.style.display = 'block';
        resultDiv.className = 'alert alert-danger';
        resultDiv.innerHTML = '<i class="bi bi-x-circle"></i> ' + error.message;
    });
};

MqttManager.prototype.enableHACommands = function() {
    var btn = this.elements.haCommandsBtn;
    var resultDiv = this.elements.haResult;
    if (btn) { btn.disabled = true; btn.innerHTML = '<i class="bi bi-hourglass-split"></i> Enabling...'; }

    fetch('/api/ha/commands/enable', { method: 'POST' })
        .then(function(r) { return r.json(); })
        .then(function(result) {
            if (!resultDiv) return;
            resultDiv.style.display = 'block';
            if (result.error) {
                resultDiv.className = 'alert alert-danger';
                resultDiv.innerHTML = '<i class="bi bi-x-circle"></i> ' + result.error;
            } else {
                resultDiv.className = 'alert alert-success';
                resultDiv.innerHTML = '<i class="bi bi-check-circle"></i> ' + result.message;
            }
        })
        .catch(function(error) {
            if (!resultDiv) return;
            resultDiv.style.display = 'block';
            resultDiv.className = 'alert alert-danger';
            resultDiv.innerHTML = '<i class="bi bi-x-circle"></i> ' + error.message;
        })
        .finally(function() {
            if (btn) { btn.disabled = false; btn.innerHTML = '<i class="bi bi-play-circle"></i> Enable Commands'; }
        });
};

// ==================== AUTO-REFRESH ====================

MqttManager.prototype.startAutoRefresh = function() {
    var self = this;
    if (this.intervalId) clearInterval(this.intervalId);
    this.intervalId = setInterval(function() {
        self.loadBrokerStats();
        self.loadBrokerClients();
        self.loadClientStatus();
        self.loadClientStats();
    }, this.refreshInterval);
};

MqttManager.prototype.stopAutoRefresh = function() {
    if (this.intervalId) {
        clearInterval(this.intervalId);
        this.intervalId = null;
    }
};

// ==================== INIT ====================

document.addEventListener('DOMContentLoaded', function() {
    window.mqttManager = new MqttManager();
    window.mqttManager.init();
});

// Legacy functions for inline onclick
function saveBrokerConfig() { if (window.mqttManager) window.mqttManager.saveBrokerConfig(); }
function testPublish() { if (window.mqttManager) window.mqttManager.testPublish(); }
function enableHACommands() { if (window.mqttManager) window.mqttManager.enableHACommands(); }
function saveClientConfig() { if (window.mqttManager) window.mqttManager.saveClientConfig(); }
function testClientConnection() { if (window.mqttManager) window.mqttManager.testClientConnection(); }
