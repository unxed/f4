package main

import (
	lua "github.com/yuin/gopher-lua"
)

type LuaPlugin struct {
	path string
	L    *lua.LState
	api  HostAPI
}

func NewLuaPlugin(path string) *LuaPlugin {
	return &LuaPlugin{path: path}
}

func (p *LuaPlugin) Init(api HostAPI) error {
	p.api = api
	p.L = lua.NewState()

	// Create 'far' compatibility table
	farTable := p.L.NewTable()
	
	// far.Message(string)
	p.L.SetField(farTable, "Message", p.L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		p.api.Message(msg)
		return 0
	}))

	// far.AdvControl(command, param)
	p.L.SetField(farTable, "AdvControl", p.L.NewFunction(func(L *lua.LState) int {
		cmd := L.CheckString(1)
		if cmd == "ACTL_GETFARVERSION" {
			L.Push(lua.LString(p.api.GetVersion()))
			return 1
		}
		L.Push(lua.LNil)
		return 1
	}))

	p.L.SetGlobal("far", farTable)

	// Execute the script. In a real plugin, this would just register exports,
	// but for our test, it will execute logic immediately.
	if err := p.L.DoFile(p.path); err != nil {
		return err
	}

	return nil
}

func (p *LuaPlugin) Close() error {
	p.L.Close()
	return nil
}

func (p *LuaPlugin) GetName() string {
	return p.path
}
