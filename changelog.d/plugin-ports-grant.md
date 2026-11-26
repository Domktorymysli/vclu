### Plugins

- **New ports wait for your permission** - installing a plugin with `udp` or `tcp` records the ports its `plugin.json` lists; an update that adds a port shows it on the plugins page as waiting, and the plugin keeps the ports allowed before until you allow the new one. A plugin installed earlier gets its current ports recorded on the next load.
