//go:build !windows

package main

import "github.com/ebitengine/purego"

// The native engine is a C-ABI shared library: .dylib on macOS, .so on Linux.
// purego loads it without cgo.

func openNativeLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func nativeSymbol(lib uintptr, name string) (uintptr, error) { return purego.Dlsym(lib, name) }

func closeNativeLibrary(lib uintptr) error { return purego.Dlclose(lib) }
