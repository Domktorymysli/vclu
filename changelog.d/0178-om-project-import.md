### Grenton

- **Object Manager project import** - the OM Files tab takes the project itself (`.omp`, or a project `.zip`) next to the diagnostic pack: it registers the same CLUs and objects as the pack built from it, the vCLU itself skipped. Projects from OM 1.3 to 1.8 are read; what vCLU cannot read is listed as a warning and the rest is imported. Names that are not letters, digits and `_` are left out of the generated Lua (the object goes by its ID), also for om.lua from a diagnostic pack. Only the OM version is taken from `properties.xml`, which holds the project's keys; the upload is not kept on disk ([#178](https://github.com/Domktorymysli/vclu/issues/178)).

### MCP

- **Event bindings and CLU scripts for the agent** - an import of a project, or of a diagnostic pack that carries one, writes `ai/om/`: `index.md`, one file per CLU with its objects, peripherals and event bindings (in OM's notation, `Home->light->SwitchOn(0)`), and the CLU's scripts in `<CLU>-scripts.md`. `index.md` tells the agent how OM's notation reads in vCLU's Lua (`_:get("CLU….DOU0001"):switchOn(0)`), with examples from the project, and lists the objects OM's bindings and scripts already command, so an automation does not fight them unseen. The next import replaces the directory; a diagnostic pack without a project removes it ([#178](https://github.com/Domktorymysli/vclu/issues/178)).
