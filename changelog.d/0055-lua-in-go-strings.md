### Maintenance

- **Lua that Go runs lives in `.lua` files** - the sandbox helpers, the HomeKit, web, plugin and startup scripts moved out of Go strings into embedded files next to their packages, and a test compiles every embedded file and fails when Lua logic comes back into a Go string ([#55](https://github.com/Domktorymysli/vclu/issues/55)).
