//go:build !darwin && !windows

package i18n

// systemLanguages has no platform list here; Detect reads $LANGUAGE and the
// POSIX locale variables.
func systemLanguages() []string { return nil }
