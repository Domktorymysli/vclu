### MCP

- **`vclu_draft_promote` does not warn about a file another module requires** - promoting `modules/energia/power.lua` warned that `modules/init.lua` has no `require('energia/power')`, though `modules/energia/init.lua` requires it. A require in any module now counts; a file in a subdirectory that no module requires still gets the warning ([#20](https://github.com/Domktorymysli/vclu/issues/20)).
