package main

import (
	"context"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type WasmPlugin struct {
	path   string
	api    HostAPI
	rt     wazero.Runtime
	mod    api.Module
	isFar2l bool
}

func NewWasmPlugin(path string) *WasmPlugin {
	return &WasmPlugin{path: path}
}

func (p *WasmPlugin) Init(hostApi HostAPI) error {
	p.api = hostApi
	ctx := context.Background()
	p.rt = wazero.NewRuntime(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, p.rt)

	// Expose Modern F4 API
	_, err := p.rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(p.f4Log).Export("F4_Log").
		NewFunctionBuilder().WithFunc(p.f4GetVersion).Export("F4_GetVersion").
		Instantiate(ctx)
	if err != nil {
		return err
	}

	// Expose Far2l Compat API (Stubbed)
	_, err = p.rt.NewHostModuleBuilder("far2l").
		NewFunctionBuilder().WithFunc(p.far2lMessage).Export("Message").
		Instantiate(ctx)
	if err != nil {
		return err
	}

	code, err := os.ReadFile(p.path)
	if err != nil {
		return err
	}

	p.mod, err = p.rt.Instantiate(ctx, code)
	if err != nil {
		return err
	}

	// Check if it's a legacy Far2l C plugin.
	// If so, we need to call SetStartupInfoW manually.
	if p.mod.ExportedFunction("SetStartupInfoW") != nil {
		p.isFar2l = true
		return InitFar2lCompat(ctx, p.mod, p.api)
	}

	return nil
}

// F4_Log(ptr, len)
func (p *WasmPlugin) f4Log(ctx context.Context, m api.Module, ptr uint32, len uint32) {
	if bytes, ok := m.Memory().Read(ptr, len); ok {
		p.api.Log(string(bytes))
	}
}

// F4_GetVersion(ptr, maxlen) -> len
func (p *WasmPlugin) f4GetVersion(ctx context.Context, m api.Module, ptr uint32, maxlen uint32) uint32 {
	ver := p.api.GetVersion()
	if uint32(len(ver)) > maxlen {
		return 0
	}
	m.Memory().Write(ptr, []byte(ver))
	return uint32(len(ver))
}

// far2lMessage - simplified thunk for far2l C-API compat
func (p *WasmPlugin) far2lMessage(ctx context.Context, m api.Module, msgPtr uint32) {
	// Read null-terminated wide string (simplification: assuming UTF-8 or ASCII for PoC)
	// Real far2l uses UTF-16LE. For this PoC, we'll just read bytes until 0.
	var bytes []byte
	for i := msgPtr; ; i++ {
		b, ok := m.Memory().Read(i, 1)
		if !ok || b[0] == 0 {
			break
		}
		bytes = append(bytes, b[0])
	}
	p.api.Message(string(bytes))
}

func (p *WasmPlugin) Close() error {
	if p.rt != nil {
		return p.rt.Close(context.Background())
	}
	return nil
}

func (p *WasmPlugin) GetName() string {
	if p.isFar2l {
		return p.path + " (Far2l C-API Compat)"
	}
	return p.path + " (F4 API)"
}
