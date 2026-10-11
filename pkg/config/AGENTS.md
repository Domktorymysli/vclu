# pkg/config: configuration files

## Change `.vclu.json` through `UpdateMasterConfig`
Several handlers read, change and write the same file; without the lock the last write wins and a
setting saved elsewhere disappears. Use `UpdateMasterConfig(dir, func(c *VCLUConfig) error { ... })`
in new code. Most existing callers (about 40) still use `WriteMasterConfig`; do not add more, and
move one over when you touch it. Never write the JSON file directly.

## A missing file is not an error
`ReadMasterConfig` returns `nil, nil` when the file does not exist yet (first start). Check for `nil`.
