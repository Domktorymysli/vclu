### Web

- **Dashboard values pushed over a WebSocket** - an open dashboard shows a change as it happens instead of on the next poll. `GET /api/dashboard/<id>/ws` (the auth of `/values`) sends what `/values` answers, then only the widgets that changed; vCLU reads each watched dashboard once for all its pages (after a state change, an action, and every `poll` seconds), so more open pages no longer mean more Lua work. Pages fall back to polling where the socket does not open (a proxy without the upgrade), and pick up a Lua reload at once. A page notices a dead connection (a phone that changed networks) in about 10 s and comes back as soon as the browser says the network is back. The socket checks the `Origin`, checks access again every 15 s (a changed PIN closes it), and takes at most 64 pages, 8 from one address. Dashboard pages link their files with the vCLU version (`?v=`), so a browser does not mix cached files with new ones after an update.

### Lua runtime

- **`section:latency(id, label, icon)`** - a tile (or a chip in the header) with the page's round trip to vCLU, measured by the page: a ping over the WebSocket, averaged so one slow answer does not jump the number, or the time of a poll while it polls; red over 300 ms, "offline" with no connection.
