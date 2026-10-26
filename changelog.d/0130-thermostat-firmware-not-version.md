### Grenton

- **A thermostat is polled when its CLU answers `get(17)` with something that is not a version** - before, such a CLU was taken as down and none of its thermostat's features was read, so current and target temperature stayed empty; a CLU that does not answer is still skipped, and the log now tells "no answer to get(17)" from "answer to get(17) is not a version" ([#130](https://github.com/Domktorymysli/vclu/issues/130)).
