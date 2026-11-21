### Panel

- **Notifications in the top bar** - a bell lists what needs attention: a new vclu version, plugin
  updates (one entry for all of them), modules that do not load, failed logins to the panel and
  locks, also of a device PIN after wrong tries from the app. Read marks are kept on vclu, so every browser shows the same; a state (an update, a
  module) goes away on its own once it is over. Sources can be turned off and single notifications muted
  in the settings (`notifications` in .vclu.json); errors from the log are off by default
  ([#248](https://github.com/Domktorymysli/vclu/issues/248)).
- **Login attempt limit for the panel** - after 5 failed logins from an address, logins from it
  wait 30 s, then twice as long each time up to 15 minutes; a successful login clears it.
- **Collapsible start page** - each section of the start page folds away (remembered in the
  browser) and has a shortcut to the settings of that part.
- The new-version check also reads the enabled plugin repositories to find plugin updates, unless
  plugin updates are turned off in the notification settings.

### Lua runtime

- **`Notifications.on(fn)`** - a module gets the bell's notifications, to pass them on (e.g.
  `Cloud.notify`, Telegram); returns a function that stops it. `msg` and `params` are the stable
  fields to match on.

### HTTP API

- `GET /api/notifications`, `POST /api/notifications/read`, `/clear`, `/mute`,
  `DELETE /api/notifications/mute`, `GET`/`PUT /api/notifications/settings`.
