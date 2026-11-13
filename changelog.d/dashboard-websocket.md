### Web

- **Values pushed over a WebSocket** - an open dashboard shows a change as it happens instead of on
  the next poll. `GET /api/dashboard/<id>/ws` sends what `/values` answers, then only what
  changed; vCLU reads each watched dashboard once for all its pages (after a state change, an
  action, and every `poll` seconds), so more open pages no longer mean more Lua work. Pages fall
  back to polling where the socket does not open. Dashboards also pick up a Lua reload at once.
  A page notices a dead connection (a phone that changed networks) in about 10 s and comes back
  as soon as the browser says the network is back.

### Lua runtime

- **`section:latency(id, label, icon)`** - a dashboard tile (a chip in the header) with the page's
  round trip to vCLU, measured by the page: a ping over the WebSocket 2.5 s after each answer,
  averaged so one slow answer does not jump the number, or the time of a poll while it polls; red
  over 300 ms, "offline" with no connection. A ping left without an answer for 9 s also tells the
  page its connection is dead, and it polls until the socket is back.
