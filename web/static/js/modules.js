class ModulesManager {
    constructor(initialState) {
        this.state = {
            modules: initialState.modules || [],
            ioObjects: initialState.ioObjects || [],
            deviceTypeId: initialState.deviceTypeId || '',
            moduleTypes: initialState.moduleTypes || []
        };

        this.init();
    }

    init() {
        this.bindEvents();
        this.renderModules();
    }

    bindEvents() {
        // Add module buttons
        document.getElementById('addModuleBtn')?.addEventListener('click', () => this.showAddModuleModal());
        document.getElementById('addFirstModuleBtn')?.addEventListener('click', () => this.showAddModuleModal());
        document.getElementById('confirmAddModule')?.addEventListener('click', () => this.addModule());
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
                this.showAlert('danger', data.error || 'Failed to add module');
                return;
            }

            // Update state
            this.state.modules.push(data.module);
            this.state.ioObjects = this.state.ioObjects.concat(data.ioObjects);

            // Update UI
            this.renderModules();
            this.updateCounts();
            this.showAlert('success', `Module "${data.module.name}" added successfully`);

            // Close modal
            bootstrap.Modal.getInstance(document.getElementById('addModuleModal')).hide();
        } catch (error) {
            this.showAlert('danger', 'Network error: ' + error.message);
        }
    }

    async removeModule(moduleId) {
        if (!confirm('Are you sure you want to remove this module?')) return;

        try {
            const response = await fetch(`/api/wizard/module/${moduleId}`, {
                method: 'DELETE'
            });

            const data = await response.json();

            if (!response.ok) {
                this.showAlert('danger', data.error || 'Failed to remove module');
                return;
            }

            // Update state
            this.state.modules = this.state.modules.filter(m => m.id !== moduleId);
            this.state.ioObjects = this.state.ioObjects.filter(io => io.moduleId !== moduleId);

            // Update UI
            this.renderModules();
            this.updateCounts();
            this.showAlert('success', 'Module removed successfully');
        } catch (error) {
            this.showAlert('danger', 'Network error: ' + error.message);
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

        // Build I/O summary (e.g., "8x DOUT | 8x DIN | 1x Analog")
        const ioCounts = {};
        ioObjects.forEach(io => {
            ioCounts[io.typeId] = (ioCounts[io.typeId] || 0) + 1;
        });
        const ioSummary = Object.entries(ioCounts)
            .map(([type, count]) => `${count}x ${type.toUpperCase()}`)
            .join(' | ');

        // Build I/O chips
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

    updateCounts() {
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

// Initialize manager when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
    if (typeof MODULES_STATE !== 'undefined') {
        window.modulesManager = new ModulesManager(MODULES_STATE);
    }
});
