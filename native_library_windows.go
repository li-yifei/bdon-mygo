//go:build windows

package main

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// purego has no Dlopen on Windows: load the DLL with the system loader and
// hand its module handle to purego.RegisterLibFunc. LOAD_WITH_ALTERED_SEARCH_PATH
// resolves the DLL's own dependencies from its directory first.

func openNativeLibrary(path string) (uintptr, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	h, err := windows.LoadLibraryEx(path, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	return uintptr(h), err
}

func nativeSymbol(lib uintptr, name string) (uintptr, error) {
	return windows.GetProcAddress(windows.Handle(lib), name)
}

func closeNativeLibrary(lib uintptr) error { return windows.FreeLibrary(windows.Handle(lib)) }
