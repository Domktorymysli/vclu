// Stats page JavaScript
(function() {
    'use strict';

    // Chart instances
    let memoryChart = null;
    let cmdRateChart = null;
    let httpRateChart = null;
    let busyPercentChart = null;
    let loopLagChart = null;

    // Chart.js default configuration for dark theme
    const chartDefaults = {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
            legend: {
                display: false
            }
        },
        scales: {
            x: {
                grid: {
                    color: 'rgba(255, 255, 255, 0.1)'
                },
                ticks: {
                    color: 'rgba(255, 255, 255, 0.7)',
                    maxTicksLimit: 12
                }
            },
            y: {
                grid: {
                    color: 'rgba(255, 255, 255, 0.1)'
                },
                ticks: {
                    color: 'rgba(255, 255, 255, 0.7)'
                },
                beginAtZero: true
            }
        }
    };

    // Format number with K/M suffix
    function formatNumber(n) {
        if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
        if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
        return n.toString();
    }

    // Update stat element
    function updateStat(id, value) {
        const el = document.getElementById(id);
        if (el) {
            el.textContent = value;
        }
    }

    // Update plugin details table
    function updatePluginDetails(details) {
        const card = document.getElementById('pluginDetailsCard');
        const tbody = document.getElementById('pluginDetailsBody');

        if (!card || !tbody) return;

        if (!details || details.length === 0) {
            card.style.display = 'none';
            return;
        }

        card.style.display = 'block';

        // Sort by name
        details.sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));

        tbody.innerHTML = details.map(p => `
            <tr>
                <td>
                    <span class="fw-medium">${VCLU.esc(p.name || p.id)}</span>
                    <small class="text-muted d-block">${VCLU.esc(p.id)}</small>
                </td>
                <td><small class="text-muted">${VCLU.esc(p.version || '-')}</small></td>
                <td class="text-center">${p.timers || 0}</td>
                <td class="text-center">${p.sensors || 0}</td>
                <td class="text-center">${p.controls || 0}</td>
                <td class="text-center">${p.subscriptions || 0}</td>
                <td class="text-center">${p.exposed || 0}</td>
            </tr>
        `).join('');
    }

    // Format timestamp for chart labels
    function formatTime(ts) {
        const d = new Date(ts);
        return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }

    // Create or update a chart
    function createChart(canvasId, data, label, color, unit) {
        const ctx = document.getElementById(canvasId);
        if (!ctx) return null;

        const labels = data.map(d => formatTime(d.t));
        const values = data.map(d => d.v);

        const config = {
            type: 'line',
            data: {
                labels: labels,
                datasets: [{
                    label: label,
                    data: values,
                    borderColor: color,
                    backgroundColor: color.replace('1)', '0.1)'),
                    fill: true,
                    tension: 0.3,
                    pointRadius: 0,
                    borderWidth: 2
                }]
            },
            options: {
                ...chartDefaults,
                plugins: {
                    ...chartDefaults.plugins,
                    tooltip: {
                        mode: 'index',
                        intersect: false,
                        callbacks: {
                            label: function(context) {
                                return context.dataset.label + ': ' + context.parsed.y.toFixed(1) + ' ' + (unit || '');
                            }
                        }
                    }
                },
                scales: {
                    ...chartDefaults.scales,
                    y: {
                        ...chartDefaults.scales.y,
                        title: {
                            display: true,
                            text: unit || '',
                            color: 'rgba(255, 255, 255, 0.7)',
                            font: { size: 11 }
                        },
                        ticks: {
                            ...chartDefaults.scales.y.ticks,
                            callback: function(value) {
                                return value + (unit ? ' ' + unit : '');
                            }
                        }
                    }
                }
            }
        };

        return new Chart(ctx, config);
    }

    // Update chart data
    function updateChart(chart, data) {
        if (!chart || !data) return;

        const labels = data.map(d => formatTime(d.t));
        const values = data.map(d => d.v);

        chart.data.labels = labels;
        chart.data.datasets[0].data = values;
        chart.update('none');
    }

    // Fetch and update stats
    async function fetchStats() {
        try {
            const response = await fetch('/api/stats');
            if (!response.ok) throw new Error('Failed to fetch stats');
            const stats = await response.json();

            // System
            updateStat('uptime', stats.uptime_str || '--');
            updateStat('memory', stats.system?.memory_mb?.toFixed(1) || '--');
            updateStat('goroutines', stats.system?.goroutines || '--');
            updateStat('gcRuns', stats.system?.gc_runs || '--');

            // Lua
            updateStat('luaExec', formatNumber(stats.lua?.executions || 0));
            updateStat('luaErrors', stats.lua?.errors || 0);
            updateStat('timersActive', stats.lua?.timers_active || 0);
            updateStat('timersFired', formatNumber(stats.lua?.timers_fired || 0));
            updateStat('busyPercent', (stats.lua?.busy_percent || 0).toFixed(1));
            updateStat('loopLag', (stats.lua?.loop_lag_ms || 0).toFixed(0));

            // Protocol
            const cluStatus = stats.protocol?.running ? 'ON' : 'OFF';
            updateStat('cluStatus', cluStatus);
            document.getElementById('cluStatus')?.classList.toggle('text-success', stats.protocol?.running);
            document.getElementById('cluStatus')?.classList.toggle('text-danger', !stats.protocol?.running);
            updateStat('cmdReceived', formatNumber(stats.protocol?.commands_received || 0));
            updateStat('cmdExecuted', formatNumber(stats.protocol?.commands_executed || 0));
            updateStat('cmdErrors', stats.protocol?.errors || 0);

            // HTTP
            updateStat('httpRequests', formatNumber(stats.http?.requests || 0));
            updateStat('httpAPI', formatNumber(stats.http?.api_requests || 0));
            updateStat('httpWeb', formatNumber(stats.http?.web_requests || 0));
            const httpErrs = (stats.http?.errors_4xx || 0) + (stats.http?.errors_5xx || 0);
            updateStat('httpErrors', httpErrs);

            // Plugins
            updateStat('pluginsActive', stats.plugins?.active || 0);
            updateStat('pluginTimers', stats.plugins?.timers || 0);
            updateStat('pluginHTTP', formatNumber(stats.plugins?.http_requests || 0));
            updateStat('luaMemory', stats.plugins?.lua_memory_kb?.toFixed(1) || '--');

            // Plugin details table
            updatePluginDetails(stats.plugins?.details || []);

            // MQTT — client counters from AppMetrics
            updateStat('mqttPublished', formatNumber(stats.mqtt?.published || 0));
            updateStat('mqttReceived', formatNumber(stats.mqtt?.received || 0));

            // HomeKit
            const hkRunning = stats.homekit?.running ? 'ON' : 'OFF';
            updateStat('hkStatus', hkRunning);
            document.getElementById('hkStatus')?.classList.toggle('text-success', stats.homekit?.running);
            document.getElementById('hkStatus')?.classList.toggle('text-danger', !stats.homekit?.running);
            updateStat('hkAccessories', stats.homekit?.accessories || 0);
            updateStat('hkRequests', formatNumber(stats.homekit?.requests || 0));
            updateStat('hkEvents', formatNumber(stats.homekit?.events || 0));

        } catch (err) {
            console.error('Failed to fetch stats:', err);
        }
    }

    // Fetch MQTT broker + client stats
    async function fetchMQTTStats() {
        try {
            const [brokerRes, clientRes] = await Promise.all([
                fetch('/api/mqtt/broker/stats'),
                fetch('/api/mqtt/clients/stats')
            ]);

            // Broker
            if (brokerRes.ok) {
                const b = await brokerRes.json();
                const running = !!b.running;
                updateStat('mqttBrokerStatus', running ? 'ON' : 'OFF');
                const el = document.getElementById('mqttBrokerStatus');
                if (el) { el.classList.toggle('text-success', running); el.classList.toggle('text-danger', !running); }
                updateStat('mqttBrokerClients', b.clientsConnected || 0);
                updateStat('mqttBrokerMsgs', formatNumber((b.messagesReceived || 0) + (b.messagesSent || 0)));
                updateStat('mqttBrokerRetained', b.retainedMessages || 0);
            }

            // Client
            if (clientRes.ok) {
                const cl = await clientRes.json();
                const connected = cl.connectedClients > 0;
                updateStat('mqttClientStatus', connected ? 'ON' : 'OFF');
                const el = document.getElementById('mqttClientStatus');
                if (el) { el.classList.toggle('text-success', connected); el.classList.toggle('text-danger', !connected); }

                // Subscriptions & info from first client
                let subs = 0;
                if (cl.clients && cl.clients.length > 0) {
                    subs = (cl.clients[0].subscriptions || []).length;
                }
                updateStat('mqttSubs', subs);
            }
        } catch (err) {
            console.error('Failed to fetch MQTT stats:', err);
        }
    }

    // Fetch and update time series charts
    async function fetchTimeSeries() {
        try {
            const response = await fetch('/api/stats/timeseries');
            if (!response.ok) throw new Error('Failed to fetch time series');
            const data = await response.json();

            // Memory chart
            if (data.memory_used && data.memory_used.length > 0) {
                if (!memoryChart) {
                    memoryChart = createChart('memoryChart', data.memory_used, 'Memory', 'rgba(54, 162, 235, 1)', 'MB');
                } else {
                    updateChart(memoryChart, data.memory_used);
                }
            }

            // Command rate chart
            if (data.cmd_rate && data.cmd_rate.length > 0) {
                if (!cmdRateChart) {
                    cmdRateChart = createChart('cmdRateChart', data.cmd_rate, 'Commands', 'rgba(255, 193, 7, 1)', 'cmd/min');
                } else {
                    updateChart(cmdRateChart, data.cmd_rate);
                }
            }

            // HTTP rate chart
            if (data.http_rate && data.http_rate.length > 0) {
                if (!httpRateChart) {
                    httpRateChart = createChart('httpRateChart', data.http_rate, 'Requests', 'rgba(40, 167, 69, 1)', 'req/min');
                } else {
                    updateChart(httpRateChart, data.http_rate);
                }
            }

            // Lua Busy % chart
            if (data.lua_busy_percent && data.lua_busy_percent.length > 0) {
                if (!busyPercentChart) {
                    busyPercentChart = createChart('busyPercentChart', data.lua_busy_percent, 'Busy', 'rgba(220, 53, 69, 1)', '%');
                } else {
                    updateChart(busyPercentChart, data.lua_busy_percent);
                }
            }

            // Loop Lag chart
            if (data.lua_loop_lag && data.lua_loop_lag.length > 0) {
                if (!loopLagChart) {
                    loopLagChart = createChart('loopLagChart', data.lua_loop_lag, 'Lag', 'rgba(13, 202, 240, 1)', 'ms');
                } else {
                    updateChart(loopLagChart, data.lua_loop_lag);
                }
            }

        } catch (err) {
            console.error('Failed to fetch time series:', err);
        }
    }

    // Download diagnostic ZIP
    async function downloadDiagnostic() {
        const btn = document.getElementById('diagnosticDownloadBtn');
        if (btn) {
            btn.disabled = true;
            btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span> Generating...';
        }

        const opts = {
            include_modules: document.getElementById('diagModules')?.checked || false,
            include_om_user: document.getElementById('diagOmUser')?.checked || false,
            include_proxy: document.getElementById('diagProxy')?.checked || false,
            include_device_names: document.getElementById('diagDeviceNames')?.checked || false,
            include_config: document.getElementById('diagConfig')?.checked || false,
        };

        try {
            const response = await fetch('/api/diagnostic', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(opts),
            });

            if (!response.ok) {
                const err = await response.json().catch(() => ({ error: 'Download failed' }));
                throw new Error(err.error || 'Download failed');
            }

            const blob = await response.blob();
            const disposition = response.headers.get('Content-Disposition') || '';
            const match = disposition.match(/filename=(.+)/);
            const filename = match ? match[1] : 'vclu-diagnostic.zip';

            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = filename;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);

            // Close modal
            const modal = bootstrap.Modal.getInstance(document.getElementById('diagnosticModal'));
            if (modal) modal.hide();
        } catch (err) {
            console.error('Diagnostic download failed:', err);
            VCLU.showToast('Failed to download diagnostic: ' + err.message, 'danger');
        } finally {
            if (btn) {
                btn.disabled = false;
                btn.innerHTML = '<i class="bi bi-download"></i> Download';
            }
        }
    }

    // Initialize
    function init() {
        // Initial fetch
        fetchStats();
        fetchMQTTStats();
        fetchTimeSeries();

        // Refresh button
        const refreshBtn = document.getElementById('refreshBtn');
        if (refreshBtn) {
            refreshBtn.addEventListener('click', () => {
                fetchStats();
                fetchMQTTStats();
                fetchTimeSeries();
            });
        }

        // Diagnostic button — open modal
        const diagnosticBtn = document.getElementById('diagnosticBtn');
        if (diagnosticBtn) {
            diagnosticBtn.addEventListener('click', () => {
                const modal = new bootstrap.Modal(document.getElementById('diagnosticModal'));
                modal.show();
            });
        }

        // Diagnostic download button
        const downloadBtn = document.getElementById('diagnosticDownloadBtn');
        if (downloadBtn) {
            downloadBtn.addEventListener('click', downloadDiagnostic);
        }

        // Auto-refresh every 30 seconds
        setInterval(fetchStats, 30000);
        setInterval(fetchMQTTStats, 30000);
        // Update charts every minute
        setInterval(fetchTimeSeries, 60000);
    }

    // Run when DOM is ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
