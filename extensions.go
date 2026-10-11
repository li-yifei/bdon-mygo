package main

import "github.com/egoist/mygo/ui"

// Optional extension points. A build may register an extra preload script
// (run before shell/inject.js) and extra controls for the advanced section of
// the native settings window. The default build registers none.
var (
	extensionPreload  func(storageRoot string) string
	extensionControls func(s *nativeSettingsState, c *ui.Context)
	// extensionSaveCredentials supplies build-time credentials; run-time
	// configuration takes precedence.
	extensionSaveCredentials func() (key, magic [32]byte, ok bool)
)

func extensionPreloadScript(storageRoot string) string {
	if extensionPreload == nil {
		return ""
	}
	return extensionPreload(storageRoot)
}
