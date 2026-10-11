// Package i18n holds the message catalogs of the native user interface:
// Simplified Chinese, Traditional Chinese, Japanese and English.
//
// Messages are looked up by stable keys and may carry named placeholders
// such as {count} or {error}, filled from name/value pairs:
//
//	i18n.T("settings.devices.found", "count", 3)
//
// The language follows the user's preference (see SetPreference), by default
// the system language (see Detect). It never follows the web page, which
// manages its own locale. A key missing from a language falls back to
// English, then to the key itself. Keys under "js." are handed to the page
// scripts (see Preload), where the same {name} placeholders are used.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Lang is a supported catalog language.
type Lang string

const (
	En     Lang = "en"
	ZhHans Lang = "zh-Hans"
	ZhHant Lang = "zh-Hant"
	Ja     Lang = "ja"
)

// Languages lists every supported language, English (the reference) first.
var Languages = []Lang{En, ZhHans, ZhHant, Ja}

// System is the preference value that follows the system language.
const System = "system"

// EnvOverride names the environment variable that forces a language, for
// tests: OURNOTES_LANG=ja, OURNOTES_LANG=zh-TW, ...
const EnvOverride = "OURNOTES_LANG"

// NativeName returns a language's name in that language, as the language
// selector shows it whatever the current language.
func NativeName(lang Lang) string {
	switch lang {
	case ZhHans:
		return "简体中文"
	case ZhHant:
		return "繁體中文"
	case Ja:
		return "日本語"
	}
	return "English"
}

//go:embed locales/*.json
var localeFS embed.FS

var (
	mu         sync.RWMutex
	catalogs   = map[Lang]map[string]string{}
	current    = En
	preference = System
	listeners  = map[int]func(Lang){}
	nextID     int
)

// SystemLanguages returns the user's preferred user-interface languages, most
// preferred first: Windows' GetUserPreferredUILanguages, macOS's
// AppleLanguages, or $LANGUAGE and the POSIX variables elsewhere.
var SystemLanguages = systemLanguages

// SystemLocale returns one system locale as a fallback when SystemLanguages
// names no supported language. The app sets it to mygo.App.Locale.
var SystemLocale func() string

func init() {
	for _, lang := range Languages {
		data, err := localeFS.ReadFile("locales/" + string(lang) + ".json")
		if err != nil {
			panic("i18n: " + err.Error())
		}
		if err := RegisterJSON(lang, data); err != nil {
			panic("i18n: locales/" + string(lang) + ".json: " + err.Error())
		}
	}
	current = Detect()
}

// Register adds messages to a language's catalog. Optional build layers
// register their own keys through it.
func Register(lang Lang, messages map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	c := catalogs[lang]
	if c == nil {
		c = map[string]string{}
		catalogs[lang] = c
	}
	for k, v := range messages {
		c[k] = v
	}
}

// RegisterJSON registers a flat JSON object of key → message.
func RegisterJSON(lang Lang, data []byte) error {
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		return err
	}
	Register(lang, messages)
	return nil
}

// Current returns the selected language.
func Current() Lang {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Set selects the catalog used by T and notifies the OnChange listeners when
// the language changed.
func Set(lang Lang) {
	if !Supported(lang) {
		lang = En
	}
	mu.Lock()
	changed := current != lang
	current = lang
	fns := make([]func(Lang), 0, len(listeners))
	ids := make([]int, 0, len(listeners))
	for id := range listeners {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		fns = append(fns, listeners[id])
	}
	mu.Unlock()
	if changed {
		for _, fn := range fns {
			fn(lang)
		}
	}
}

// Supported reports whether lang has a catalog.
func Supported(lang Lang) bool {
	for _, l := range Languages {
		if l == lang {
			return true
		}
	}
	return false
}

// Preference returns the user's language preference: System or a language.
func Preference() string {
	mu.RLock()
	defer mu.RUnlock()
	return preference
}

// NormalizePreference returns System for an empty or unknown preference.
func NormalizePreference(pref string) string {
	if Supported(Lang(pref)) {
		return pref
	}
	return System
}

// SetPreference records the user's preference and selects the language it
// resolves to (see Resolve). It returns that language.
func SetPreference(pref string) Lang {
	pref = NormalizePreference(pref)
	mu.Lock()
	preference = pref
	mu.Unlock()
	lang := Resolve(pref)
	Set(lang)
	return lang
}

// Resolve returns the language for a preference: OURNOTES_LANG wins, then an
// explicit language, then the detected system language.
func Resolve(pref string) Lang {
	if v := strings.TrimSpace(os.Getenv(EnvOverride)); v != "" {
		return Match(v)
	}
	if Supported(Lang(pref)) {
		return Lang(pref)
	}
	return Detect()
}

// OnChange registers fn to run after the language changed, on the goroutine
// that changed it. It returns a function that unregisters fn.
func OnChange(fn func(Lang)) (cancel func()) {
	mu.Lock()
	nextID++
	id := nextID
	listeners[id] = fn
	mu.Unlock()
	return func() {
		mu.Lock()
		delete(listeners, id)
		mu.Unlock()
	}
}

// Detect returns the system language: OURNOTES_LANG overrides it; otherwise
// the first supported entry of SystemLanguages, then SystemLocale, then the
// POSIX variables. Without a supported entry it is English.
func Detect() Lang {
	if v := strings.TrimSpace(os.Getenv(EnvOverride)); v != "" {
		return Match(v)
	}
	var candidates []string
	if SystemLanguages != nil {
		candidates = append(candidates, SystemLanguages()...)
	}
	if SystemLocale != nil {
		candidates = append(candidates, SystemLocale())
	}
	candidates = append(candidates, posixLanguages()...)
	for _, c := range candidates {
		if lang, ok := MatchSupported(c); ok {
			return lang
		}
	}
	return En
}

var (
	detectedOnce sync.Once
	detected     Lang
)

// SystemLanguage is Detect computed once per run, for labels drawn on every
// frame (Detect may start a process on macOS).
func SystemLanguage() Lang {
	detectedOnce.Do(func() { detected = Detect() })
	return detected
}

// posixLanguages lists $LANGUAGE (colon-separated, GNU gettext) and then the
// first set of LC_ALL, LC_MESSAGES and LANG.
func posixLanguages() []string {
	var out []string
	for _, v := range strings.Split(os.Getenv("LANGUAGE"), ":") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(env); v != "" && v != "C" && v != "POSIX" {
			return append(out, v)
		}
	}
	return out
}

// AppleLanguages parses the plist array printed by
// `defaults read -g AppleLanguages`.
func AppleLanguages(out string) []string {
	var langs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.Trim(strings.TrimSpace(line), `",`)
		if line == "" || line == "(" || line == ")" {
			continue
		}
		langs = append(langs, line)
	}
	return langs
}

// FirstAppleLanguage returns the first entry of AppleLanguages output.
func FirstAppleLanguage(out string) string {
	if langs := AppleLanguages(out); len(langs) > 0 {
		return langs[0]
	}
	return ""
}

// MatchSupported maps a tag to a supported language and reports whether the
// tag names one (Match returns English for every other language).
func MatchSupported(locale string) (Lang, bool) {
	lang := Match(locale)
	if lang != En {
		return lang, true
	}
	primary := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(locale)), func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == '@' })
	return En, len(primary) > 0 && (primary[0] == "en" || primary[0] == "eng")
}

// Match maps a BCP 47 tag ("zh-Hant-TW") or POSIX locale ("zh_TW.UTF-8") to
// a supported language: zh-Hans, zh-CN, zh-SG and bare zh → zh-Hans;
// zh-Hant, zh-TW, zh-HK, zh-MO → zh-Hant; ja* → ja; anything else → en.
// An explicit script subtag wins over the region (zh-Hans-HK → zh-Hans).
func Match(locale string) Lang {
	locale, _, _ = strings.Cut(strings.TrimSpace(locale), ".")
	locale, _, _ = strings.Cut(locale, "@")
	parts := strings.FieldsFunc(strings.ToLower(locale), func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return En
	}
	switch parts[0] {
	case "ja", "jpn":
		return Ja
	case "zh", "zho", "chi", "cmn", "yue":
		traditional := parts[0] == "yue"
		for _, p := range parts[1:] {
			switch p {
			case "hans":
				return ZhHans
			case "hant":
				return ZhHant
			case "tw", "hk", "mo":
				traditional = true
			case "cn", "sg", "my":
				traditional = false
			}
		}
		if traditional {
			return ZhHant
		}
		return ZhHans
	}
	return En
}

// T returns the message for key in the current language with its {name}
// placeholders filled from kv, alternating names and values.
func T(key string, kv ...any) string { return TIn(Current(), key, kv...) }

// TIn is T for an explicit language.
func TIn(lang Lang, key string, kv ...any) string {
	msg, ok := lookup(lang, key)
	if !ok {
		msg, ok = lookup(En, key)
	}
	if !ok {
		msg = key
	}
	return format(msg, kv)
}

func format(msg string, kv []any) string {
	if len(kv) < 2 || !strings.Contains(msg, "{") {
		return msg
	}
	pairs := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{"+fmt.Sprint(kv[i])+"}", fmt.Sprint(kv[i+1]))
	}
	return strings.NewReplacer(pairs...).Replace(msg)
}

func lookup(lang Lang, key string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	msg, ok := catalogs[lang][key]
	return msg, ok
}

// Has reports whether lang's catalog defines key.
func Has(lang Lang, key string) bool {
	_, ok := lookup(lang, key)
	return ok
}

// Keys returns the sorted keys of a language's catalog.
func Keys(lang Lang) []string {
	mu.RLock()
	defer mu.RUnlock()
	keys := make([]string, 0, len(catalogs[lang]))
	for k := range catalogs[lang] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ScriptMessages returns the "js." messages of lang, with English filling
// any gap, keyed without the "js." prefix.
func ScriptMessages(lang Lang) map[string]string {
	out := map[string]string{}
	for _, l := range []Lang{En, lang} {
		for _, k := range Keys(l) {
			if name, ok := strings.CutPrefix(k, "js."); ok {
				out[name], _ = lookup(l, k)
			}
		}
	}
	return out
}

// pageLangKey is the page's sessionStorage entry that carries a language
// chosen after the page was created across reloads.
const pageLangKey = "bdon-mygo:lang"

// Preload returns the page script that exposes the script messages as
// window.__bdonMygoMessages and the language as window.__bdonMygoLang. It
// carries every language: the preload is fixed when the window is created,
// so a language chosen later (see PageSwitchScript) is remembered in the
// page's sessionStorage and picked up again when the page reloads. It only
// covers the desktop shell's own strings; the page's locale is untouched.
func Preload() string {
	catalogs := map[Lang]map[string]string{}
	for _, lang := range Languages {
		catalogs[lang] = ScriptMessages(lang)
	}
	b, _ := json.Marshal(catalogs)
	lang, _ := json.Marshal(string(Current()))
	key, _ := json.Marshal(pageLangKey)
	return "(() => {\n" +
		"  const catalogs = " + string(b) + ";\n" +
		"  let lang = " + string(lang) + ";\n" +
		"  try { const chosen = sessionStorage.getItem(" + string(key) + "); if (chosen && catalogs[chosen]) lang = chosen; } catch { /* storage unavailable */ }\n" +
		"  window.__bdonMygoLang = lang;\n" +
		"  window.__bdonMygoMessages = catalogs[lang];\n" +
		"})();\n"
}

// PageSwitchScript returns the script that switches a loaded page to the
// current language: messages looked up from now on use it, and the choice
// survives a reload of the page.
func PageSwitchScript() string {
	b, _ := json.Marshal(ScriptMessages(Current()))
	lang, _ := json.Marshal(string(Current()))
	key, _ := json.Marshal(pageLangKey)
	return "try { sessionStorage.setItem(" + string(key) + ", " + string(lang) + "); } catch { /* storage unavailable */ }\n" +
		"window.__bdonMygoLang = " + string(lang) + ";\n" +
		"window.__bdonMygoMessages = " + string(b) + ";\n"
}
