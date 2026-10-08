class DeviceWizard {
    constructor(initialState, i18n) {
        this.state = {
            deviceTypeId: initialState.deviceTypeId || '',
            deviceTypeName: initialState.deviceTypeName || '',
            category: initialState.category || '',
            deviceTypeLocked: initialState.deviceTypeLocked || false,
            supportsModules: initialState.supportsModules || false,
            modules: initialState.modules || [],
            ioObjects: initialState.ioObjects || [],
            moduleTypes: initialState.moduleTypes || [],
            pin: initialState.pin || '00000000',
            broadcastEnabled: initialState.broadcastEnabled || false,
            authUsername: initialState.authUsername || ''
        };

        this.i18n = i18n || {};

        // Step labels keyed by step ID
        this.stepLabels = {
            'device-type': this.i18n.step_device_type || 'Device Type',
            'modules': this.i18n.step_modules || 'Modules',
            'grenton': this.i18n.step_keys || 'Grenton',
            'security': this.i18n.step_security || 'Security',
            'finish': this.i18n.step_finish || 'Finish'
        };

        // Compute initial step sequence
        this.stepSequence = this.computeStepSequence(this.state.category);
        this.currentStepId = this.stepSequence[0];

        this.init();
    }

    computeStepSequence(category) {
        switch ((category || '').toUpperCase()) {
            case 'STANDALONE':
                return ['device-type', 'security', 'finish'];
            case 'GATE':
                return ['device-type', 'grenton', 'security', 'finish'];
            case 'CLU':
                return ['device-type', 'modules', 'grenton', 'security', 'finish'];
            default:
                // Unknown/empty — show all steps (CLU-like); will rebuild after device selection
                return ['device-type', 'modules', 'grenton', 'security', 'finish'];
        }
    }

    init() {
        this.renderProgressBar();
        this.updateProgressIndicators();
        this.bindEvents();
        this.renderModules();
        this.updateUI();

        // If device type is already selected, enable next button
        if (this.state.deviceTypeId) {
            const nextBtn = document.querySelector('#step-device-type .wizard-next');
            if (nextBtn) nextBtn.disabled = false;
        }

        // Populate keys fields from state
        const pinInput = document.getElementById('wizardPIN');
        if (pinInput) pinInput.value = this.state.pin || '00000000';

        const broadcastEnabledCb = document.getElementById('broadcastEnabled');
        if (broadcastEnabledCb) broadcastEnabledCb.checked = this.state.broadcastEnabled;

        // Populate auth fields from state
        const authUsernameInput = document.getElementById('authUsername');
        if (authUsernameInput) authUsernameInput.value = this.state.authUsername;
    }

    renderProgressBar() {
        const container = document.getElementById('wizardProgressBar');
        if (!container) return;

        container.innerHTML = '';
        this.stepSequence.forEach((stepId, index) => {
            if (index > 0) {
                const line = document.createElement('div');
                line.className = 'wizard-step-line';
                container.appendChild(line);
            }

            const stepEl = document.createElement('div');
            stepEl.className = 'wizard-step';
            stepEl.dataset.stepId = stepId;

            const numEl = document.createElement('div');
            numEl.className = 'step-number';
            numEl.textContent = index + 1;

            const labelEl = document.createElement('div');
            labelEl.className = 'step-label';
            labelEl.textContent = this.stepLabels[stepId] || stepId;

            stepEl.appendChild(numEl);
            stepEl.appendChild(labelEl);
            container.appendChild(stepEl);
        });
    }

    updateProgressIndicators() {
        const currentIndex = this.stepSequence.indexOf(this.currentStepId);
        const container = document.getElementById('wizardProgressBar');
        if (!container) return;

        container.querySelectorAll('.wizard-step').forEach((el, index) => {
            el.classList.remove('active', 'completed');
            if (index < currentIndex) {
                el.classList.add('completed');
            } else if (index === currentIndex) {
                el.classList.add('active');
            }
        });

        container.querySelectorAll('.wizard-step-line').forEach((line, index) => {
            if (index < currentIndex) {
                line.classList.add('completed');
            } else {
                line.classList.remove('completed');
            }
        });
    }

    bindEvents() {
        // Device type selection
        document.querySelectorAll('.device-type-card:not(.disabled)').forEach(card => {
            card.addEventListener('click', () => this.selectDeviceType(card));
        });

        // Generic next/back buttons (per panel)
        document.querySelectorAll('.wizard-panel').forEach(panel => {
            const nextBtn = panel.querySelector('.wizard-next');
            const backBtn = panel.querySelector('.wizard-back');

            if (nextBtn) {
                nextBtn.addEventListener('click', () => this.next());
            }
            if (backBtn) {
                backBtn.addEventListener('click', () => this.prev());
            }
        });

        // Add module buttons
        document.getElementById('addModuleBtn')?.addEventListener('click', () => this.showAddModuleModal());
        document.getElementById('addFirstModuleBtn')?.addEventListener('click', () => this.showAddModuleModal());
        document.getElementById('confirmAddModule')?.addEventListener('click', () => this.addModule());

        // Finish button
        document.getElementById('finishBtn')?.addEventListener('click', () => this.finish());

        // Preview refresh
        document.getElementById('refreshPreviewBtn')?.addEventListener('click', () => this.loadOMPreview());
    }

    selectDeviceType(card) {
        if (this.state.deviceTypeLocked) return;

        const deviceId = card.dataset.deviceId;
        const supportsModules = card.dataset.supportsModules === 'true';
        const category = card.dataset.category || '';

        // Update UI
        document.querySelectorAll('.device-type-card').forEach(c => c.classList.remove('selected'));
        card.classList.add('selected');

        // Update state
        this.state.deviceTypeId = deviceId;
        this.state.supportsModules = supportsModules;
        this.state.category = category;
        this.state.deviceTypeName = card.querySelector('h4').textContent;

        // Enable next button
        const nextBtn = document.querySelector('#step-device-type .wizard-next');
        if (nextBtn) nextBtn.disabled = false;

        // If device doesn't support modules, clear them
        if (!supportsModules) {
            this.state.modules = [];
            this.state.ioObjects = [];
        }

        // Rebuild progress bar to reflect the selected device's steps
        this.stepSequence = this.computeStepSequence(category);
        this.renderProgressBar();
        this.updateProgressIndicators();
    }


    next() {
        const currentIndex = this.stepSequence.indexOf(this.currentStepId);
        if (currentIndex < this.stepSequence.length - 1) {
            this.goToStep(this.stepSequence[currentIndex + 1]);
        }
    }

    prev() {
        const currentIndex = this.stepSequence.indexOf(this.currentStepId);
        if (currentIndex > 0) {
            this.goToStep(this.stepSequence[currentIndex - 1]);
        }
    }

    async goToStep(stepId) {
        const leavingStepId = this.currentStepId;
        const targetIndex = this.stepSequence.indexOf(stepId);
        const currentIndex = this.stepSequence.indexOf(leavingStepId);
        const goingForward = targetIndex > currentIndex;

        // Save triggers when going forward
        if (goingForward) {
            if (leavingStepId === 'device-type' && !this.state.deviceTypeLocked) {
                const success = await this.saveDeviceType();
                if (!success) return;
                // Rebuild step sequence based on newly selected category
                this.stepSequence = this.computeStepSequence(this.state.category);
                this.renderProgressBar();
                // Navigate to step after device-type in the new sequence
                stepId = this.stepSequence[1];
            }
            if (leavingStepId === 'grenton') {
                const success = await this.saveKeys();
                if (!success) return;
            }
            if (leavingStepId === 'security') {
                const success = await this.saveSecurity();
                if (!success) return;
            }
        }

        // Hide all panels
        document.querySelectorAll('.wizard-panel').forEach(p => p.style.display = 'none');

        // Show target panel
        const panel = document.getElementById('step-' + stepId);
        if (panel) panel.style.display = 'block';

        this.currentStepId = stepId;
        this.updateProgressIndicators();

        // Load summary on finish step
        if (stepId === 'finish') {
            this.updateSummary();
            if ((this.state.category || '').toUpperCase() === 'CLU') {
                this.loadOMPreview();
            }
        }
    }

    async saveDeviceType() {
        try {
            const response = await fetch('/api/wizard/device-type', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ deviceTypeId: this.state.deviceTypeId })
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || this.i18n.err_save_device);
                return false;
            }

            this.state.category = data.deviceType.category;
            return true;
        } catch (error) {
            this.showAlert('danger', this.i18n.err_network + ': ' + error.message);
            return false;
        }
    }

    async saveKeys() {
        const pin = document.getElementById('wizardPIN')?.value || '00000000';
        const bEl = document.getElementById('broadcastEnabled');
        const broadcastEnabled = bEl ? (bEl.type === 'checkbox' ? bEl.checked : bEl.value === 'true') : false;
        const broadcastKey = document.getElementById('broadcastKey')?.value || '';
        const defaultIV = document.getElementById('defaultIV')?.value || '';
        const broadcastIV = document.getElementById('broadcastIV')?.value || '';

        // Client-side validation
        if (pin.length !== 8) {
            this.showAlert('danger', this.i18n.err_pin_length);
            return false;
        }

        try {
            const response = await fetch('/api/wizard/keys', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    pin: pin,
                    broadcastEnabled: broadcastEnabled,
                    broadcastKey: broadcastEnabled ? broadcastKey : '',
                    defaultIV: broadcastEnabled ? defaultIV : '',
                    broadcastIV: broadcastEnabled ? broadcastIV : ''
                })
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || this.i18n.err_save_keys);
                return false;
            }

            this.state.pin = pin;
            this.state.broadcastEnabled = broadcastEnabled;
            return true;
        } catch (error) {
            this.showAlert('danger', this.i18n.err_network + ': ' + error.message);
            return false;
        }
    }

    async saveSecurity() {
        const username = document.getElementById('authUsername')?.value || '';
        const password = document.getElementById('authPassword')?.value || '';
        const passwordConfirm = document.getElementById('authPasswordConfirm')?.value || '';

        if (username.length < 3) {
            this.showAlert('danger', this.i18n.err_username_min);
            return false;
        }
        if (password.length < 8) {
            this.showAlert('danger', this.i18n.err_password_min);
            return false;
        }
        if (password !== passwordConfirm) {
            this.showAlert('danger', this.i18n.err_password_match);
            return false;
        }

        try {
            const response = await fetch('/api/wizard/security', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    username: username,
                    password: password,
                    passwordConfirm: passwordConfirm
                })
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || this.i18n.err_save_security);
                return false;
            }

            this.state.authUsername = username;
            return true;
        } catch (error) {
            this.showAlert('danger', this.i18n.err_network + ': ' + error.message);
            return false;
        }
    }

    showAddModuleModal() {
        const modal = new bootstrap.Modal(document.getElementById('addModuleModal'));
        document.getElementById('moduleName').value = '';
        modal.show();
    }

    async addModule() {
        const moduleTypeId = document.getElementById('moduleType').value;
        const moduleName = document.getElementById('moduleName').value.trim();

        try {
            const response = await fetch('/api/wizard/module', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    moduleTypeId: moduleTypeId,
                    name: moduleName
                })
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || this.i18n.err_add_module);
                return;
            }

            // Update state
            this.state.modules.push(data.module);
            this.state.ioObjects = this.state.ioObjects.concat(data.ioObjects);

            // Update UI
            this.renderModules();
            this.showAlert('success', this.i18n.module_added.replace('{name}', data.module.name));

            // Close modal
            bootstrap.Modal.getInstance(document.getElementById('addModuleModal')).hide();
        } catch (error) {
            this.showAlert('danger', this.i18n.err_network + ': ' + error.message);
        }
    }

    async removeModule(moduleId) {
        if (!confirm(this.i18n.confirm_remove)) return;

        try {
            const response = await fetch(`/api/wizard/module/${moduleId}`, {
                method: 'DELETE'
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || this.i18n.err_remove_module);
                return;
            }

            // Update state
            this.state.modules = this.state.modules.filter(m => m.id !== moduleId);
            this.state.ioObjects = this.state.ioObjects.filter(io => io.moduleId !== moduleId);

            // Update UI
            this.renderModules();
            this.showAlert('success', this.i18n.module_removed);
        } catch (error) {
            this.showAlert('danger', this.i18n.err_network + ': ' + error.message);
        }
    }

    renderModules() {
        const container = document.getElementById('modulesList');
        const noModulesMsg = document.getElementById('noModulesMsg');

        if (!container) return;

        if (this.state.modules.length === 0) {
            container.classList.add('d-none');
            noModulesMsg?.classList.remove('d-none');
            return;
        }

        container.classList.remove('d-none');
        noModulesMsg?.classList.add('d-none');

        container.innerHTML = this.state.modules.map(module => {
            const moduleIOs = this.state.ioObjects.filter(io => io.moduleId === module.id);
            return this.renderModuleCard(module, moduleIOs);
        }).join('');

        // Bind remove buttons
        container.querySelectorAll('.remove-module-btn').forEach(btn => {
            btn.addEventListener('click', (e) => {
                this.removeModule(e.currentTarget.dataset.moduleId);
            });
        });
    }

    getModuleType(typeId) {
        return this.state.moduleTypes.find(mt => mt.id === typeId) || null;
    }

    renderModuleCard(module, ioObjects) {
        const moduleType = this.getModuleType(module.typeId);
        const category = moduleType ? moduleType.category : '';
        const icon = moduleType ? moduleType.icon : 'bi-puzzle';

        const ioCounts = {};
        ioObjects.forEach(io => {
            ioCounts[io.typeId] = (ioCounts[io.typeId] || 0) + 1;
        });
        const ioSummary = Object.entries(ioCounts)
            .map(([type, count]) => `${count}x ${type.toUpperCase()}`)
            .join(' | ');

        const ioHtml = ioObjects.map(io => `
            <span class="io-chip ${io.typeId}">${io.id}</span>
        `).join('');

        return `
            <div class="module-card ${category}">
                <div class="module-card-header">
                    <i class="bi ${icon} module-icon"></i>
                    <div class="module-info">
                        <div class="module-name">${module.name}</div>
                        <div class="module-type">${module.typeId.toUpperCase()}</div>
                    </div>
                    <span class="badge bg-secondary io-count-badge">${ioObjects.length} I/O</span>
                    <div class="module-serial">${module.serial}</div>
                    <div class="module-actions">
                        <button class="btn btn-sm btn-outline-danger remove-module-btn" data-module-id="${module.id}">
                            <i class="bi bi-trash"></i>
                        </button>
                    </div>
                </div>
                <div class="module-card-body">
                    <div class="io-summary">${ioSummary}</div>
                    <div class="io-chips-container">
                        ${ioHtml}
                    </div>
                </div>
            </div>
        `;
    }

    updateSummary() {
        document.getElementById('summaryDeviceType').textContent = this.state.deviceTypeName;
        document.getElementById('summaryCategory').textContent = this.state.category || '-';
        document.getElementById('summaryModules').textContent = this.state.modules.length;
        document.getElementById('summaryIO').textContent = this.state.ioObjects.length;

        const cat = (this.state.category || '').toUpperCase();
        const hasGrenton = this.stepSequence.includes('grenton');
        const hasModules = this.stepSequence.includes('modules');

        // Hide irrelevant summary rows
        const modulesRow = document.getElementById('summaryModulesRow');
        const ioRow = document.getElementById('summaryIORow');
        const pinRow = document.getElementById('summaryPINRow');
        const broadcastRow = document.getElementById('summaryBroadcastRow');

        if (modulesRow) modulesRow.style.display = hasModules ? '' : 'none';
        if (ioRow) ioRow.style.display = hasModules ? '' : 'none';
        if (pinRow) pinRow.style.display = hasGrenton ? '' : 'none';
        if (broadcastRow) broadcastRow.style.display = hasGrenton ? '' : 'none';

        const pinEl = document.getElementById('summaryPIN');
        if (pinEl) pinEl.textContent = this.state.pin || '00000000';

        const broadcastEl = document.getElementById('summaryBroadcast');
        if (broadcastEl) broadcastEl.textContent = this.state.broadcastEnabled ? this.i18n.enabled : this.i18n.disabled;

        const authEl = document.getElementById('summaryAuth');
        if (authEl) {
            authEl.textContent = this.state.authUsername
                ? this.i18n.enabled_user.replace('{user}', this.state.authUsername)
                : this.i18n.disabled;
        }

        // Hide om.lua preview and OM note for non-CLU devices
        const omSection = document.getElementById('omPreviewSection');
        const finishNote = document.getElementById('finishNoteAlert');
        const showOM = cat === 'CLU';
        if (omSection) omSection.style.display = showOM ? '' : 'none';
        if (finishNote) finishNote.style.display = hasGrenton ? '' : 'none';
    }

    async loadOMPreview() {
        const previewEl = document.getElementById('omPreview');
        if (!previewEl) return;

        previewEl.innerHTML = `<code>${this.i18n.loading}</code>`;

        try {
            const response = await fetch('/api/wizard/om-preview');
            const data = await response.json();

            if (!response.ok) {
                previewEl.innerHTML = `<code class="text-danger">${data.error || this.i18n.err_load_preview}</code>`;
                return;
            }

            previewEl.innerHTML = `<code>${VCLU.esc(data.content)}</code>`;
        } catch (error) {
            previewEl.innerHTML = `<code class="text-danger">${this.i18n.err_network}: ${error.message}</code>`;
        }
    }

    async finish() {
        const finishBtn = document.getElementById('finishBtn');
        finishBtn.disabled = true;
        finishBtn.innerHTML = `<span class="spinner-border spinner-border-sm"></span> ${this.i18n.saving}`;

        try {
            const response = await fetch('/api/wizard/finish', {
                method: 'POST'
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || this.i18n.err_save_config);
                finishBtn.disabled = false;
                finishBtn.innerHTML = `<i class="bi bi-check-circle"></i> ${this.i18n.finish_btn}`;
                return;
            }

            // Trigger restart
            fetch('/api/restart', { method: 'POST' }).catch(() => {});

            // Show restart overlay — polls /api/health until CLU is back
            RestartOverlay.show({
                title: this.i18n.restarting || 'Restarting CLU...',
                subtitle: this.i18n.please_wait || 'Please wait while the configuration is applied',
                redirectUrl: '/stats'
            });
        } catch (error) {
            this.showAlert('danger', this.i18n.err_network + ': ' + error.message);
            finishBtn.disabled = false;
            finishBtn.innerHTML = `<i class="bi bi-check-circle"></i> ${this.i18n.finish_btn}`;
        }
    }

    updateUI() {
        const moduleCount = document.getElementById('moduleCount');
        const ioCount = document.getElementById('ioCount');
        if (moduleCount) moduleCount.textContent = this.state.modules.length;
        if (ioCount) ioCount.textContent = this.state.ioObjects.length;
    }

    showAlert(type, message) {
        const container = document.getElementById('alertContainer');
        if (!container) return;

        const alertId = 'alert-' + Date.now();
        const alertHtml = `
            <div class="alert alert-${type} alert-dismissible fade show" id="${alertId}">
                <i class="bi bi-${type === 'success' ? 'check-circle' : 'exclamation-triangle'}"></i>
                ${message}
                <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
            </div>
        `;
        container.insertAdjacentHTML('beforeend', alertHtml);

        // Auto-dismiss after 5 seconds
        setTimeout(() => {
            const alert = document.getElementById(alertId);
            if (alert) {
                bootstrap.Alert.getOrCreateInstance(alert).close();
            }
        }, 5000);
    }

}

// Initialize wizard when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
    if (typeof WIZARD_STATE !== 'undefined') {
        const i18n = typeof WIZARD_I18N !== 'undefined' ? WIZARD_I18N : {};
        window.wizard = new DeviceWizard(WIZARD_STATE, i18n);
    }
});
