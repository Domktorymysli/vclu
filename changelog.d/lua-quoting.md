### Fixes

- **Quotes in names and passwords reach Lua unchanged** - the embedded MQTT broker's username and password, a plugin's configuration, ID and folder, access profile names, the Sync list and the runtime's paths are written into the scripts Go builds as Lua string literals; a `"` or `]]` in one of them broke the script (an MQTT broker password with a quote stopped the internal client from connecting).
