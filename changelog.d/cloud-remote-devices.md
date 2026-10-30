### Lua runtime

- **Devices in the vclu cloud app: `:remote()`, `:roles()` and `:pin()` on an exposed handle** -
  `expose(lamp, "light", { id = "salon" }):remote()` lets every member of the home switch it from
  the app; `:remote({ confirm = true })` asks the person to confirm, `:pin(1234):remote({ pin = true })`
  asks for a PIN that never leaves vCLU (5 wrong in a row lock the device for 30 s),
  `:remote({ readonly = true })` shows it without control, and `:roles("domownicy")` narrows it to
  people given that role in the cloud. Commands call the device's own method (`switchOn`, `open`,
  `close`, `stop`, a scene's `execute`), never a raw write, and run as the person (origin `remote`
  in history). A device whose path comes from its name gets a warning, since renaming it would
  make it another device in the cloud: give it an `id`. **Contract change:** new public methods
  `ExposedHandle:remote`, `:roles` and `:pin`; the options `remote`, `roles` and `pin` of
  `expose()` and `handle:update()` are now refused with an error naming the method to use (before
  they were stored and meant nothing): a script that set them drops or renames those keys.

### Cloud

- **Remote devices** - vCLU sends the cloud the list of devices exposed with `:remote()` (the
  `snapshot` message, after a change, debounced, and once after a Lua reload) and answers its
  `cmd` messages: at most once per id, 10 a second and 8 at a time per connection, and not after
  the command's deadline. A repeated `notify_action` that arrives while the first one still runs
  now waits for its answer instead of pressing the button twice.
