# pkg/plugins: plugin system

## A plugin is a factory, not a configured singleton
The sandbox is keyed by plugin id, so one plugin is one instance. No `config` section in the
manifest: the plugin returns an object with `create(opts)`, and each call makes an independent
instance with its own poller and sensors. Configuration is Lua written by the user or an agent.

## Sensor ids are global
Ids of sensors and controls are flat across the whole of vCLU. Derive them from the instance's
options and refuse a duplicate with an error in the log instead of replacing another instance's.

## What the sandbox gives
`pkg/lua/runtime/core/PluginSandbox.lua` builds the environment from an allow-list: no
`setmetatable`, `require`, `io` or `debug`; `os` is a safe subset. `string`, `table` and `math` are
the shared global tables, not copies: a plugin must not modify them. Timers, events, MQTT
subscriptions and registered objects go through the plugin API and are cleaned up on disable or
reload; `HTTP` is the global client passed through, so a plugin must not leave requests or retries
running after it stops.
