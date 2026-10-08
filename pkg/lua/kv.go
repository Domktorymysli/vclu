package lua

import (
	"github.com/Domktorymysli/vclu/pkg/plugins"
	lua "github.com/yuin/gopher-lua"
)

// registerKVFunctions registers KV store functions in Lua
// Functions:
//   - __go_kv_get(pluginID, key) -> value, exists
//   - __go_kv_set(pluginID, key, value, secure, ttl) -> error
//   - __go_kv_delete(pluginID, key) -> error
//   - __go_kv_has(pluginID, key) -> bool
//   - __go_kv_list(pluginID, prefix) -> table
//   - __go_kv_get_all(pluginID) -> table
func registerKVFunctions(L *lua.LState, kvManager *plugins.KVStoreManager) {
	if kvManager == nil {
		return
	}

	// __go_kv_get(pluginID, key) -> value, exists
	L.SetGlobal("__go_kv_get", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)
		key := L.CheckString(2)

		store := kvManager.GetStore(pluginID)
		value, exists := store.Get(key)

		if !exists {
			L.Push(lua.LNil)
			L.Push(lua.LFalse)
			return 2
		}

		L.Push(goValueToLua(L, value))
		L.Push(lua.LTrue)
		return 2
	}))

	// __go_kv_set(pluginID, key, value, secure, ttl) -> error
	L.SetGlobal("__go_kv_set", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)
		key := L.CheckString(2)
		value, err := LuaToGo(L.Get(3))
		if err != nil {
			L.Push(lua.LString("kv: " + err.Error()))
			return 1
		}
		secure := L.OptBool(4, false)
		ttl := L.OptInt64(5, 0)

		store := kvManager.GetStore(pluginID)
		err = store.Set(key, value, secure, ttl)

		if err != nil {
			L.Push(lua.LString(err.Error()))
			return 1
		}

		L.Push(lua.LNil)
		return 1
	}))

	// __go_kv_delete(pluginID, key) -> error
	L.SetGlobal("__go_kv_delete", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)
		key := L.CheckString(2)

		store := kvManager.GetStore(pluginID)
		err := store.Delete(key)

		if err != nil {
			L.Push(lua.LString(err.Error()))
			return 1
		}

		L.Push(lua.LNil)
		return 1
	}))

	// __go_kv_has(pluginID, key) -> bool
	L.SetGlobal("__go_kv_has", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)
		key := L.CheckString(2)

		store := kvManager.GetStore(pluginID)
		exists := store.Has(key)

		L.Push(lua.LBool(exists))
		return 1
	}))

	// __go_kv_list(pluginID, prefix) -> table
	L.SetGlobal("__go_kv_list", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)
		prefix := L.OptString(2, "")

		store := kvManager.GetStore(pluginID)
		keys := store.List(prefix)

		// Convert to Lua table
		table := L.NewTable()
		for i, key := range keys {
			table.RawSetInt(i+1, lua.LString(key))
		}

		L.Push(table)
		return 1
	}))

	// __go_kv_get_all(pluginID) -> table
	L.SetGlobal("__go_kv_get_all", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)

		store := kvManager.GetStore(pluginID)
		data := store.GetAll()

		// Convert to Lua table
		table := L.NewTable()
		for key, value := range data {
			table.RawSetString(key, goValueToLua(L, value))
		}

		L.Push(table)
		return 1
	}))
}

// Note: goValueToLua and LuaToGo are defined in parsers.go
