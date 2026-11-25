### Plugins

- **A plugin can be a small app: its own dashboard and named actions** - `plugin:dashboard(id)` gives the builder of `Dashboard.new` for a dashboard at `/d/<shortId>_<id>` (`-` in the short id as `_`), behind login unless the plugin sets another auth. Its widget callbacks run as the plugin's code, errors go to the plugin's log; widgets that reach the house by name (`area`, `floor`, `room`, `covers`, `calendar`) are not available, and an id a module or another plugin already uses is refused. `plugin:action(name, {description, params}, fn)` registers an action agents and voice assistants call: params are typed (`string`, `number`, `boolean`), unknown ones refused, text up to 1000 characters; at most 32 per plugin. Both go when the plugin is disabled, reloaded or uninstalled (uninstalling now also stops a running plugin). The dashboards page marks a plugin's dashboard. No permission needed.

### HTTP API

- **Plugin actions** - `GET /api/plugins/actions` (`?plugin=` for one) lists `{plugin, name, description, params}`; `POST /api/plugins/:id/actions/:name` with the params as a JSON body answers `{result}`, or `{error}` with 404 (no such action), 400 (params) or 422 (the action failed). Same auth as the other plugin routes; the action runs as the caller.

### MCP

- **`vclu_app_actions` and `vclu_app_action`** - list and run the actions of installed apps (control layer, like `vclu_device_set`): a bot sees a plugin's actions when its profiles read the plugin id and runs them when they control it. `vclu_runtime_schema` lists each running plugin's actions and the `apps` API.
