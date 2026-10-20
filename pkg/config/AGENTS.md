# pkg/config: configuration files

## Change `.vclu.json` through `UpdateMasterConfig`
Several handlers read, change and write the same file; without the lock the last write wins and a
setting saved elsewhere disappears. Use `UpdateMasterConfig(dir, func(c *VCLUConfig) error { ... })`;
`writeMasterConfig` is unexported and runs only under `masterConfigMu`. Never write the JSON file
directly.

- fn runs under the lock: no network calls inside it (fetch first, as `handlePluginsAPIInstall`
  does), and never call `UpdateMasterConfig` or a helper that takes the lock (`SaveAuth`,
  `AddOMFile`...) from fn: it deadlocks.
- fn returning an error writes nothing; `pkg/web` uses `configError` to carry the HTTP answer out
  and `errUnchanged` (with `updateMasterConfig`) to skip a write that changes nothing.
- On a missing file fn gets an empty `VCLUConfig`; an unreadable file is an error, not a fresh config.

## A missing file is not an error
`ReadMasterConfig` returns `nil, nil` when the file does not exist yet (first start). Check for `nil`.
