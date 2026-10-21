### Lua runtime

- **`_:value(path)` reads an object's value** - the value by path or name, or `nil` when the object does not exist, has no reading yet or has no `getValue`, instead of the `attempt to index a nil value` that `_:get(path):getValue()` gives for a stale path. It works in modules and in plugins; `vclu_draft_test` warns about a missing object as it does for `_:get` ([#82](https://github.com/Domktorymysli/vclu/issues/82)).
- **`_:get` in a plugin finds an object named like a registry method** - for a name such as `list` or `info` it returned the registry's own function wrapped as an object.
- **Registry methods in a plugin fail with a message, not a stack overflow** - in a plugin every key of `_` other than `get` recursed until the stack overflowed (`_:byTag`, `_:find`, even `_.x`). `_:sync`, `_:byTag`, `_:find` and the other methods now raise `_:byTag is not available in a plugin; use _:get or _:value`, and an unknown key is `nil`. Plugins still cannot change state through `_`.

### Web

- **Lua editor completions load again** - `lua-completions.json` lacked a comma since v1.2.0, so the editor had no completions, hover help or go-to-definition, and the registry browser listed no methods. A test now parses the file.
