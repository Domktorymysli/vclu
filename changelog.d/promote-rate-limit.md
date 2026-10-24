### MCP

- **60 promotes an hour instead of 10** - `vclu_draft_promote` allowed 10 calls an hour, which an evening of dashboard work on a real installation reached three times. The limit is there so a looping agent cannot reload the runtime without end; 60 (one reload a minute on average) still does that. The tool description and the limit's error now say to promote the files of one change in one call (a path array or `"all"`).
