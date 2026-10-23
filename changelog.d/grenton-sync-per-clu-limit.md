### Grenton

- **Object sync is no longer limited to 64 entries per CLU** - sync subscribed at most 64 entries on each CLU (one per object, more for objects that subscribe several features, such as a roller) and dropped the rest with only a `truncated at 64 subscription entries` warning in the log, so an installation with 4 CLUs stopped at about 256 objects while the sync settings showed all of them as saved. Which objects were dropped changed from run to run. Every whitelisted object is now subscribed, in the same batches on every run (sorted by path).
- **A large sync is not cut off by the 30 s limit** - the whole sync had a fixed 30 s budget, checked before each CLU in random order, and the CLUs left over were skipped without being counted. The budget now grows with the number of batches (at least 30 s, 0.25 s per batch) and the summary counts skipped CLUs. A full re-subscribe still holds the Lua runtime for its whole run (about 15 s for ~730 objects with batch size 4); the periodic renew is unaffected. Not tested on Grenton hardware with more than 64 entries per CLU: if late objects stop updating, try batch size 8, which halves the number of sessions per CLU.

### Web

- **The sidebar shows the vCLU version** - next to "Online" in the lower left corner (e.g. `v1.3.0`, `dev` for a local build); with the sidebar collapsed it is in the status dot's tooltip. Only on pages behind the login, not on the login page or public dashboards.
