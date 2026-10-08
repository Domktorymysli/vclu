package lua

import (
	"github.com/Domktorymysli/vclu/pkg/metrics"
	lua "github.com/yuin/gopher-lua"
)

// registerMetricsFunctions registers the __go_metric_register bridge function.
// Called from Lua's metric() to register a metric in the Go collector.
func registerMetricsFunctions(L *lua.LState, mc *metrics.Collector) {
	if mc == nil {
		return
	}

	L.SetGlobal("__go_metric_register", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		unit := L.OptString(2, "")
		interval := L.OptInt(3, 60)
		mc.Register(path, unit, interval)
		return 0
	}))
}
