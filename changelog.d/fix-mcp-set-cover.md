### MCP

- **`vclu_device_set` moves a cover to its position** - a value set on an exposed cover goes to its `setPosition` (a Grenton roller's SetPosition, execute 10) instead of feature 0; a roller without a position opens or closes, and a value that is not a number is refused. A light's value goes through its own `setValue`, so a Grenton LED RGB light is set by SetValue rather than its read-only feature 0. Home Assistant and HomeKit position commands use the same path.
