package main

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// InitFar2lCompat builds the fake PluginStartupInfo struct in WASM memory
// and calls the C plugin's SetStartupInfoW.
func InitFar2lCompat(ctx context.Context, mod api.Module, apiHost HostAPI) error {
	// 1. Allocate memory in WASM for PluginStartupInfo.
	// Since wazero doesn't have an easy "malloc" wrapper out of the box unless we call the guest's malloc,
	// and we don't want to rely on the guest having `malloc` exported, 
	// we will assume for this PoC that address 1024 is safe to use temporarily 
	// (usually true for simple WASI modules, heap starts higher).
	
	// NOTE: In a production far2l compat layer, we MUST export `malloc` from the C plugin
	// and call it from Go to allocate this struct safely.

	// struct PluginStartupInfo {
	//   int StructSize; (4 bytes)
	//   const wchar_t *ModuleName; (4 bytes ptr)
	//   ...
	//   FARAPIMESSAGE Message; (4 bytes fn ptr)
	//   ...
	// }
	
	// For this PoC, we will bypass the massive struct generation and just call a dummy function
	// to prove execution works. The C code will use our hardcoded import `far2l.Message`.
	
	fn := mod.ExportedFunction("SetStartupInfoW")
	if fn == nil {
		return fmt.Errorf("SetStartupInfoW not found")
	}

	// Call it with a null pointer for now, the C PoC won't actually dereference it
	// because building a 200-byte struct with thunk pointers here is >500 lines of code.
	_, err := fn.Call(ctx, 0)

	return err
}
