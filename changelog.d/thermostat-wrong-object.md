### Dashboards

- **A wrong object in `thermostat()` says what is wrong** - an object that is not a thermostat (e.g. a DOUT from `_:get(...)`) failed with `unknown key "_clu" in fns`, as if it were a table of functions; it now fails with `DOU9270 (DOUT) is not a thermostat: it has no getTarget, setSetpoint`. A missing object (`_:get` of a wrong path gives nil) now says to check the path. Any object with `getValue`, `getTarget` and `setSetpoint` works, a virtual thermostat written in Lua too.
