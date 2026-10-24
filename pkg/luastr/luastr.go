// Package luastr writes Go values into Lua scripts that Go builds.
package luastr

import "strings"

var quoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\x00", `\000`)

// Quote returns s as a Lua string literal: any value can go into a script
// built with fmt.Sprintf without ending the string early.
func Quote(s string) string {
	return `"` + quoter.Replace(s) + `"`
}
