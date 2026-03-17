-- Simple Lua plugin for f4, using far2m/far3-like API
local version = far.AdvControl("ACTL_GETFARVERSION")
far.Message("Hello from Lua Plugin! Host version: " .. version)
