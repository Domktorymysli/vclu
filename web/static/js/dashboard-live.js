/**
 * The values of one dashboard as they change: pushed over its WebSocket, polled from /values
 * while the socket is not open (it does not open within OPEN_WAIT, or it dropped and has not
 * opened again). The socket and polling never run together.
 *
 *   var live = VCLU.dashboardLive('/api/dashboard/home', {
 *       interval: function() { return 2000; }, // the poll interval now
 *       show: function(values) {},             // the whole state: what it leaves out is gone
 *       failed: function() {},                 // the values cannot be had now
 *       definition: function() {},             // Lua reloaded: fetch the definition again
 *       latency: function(l) {}                // {ms, via: 'ws' | 'poll'}, or null: no connection
 *   });
 *   live.start(); live.stop(); live.poll(); live.values();
 *
 * Messages (pkg/web/dashboard_live.go): values first, then patch {values, removed}, definition
 * after a reload, error while vCLU cannot read them, pong for each ping the page sends.
 *
 * Pings follow lichess's socket client: one as the socket opens, the next PING_DELAY after each
 * pong. A ping without its pong in PONG_WAIT means the connection is dead (a browser does not see
 * WebSocket pings, and a mobile network drops an idle connection without a word), and the page
 * polls until it opens again. The latency shown is the round trip averaged: the first four
 * plainly, then each new one counts a tenth, so one slow answer does not jump the number. While
 * polling it is the time of a poll. Retries wait 2, 4, 8 .. 30 s plus up to half of that again,
 * so pages do not all come back in the same second after vCLU restarted; when the browser says
 * the network is back, at once.
 */
(function() {
    'use strict';

    var OPEN_WAIT = 5000;   // poll meanwhile when the socket has not opened by then
    var RETRY_MIN = 2000;   // the next try after a drop, doubling with each failed open
    var RETRY_MAX = 30000;
    var PING_DELAY = 2500; // a ping as soon as the socket opens, then this long after each pong
    var PONG_WAIT = 9000;

    function dashboardLive(base, h) {
        var ws = null;        // the socket, connecting or open
        var state = null;     // what it gave, patches applied
        var failures = 0;     // opens that failed in a row
        var retryTimer = null;
        var waitTimer = null;
        var pollTimer = null;
        var running = false;
        var pingTimer = null;
        var pongTimer = null;
        var pingId = 0;
        var pingSent = {}; // id -> time sent
        var pongs = 0;     // answered pings, for the average
        var avgLag = 0;

        function latency(l) {
            if (h.latency) h.latency(l);
        }

        function wsURL() {
            return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + base + '/ws';
        }

        function poll() {
            var t0 = Date.now();
            fetch(base + '/values').then(function(r) {
                if (r.status === 404) return { _notFound: true };
                if (!r.ok) throw new Error('HTTP ' + r.status);
                var ms = Date.now() - t0;
                return r.json().then(function(data) {
                    // a poll answered while the socket is open says nothing of the socket
                    if (running && !(ws && ws.readyState === 1)) latency({ ms: ms, via: 'poll' });
                    return data;
                });
            }).then(function(data) {
                if (running) h.show(data);
            }).catch(function() {
                if (running) {
                    h.failed();
                    if (!ws) latency(null);
                }
            });
        }

        function ping(sock) {
            pingTimer = null;
            var id = ++pingId;
            pingSent = {};
            pingSent[id] = Date.now();
            try { sock.send(JSON.stringify({ type: 'ping', id: id })); } catch (e) { return; }
            pongTimer = setTimeout(function() {
                pongTimer = null;
                if (ws === sock) lost(sock); // no answer: the connection is dead
            }, PONG_WAIT);
        }

        function pong(sock, sent) {
            pingSent = {};
            if (pongTimer) clearTimeout(pongTimer);
            pongTimer = null;
            pongs++;
            avgLag += (Date.now() - sent - avgLag) * (pongs > 4 ? 0.1 : 1 / pongs);
            latency({ ms: Math.round(avgLag), via: 'ws' });
            pingTimer = setTimeout(function() { ping(sock); }, PING_DELAY);
        }

        function stopPings() {
            if (pingTimer) clearTimeout(pingTimer);
            if (pongTimer) clearTimeout(pongTimer);
            pingTimer = pongTimer = null;
            pingSent = {};
        }

        // The socket is gone (closed, or no pong): poll and try again later
        function lost(sock) {
            if (ws !== sock) return;
            ws = null;
            state = null;
            stopPings();
            latency(null);
            if (sock.readyState !== 3) sock.close();
            if (!sock._opened) failures++;
            startPolling();
            var wait = Math.min(RETRY_MAX, RETRY_MIN * Math.pow(2, Math.max(failures - 1, 0)));
            retryTimer = setTimeout(function() {
                retryTimer = null;
                connect();
            }, wait + Math.random() * wait / 2);
        }

        // The browser says the network is back: try now rather than at the end of the wait
        function online() {
            if (retryTimer) clearTimeout(retryTimer);
            retryTimer = null;
            connect(); // nothing while a socket is open or trying, or after stop
        }

        function startPolling() {
            if (pollTimer || !running) return;
            pollTimer = setInterval(poll, h.interval());
        }

        function stopPolling() {
            if (pollTimer) {
                clearInterval(pollTimer);
                pollTimer = null;
            }
        }

        function connect() {
            if (ws || retryTimer || !running) return;
            var sock;
            try {
                sock = new WebSocket(wsURL());
            } catch (e) {
                // no WebSocket in this browser, or a URL it refuses: poll
                startPolling();
                return;
            }
            ws = sock;
            waitTimer = setTimeout(function() {
                waitTimer = null;
                if (ws === sock && !sock._opened) startPolling();
            }, OPEN_WAIT);
            sock.onopen = function() {
                if (ws !== sock) return;
                sock._opened = true;
                failures = 0;
                stopPolling();
                ping(sock);
            };
            sock.onmessage = function(ev) {
                if (ws !== sock) return;
                var m;
                try { m = JSON.parse(ev.data); } catch (e) { return; }
                if (m.type === 'values') {
                    state = m.values || {};
                    h.show(state);
                } else if (m.type === 'patch' && state) {
                    for (var k in m.values) {
                        if (m.values.hasOwnProperty(k)) state[k] = m.values[k];
                    }
                    (m.removed || []).forEach(function(r) { delete state[r]; });
                    h.show(state);
                } else if (m.type === 'definition') {
                    h.definition();
                } else if (m.type === 'error') {
                    h.failed();
                } else if (m.type === 'pong' && pingSent[m.id] != null) {
                    pong(sock, pingSent[m.id]);
                }
            };
            sock.onclose = function() {
                lost(sock); // nothing when closed by stop() or after a missing pong
            };
        }

        return {
            // Values from now on
            start: function() {
                running = true;
                if (window.addEventListener) window.addEventListener('online', online);
                connect();
            },
            // Nothing until start (the page is hidden)
            stop: function() {
                running = false;
                if (window.removeEventListener) window.removeEventListener('online', online);
                stopPolling();
                stopPings();
                [retryTimer, waitTimer].forEach(function(t) { if (t) clearTimeout(t); });
                retryTimer = waitTimer = null;
                if (ws) {
                    var sock = ws;
                    ws = null;
                    state = null;
                    sock.close();
                }
            },
            // One /values now (an action failed, the page came back)
            poll: poll,
            // The state the socket gave, null while it is not open
            values: function() { return state; }
        };
    }

    window.VCLU = window.VCLU || {};
    window.VCLU.dashboardLive = dashboardLive;
})();
