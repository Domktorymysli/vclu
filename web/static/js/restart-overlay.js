/**
 * RestartOverlay — generic fullscreen overlay for CLU restart/reboot scenarios.
 *
 * Usage:
 *   RestartOverlay.show({
 *       title: 'Restarting CLU...',
 *       subtitle: 'Please wait',
 *       healthUrl: '/api/health',
 *       redirectUrl: '/stats',
 *       pollInterval: 1500,
 *       maxWait: 60000
 *   });
 */
const RestartOverlay = (() => {
    let overlayEl = null;
    let timerInterval = null;

    function show(opts = {}) {
        const title = opts.title || 'Restarting CLU...';
        const subtitle = opts.subtitle || 'Please wait';
        const healthUrl = opts.healthUrl || '/api/health';
        const redirectUrl = opts.redirectUrl || '/stats';
        const pollInterval = opts.pollInterval || 1500;
        const maxWait = opts.maxWait || 60000;
        const initialDelay = opts.initialDelay || 2000;

        // Remove previous overlay if any
        if (overlayEl) overlayEl.remove();

        overlayEl = document.createElement('div');
        overlayEl.id = 'restart-overlay';
        overlayEl.innerHTML = `
            <div style="text-align:center;color:#fff;">
                <div class="ro-spinner"></div>
                <h2 style="margin:24px 0 8px;font-size:1.5rem;" id="ro-title">${VCLU.esc(title)}</h2>
                <p style="margin:0 0 16px;opacity:.7;font-size:.95rem;" id="ro-subtitle">${VCLU.esc(subtitle)}</p>
                <p style="margin:0;font-size:.85rem;opacity:.5;" id="ro-timer">0s</p>
            </div>
        `;

        // Inline styles — no dependency on Bootstrap or external CSS
        Object.assign(overlayEl.style, {
            position: 'fixed',
            inset: '0',
            zIndex: '99999',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: 'rgba(0,0,0,.85)',
            opacity: '0',
            transition: 'opacity .3s ease'
        });

        // Spinner CSS (keyframes injected once)
        if (!document.getElementById('ro-style')) {
            const style = document.createElement('style');
            style.id = 'ro-style';
            style.textContent = `
                .ro-spinner {
                    width:48px;height:48px;margin:0 auto;
                    border:4px solid rgba(255,255,255,.15);
                    border-top-color:#27C2A8;
                    border-radius:50%;
                    animation:ro-spin 1s linear infinite;
                }
                @keyframes ro-spin { to { transform:rotate(360deg); } }
            `;
            document.head.appendChild(style);
        }

        document.body.appendChild(overlayEl);
        // Trigger fade-in
        requestAnimationFrame(() => { overlayEl.style.opacity = '1'; });

        // Timer display
        const startTime = Date.now();
        const timerEl = overlayEl.querySelector('#ro-timer');
        timerInterval = setInterval(() => {
            const elapsed = Math.floor((Date.now() - startTime) / 1000);
            if (timerEl) timerEl.textContent = elapsed + 's';
        }, 1000);

        // Wait for server to shut down, then start polling
        setTimeout(() => {
            poll(healthUrl, redirectUrl, pollInterval, maxWait, startTime);
        }, initialDelay);
    }

    function poll(healthUrl, redirectUrl, interval, maxWait, startTime) {
        if (Date.now() - startTime > maxWait) {
            timeout();
            return;
        }

        fetch(healthUrl, { cache: 'no-store' })
            .then(resp => {
                if (resp.ok) {
                    clearInterval(timerInterval);
                    window.location.href = redirectUrl;
                } else {
                    scheduleNext();
                }
            })
            .catch(() => {
                scheduleNext();
            });

        function scheduleNext() {
            setTimeout(() => poll(healthUrl, redirectUrl, interval, maxWait, startTime), interval);
        }
    }

    function timeout() {
        clearInterval(timerInterval);
        const titleEl = overlayEl && overlayEl.querySelector('#ro-title');
        const subtitleEl = overlayEl && overlayEl.querySelector('#ro-subtitle');
        if (titleEl) titleEl.textContent = 'CLU did not respond';
        if (subtitleEl) subtitleEl.textContent = 'Please refresh the page manually.';
    }

    return { show };
})();
