package i18n

import "os/exec"

// systemLanguages reads macOS's preferred languages (System Settings →
// General → Language & Region), most preferred first.
func systemLanguages() []string {
	out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return nil
	}
	return AppleLanguages(string(out))
}
