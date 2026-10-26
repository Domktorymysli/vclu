### MCP

- **Promote no longer warns about a file loaded with `optional()`** - a module in a subdirectory that another module loads with `optional("dir/file")` counts as loaded, like `require`; the warning names both ([#132](https://github.com/Domktorymysli/vclu/issues/132)).
