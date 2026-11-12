### Web

- **The Access page shares devices with vclu cloud** - its Cloud column is a choice like MQTT/HA
  and HomeKit: Hidden by default, Readonly (every member of the home sees it) or Full (every member
  controls it), one by one or for the selected rows. A device Lua names with `:remote()` (roles,
  PIN, confirmation) keeps Lua's setting, shown read-only with a "Lua" mark; `:remote(false)` now
  keeps a device out of the cloud whatever the page says (before it only undid `:remote()`). A
  registry object hidden from MQTT and HomeKit is exposed as soon as the cloud is opened for it,
  not at the next reload. **Contract change:** the access file and `/api/access` know the
  integration `cloud`; an access file without it leaves the cloud closed; `/api/access` lists
  `access.cloud` and, for devices Lua decides about, `cloudLua` (`false` or `{access, roles}`)
  instead of `cloud`.
