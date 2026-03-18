// Minimal Far2l C-API compatibility demonstration.
// In a real scenario, this includes <plugin.hpp>.

// This is the thunk provided by wazero host in far2l_compat.go
__attribute__((import_module("far2l"), import_name("Message")))
void FarMessage(const char* msg);

// Exported function expected by Far2l
__attribute__((export_name("SetStartupInfoW")))
void SetStartupInfoW(void* info) {
    // In a real far2l plugin, we would save 'info' and call info->Message(...)
    // Here we call the thunk directly to prove C -> WASM -> Go host execution works.
    FarMessage("Hello from C WASM Plugin via far2l compat API!");
}

int main() {
    return 0;
}
