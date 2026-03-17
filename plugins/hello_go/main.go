package main

import "unsafe"

// Modern F4 WASM API imports
//go:wasmimport env F4_Log
func f4Log(ptr uint32, len uint32)

//go:wasmimport env F4_GetVersion
func f4GetVersion(ptr uint32, maxlen uint32) uint32

func main() {
	buf := make([]byte, 64)
	n := f4GetVersion(uint32(uintptr(unsafe.Pointer(&buf[0]))), uint32(len(buf)))

	ver := string(buf[:n])
	msg := "Hello from Go WASM Plugin! Host version: " + ver

	b := []byte(msg)
	f4Log(uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}
