/**
 * CloudManager - vclu cloud pairing and connection status
 * @param {Object} options - { i18n: {key: translation}, status: initial status }
 */
function CloudManager(options) {
    this.options = options || {};
    this.i18n = this.options.i18n || {};
    this.status = this.options.status || { state: 'off' };
    this.timer = null;
    this.el = {};
}

CloudManager.prototype.t = function(key) {
    return this.i18n[key] || key;
};

CloudManager.prototype.init = function() {
    var self = this;
    ['cloudAlert', 'cloudBadge', 'cloudOff', 'cloudPairing', 'cloudPaired', 'cloudPairBtn',
     'cloudCancelBtn', 'cloudUnpairBtn', 'cloudReconnectBtn', 'cloudVerifyLink', 'cloudCode',
     'cloudExpires', 'cloudHome', 'cloudServer', 'cloudSince', 'cloudSinceLabel', 'cloudEnabled',
     'cloudDanger'].forEach(function(id) {
        self.el[id] = document.getElementById(id);
    });
    this.el.cloudPairBtn.addEventListener('click', function() { self.call('/api/cloud/pair', this); });
    this.el.cloudCancelBtn.addEventListener('click', function() { self.call('/api/cloud/pair/cancel', this); });
    this.el.cloudReconnectBtn.addEventListener('click', function() { self.call('/api/cloud/reconnect', this); });
    this.el.cloudEnabled.addEventListener('change', function() {
        self.call('/api/cloud/enabled?on=' + (this.checked ? '1' : '0'), this);
    });
    this.el.cloudUnpairBtn.addEventListener('click', function() {
        if (confirm(self.t('disconnect_confirm'))) {
            self.call('/api/cloud/unpair', this);
        }
    });
    this.render();
    this.schedule();
};

// call POSTs to the API; answers carry the new status (directly or in .status).
CloudManager.prototype.call = function(url, btn) {
    var self = this;
    btn.disabled = true;
    this.alert(null);
    fetch(url, { method: 'POST' })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.error) {
                self.alert('danger', self.t('error') + ': ' + data.error);
            } else if (data.warning) {
                self.alert('warning', data.warning);
            } else if (url === '/api/cloud/unpair') {
                self.alert('success', self.t('disconnected'));
            }
            var st = data.status || (data.state ? data : null);
            if (st) {
                self.status = st;
                self.render();
            }
        })
        .catch(function(err) { self.alert('danger', self.t('error') + ': ' + err); })
        .then(function() {
            btn.disabled = false;
            self.schedule();
        });
};

CloudManager.prototype.refresh = function() {
    var self = this;
    fetch('/api/cloud/status')
        .then(function(r) { return r.json(); })
        .then(function(st) {
            if (st && st.state) {
                self.status = st;
                self.render();
            }
        })
        .catch(function() {})
        .then(function() { self.schedule(); });
};

// Poll fast while something is about to change.
CloudManager.prototype.schedule = function() {
    var self = this;
    clearTimeout(this.timer);
    var s = this.status.state;
    var fast = s === 'pairing' || s === 'connecting';
    this.timer = setTimeout(function() { self.refresh(); }, fast ? 2000 : 10000);
};

CloudManager.prototype.alert = function(kind, text) {
    var a = this.el.cloudAlert;
    if (!kind) {
        a.className = 'alert d-none';
        return;
    }
    a.className = 'alert alert-' + kind;
    a.textContent = text;
};

CloudManager.prototype.render = function() {
    var st = this.status, el = this.el;
    var badges = { off: 'bg-secondary', pairing: 'bg-info', connecting: 'bg-warning',
                   online: 'bg-success', offline: 'bg-danger', stopped: 'bg-danger',
                   disabled: 'bg-secondary' };
    el.cloudBadge.className = 'badge ' + (badges[st.state] || 'bg-secondary');
    el.cloudBadge.textContent = this.t('state_' + st.state);

    el.cloudOff.classList.toggle('d-none', st.state !== 'off');
    el.cloudPairing.classList.toggle('d-none', st.state !== 'pairing');
    var paired = st.state !== 'off' && st.state !== 'pairing';
    el.cloudPaired.classList.toggle('d-none', !paired);
    el.cloudDanger.classList.toggle('d-none', !paired);
    el.cloudEnabled.checked = st.state !== 'disabled';

    if (st.error && el.cloudAlert.classList.contains('d-none')) {
        var reason = this.i18n['reason_' + st.error];
        var kind = st.state === 'offline' ? 'warning' : 'secondary';
        if (st.state !== 'online') {
            this.alert(kind, reason || st.error);
        }
    } else if (!st.error && st.state === 'online') {
        this.alert(null);
    }

    if (st.state === 'pairing' && st.pairing) {
        el.cloudVerifyLink.href = st.pairing.verifyUrlFull || st.pairing.verifyUrl;
        el.cloudVerifyLink.textContent = st.pairing.verifyUrl;
        el.cloudCode.textContent = st.pairing.userCode;
        var left = Math.max(0, Math.round((new Date(st.pairing.expiresAt) - new Date()) / 1000));
        el.cloudExpires.textContent = Math.floor(left / 60) + ':' + ('0' + (left % 60)).slice(-2);
    }

    el.cloudHome.textContent = st.homeName || '—';
    el.cloudServer.textContent = st.url || '';
    el.cloudSinceLabel.textContent = this.t(st.state === 'online' ? 'online_since' :
        st.state === 'disabled' ? 'disabled_since' : 'offline_since');
    el.cloudSince.textContent = st.since ? new Date(st.since).toLocaleString() : '—';
    el.cloudReconnectBtn.classList.toggle('d-none', st.state !== 'stopped' && st.state !== 'offline');
};

document.addEventListener('DOMContentLoaded', function() {
    var pageData = window.CLOUD_PAGE || {};
    window.cloudManager = new CloudManager(pageData);
    window.cloudManager.init();
});
