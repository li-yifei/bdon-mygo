package i18n

import "golang.org/x/sys/windows"

// systemLanguages returns Windows' preferred display languages (Settings →
// Time & language → Language & region), most preferred first. Unlike the user
// locale (formats and region), this is the language the UI is shown in.
func systemLanguages() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return langs
}
