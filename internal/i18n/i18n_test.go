package i18n

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var placeholder = regexp.MustCompile(`\{\w+\}`)

func placeholders(msg string) string {
	found := placeholder.FindAllString(msg, -1)
	sort.Strings(found)
	return strings.Join(found, ",")
}

// TestCatalogsComplete checks that every English key exists in the other
// languages with the same placeholders, and that no language has keys
// English lacks.
func TestCatalogsComplete(t *testing.T) {
	en := Keys(En)
	if len(en) == 0 {
		t.Fatal("empty English catalog")
	}
	for _, lang := range Languages[1:] {
		for _, key := range en {
			msg, ok := lookup(lang, key)
			if !ok {
				t.Errorf("%s: missing %q", lang, key)
				continue
			}
			ref, _ := lookup(En, key)
			if strings.TrimSpace(msg) == "" {
				t.Errorf("%s: empty %q", lang, key)
			}
			if placeholders(msg) != placeholders(ref) {
				t.Errorf("%s: %q placeholders %q, English has %q", lang, key, placeholders(msg), placeholders(ref))
			}
		}
		for _, key := range Keys(lang) {
			if !Has(En, key) {
				t.Errorf("%s: %q is not in the English catalog", lang, key)
			}
		}
	}
}

func TestMatch(t *testing.T) {
	cases := map[string]Lang{
		"zh-Hans": ZhHans, "zh-Hans-CN": ZhHans, "zh-CN": ZhHans, "zh_CN.UTF-8": ZhHans, "zh-SG": ZhHans, "zh": ZhHans,
		"zh-Hant": ZhHant, "zh-Hant-TW": ZhHant, "zh-TW": ZhHant, "zh_TW.UTF-8": ZhHant, "zh-HK": ZhHant, "zh-MO": ZhHant,
		"zh-Hans-HK": ZhHans, "zh-Hant-CN": ZhHant,
		"ja": Ja, "ja-JP": Ja, "ja_JP.UTF-8": Ja,
		"en": En, "en-US": En, "fr-FR": En, "ko-KR": En, "": En, "C": En,
	}
	for in, want := range cases {
		if got := Match(in); got != want {
			t.Errorf("Match(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFirstAppleLanguage(t *testing.T) {
	out := "(\n    \"zh-Hant-TW\",\n    \"en-US\"\n)\n"
	if got := FirstAppleLanguage(out); got != "zh-Hant-TW" {
		t.Fatalf("FirstAppleLanguage = %q", got)
	}
	if got := FirstAppleLanguage("(\n    ja\n)\n"); got != "ja" {
		t.Fatalf("FirstAppleLanguage unquoted = %q", got)
	}
}

func TestDetect(t *testing.T) {
	saved, savedList := SystemLocale, SystemLanguages
	t.Cleanup(func() { SystemLocale, SystemLanguages = saved, savedList })
	SystemLanguages = nil
	t.Setenv("LANGUAGE", "")
	t.Setenv(EnvOverride, "")
	SystemLocale = func() string { return "ja-JP" }
	if got := Detect(); got != Ja {
		t.Fatalf("Detect = %s", got)
	}
	SystemLocale = func() string { return "" }
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "zh_TW.UTF-8")
	if got := Detect(); got != ZhHant {
		t.Fatalf("Detect from LC_MESSAGES = %s", got)
	}
	t.Setenv(EnvOverride, "zh-CN")
	if got := Detect(); got != ZhHans {
		t.Fatalf("Detect with %s = %s", EnvOverride, got)
	}
}

func TestPreference(t *testing.T) {
	savedLocale, savedList, savedLang, savedPref := SystemLocale, SystemLanguages, Current(), Preference()
	t.Cleanup(func() {
		SystemLocale, SystemLanguages = savedLocale, savedList
		SetPreference(savedPref)
		Set(savedLang)
	})
	t.Setenv(EnvOverride, "")
	SystemLanguages = func() []string { return []string{"zh-Hans-CN"} }
	SystemLocale = nil
	if got := SetPreference(System); got != ZhHans || Preference() != System {
		t.Fatalf("system preference = %s %s", got, Preference())
	}
	if got := SetPreference("ja"); got != Ja || Current() != Ja {
		t.Fatalf("ja preference = %s", got)
	}
	if got := SetPreference("klingon"); got != ZhHans || Preference() != System {
		t.Fatalf("unknown preference = %s %s", got, Preference())
	}
}

func TestTAndFallback(t *testing.T) {
	Register(En, map[string]string{"test.only.en": "Found {count} of {total}"})
	Register(Ja, map[string]string{"test.ja": "{count} 件"})
	if got := TIn(Ja, "test.only.en", "count", 2, "total", 5); got != "Found 2 of 5" {
		t.Fatalf("English fallback = %q", got)
	}
	if got := TIn(Ja, "test.ja", "count", 3); got != "3 件" {
		t.Fatalf("T = %q", got)
	}
	if got := TIn(Ja, "test.absent"); got != "test.absent" {
		t.Fatalf("missing key = %q", got)
	}
	if got := TIn(En, "test.only.en", "count", 1); got != "Found 1 of {total}" {
		t.Fatalf("unfilled placeholder = %q", got)
	}
}

func TestOnChange(t *testing.T) {
	saved := Current()
	t.Cleanup(func() { Set(saved) })
	Set(En)
	var got []Lang
	cancel := OnChange(func(l Lang) { got = append(got, l) })
	Set(Ja)
	Set(Ja) // unchanged: no notification
	cancel()
	Set(ZhHant)
	if fmt.Sprint(got) != "[ja]" {
		t.Fatalf("notifications = %v", got)
	}
}

func TestErrorDescribe(t *testing.T) {
	saved := Current()
	t.Cleanup(func() { Set(saved) })
	Set(ZhHans)
	cause := errors.New("exit status 1: adb: device offline")
	err := fmt.Errorf("context: %w", Wrap(cause, "adb.error.startServer"))
	reason, raw := Describe(err)
	if reason != "启动 adb server 失败" || raw != cause.Error() {
		t.Fatalf("Describe = %q, %q", reason, raw)
	}
	if !errors.Is(err, cause) || !errors.Is(err, New("adb.error.startServer")) {
		t.Fatal("errors.Is should see the cause and the key")
	}
	reason, raw = Describe(New("adb.error.unsafeZip", "name", "../x"))
	if reason != "不安全的 ZIP 项：../x" || raw != "" {
		t.Fatalf("Describe without cause = %q, %q", reason, raw)
	}
	if _, raw = Describe(errors.New("plain")); raw != "plain" {
		t.Fatalf("plain raw = %q", raw)
	}
	Set(En)
	if got := Wrap(cause, "adb.error.startServer").Error(); got != "Starting the adb server failed: "+cause.Error() {
		t.Fatalf("Error() = %q", got)
	}
}

func TestPreload(t *testing.T) {
	saved := Current()
	t.Cleanup(func() { Set(saved) })
	Set(Ja)
	p := Preload()
	for _, want := range []string{`"settingsButton":"ローカル設定"`, `"settingsButton":"本地设置"`, `let lang = "ja";`, `window.__bdonMygoMessages = catalogs[lang]`, `sessionStorage.getItem("bdon-mygo:lang")`} {
		if !strings.Contains(p, want) {
			t.Errorf("Preload lacks %s:\n%s", want, p)
		}
	}
	if strings.Contains(p, `"js.`) {
		t.Fatal("script keys must drop the js. prefix")
	}
	Set(ZhHant)
	sw := PageSwitchScript()
	if !strings.Contains(sw, `sessionStorage.setItem("bdon-mygo:lang", "zh-Hant")`) || !strings.Contains(sw, `"settingsButton":"本機設定"`) {
		t.Fatalf("PageSwitchScript = %s", sw)
	}
}

// TestPreloadRuns evaluates the preload with node: the page language comes
// from the preload, or from sessionStorage after PageSwitchScript.
func TestPreloadRuns(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	saved := Current()
	t.Cleanup(func() { Set(saved) })
	Set(En)
	preload := Preload()
	Set(Ja)
	switchScript := PageSwitchScript()
	script := `const store = {}; globalThis.window = globalThis;
globalThis.sessionStorage = {getItem: k => store[k] ?? null, setItem: (k, v) => { store[k] = v; }};
const vm = require('node:vm'); const preload = process.argv[1], switchScript = process.argv[2];
vm.runInThisContext(preload); const first = window.__bdonMygoLang + '|' + window.__bdonMygoMessages.settingsButton;
vm.runInThisContext(switchScript); vm.runInThisContext(preload);
console.log(first + '|' + window.__bdonMygoLang + '|' + window.__bdonMygoMessages.settingsButton);`
	out, err := exec.Command(node, "-e", script, preload, switchScript).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "en|Local settings|ja|ローカル設定" {
		t.Fatalf("preload result = %q", got)
	}
}

func TestDetectWalksThePreferenceList(t *testing.T) {
	saved, savedList := SystemLocale, SystemLanguages
	t.Cleanup(func() { SystemLocale, SystemLanguages = saved, savedList })
	t.Setenv(EnvOverride, "")
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
	cases := []struct {
		list   []string
		locale string
		want   Lang
	}{
		{[]string{"ko-KR", "ja-JP", "en-US"}, "", Ja}, // skip an unsupported first choice
		{[]string{"zh-Hant-HK"}, "ja-JP", ZhHant},     // UI language beats the region locale
		{[]string{"zh-CN"}, "", ZhHans},
		{[]string{"en-GB", "ja-JP"}, "", En}, // English chosen explicitly
		{[]string{"ko-KR", "fr-FR"}, "", En}, // nothing supported
		{nil, "ja-JP", Ja},                   // fall back to the locale
	}
	for _, c := range cases {
		list, locale := c.list, c.locale
		SystemLanguages = func() []string { return list }
		SystemLocale = func() string { return locale }
		if got := Detect(); got != c.want {
			t.Errorf("Detect(%v, %q) = %s, want %s", c.list, c.locale, got, c.want)
		}
	}
	SystemLanguages, SystemLocale = nil, nil
	t.Setenv("LANGUAGE", "ko:zh_TW:en")
	if got := Detect(); got != ZhHant {
		t.Errorf("Detect($LANGUAGE) = %s", got)
	}
}
