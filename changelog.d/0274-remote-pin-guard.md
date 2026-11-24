### MCP

- **Alarms, locks and gates stay behind a PIN in the cloud** - the agent adds `:pin()` (gates: at least `:confirm()`) when it sends an alarm partition, a lock, a door, a gate or a garage to the cloud with `:remote()`, asks the owner for the PIN and never removes one; `vclu_draft_validate` warns about a draft that does not ([#274](https://github.com/Domktorymysli/vclu/issues/274)).
