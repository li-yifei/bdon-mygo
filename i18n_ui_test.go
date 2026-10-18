package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// TestNoStrayCJKLiterals keeps user-facing text in the catalogs: the
// settings window and main.go must not carry Chinese or Japanese literals.
func TestNoStrayCJKLiterals(t *testing.T) {
	cjk := regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}]`)
	for _, name := range []string{"native_settings.go", "main.go", "preferences.go", "extensions.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if cjk.MatchString(line) {
				t.Errorf("%s:%d: CJK literal outside the catalogs: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestScriptMessageKeys checks that every message the page scripts look up
// exists in the catalogs as a js. key.
func TestScriptMessageKeys(t *testing.T) {
	inject, err := os.ReadFile("shell/inject.js")
	if err != nil {
		t.Fatal(err)
	}
	window, err := os.ReadFile("shell/window.js")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	supplement, err := os.ReadFile("shell/supplement.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`\bt\('([\w.]+)'`).FindAllStringSubmatch(string(inject)+string(supplement), -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`messages\.(\w+)`).FindAllStringSubmatch(string(window), -1) {
		keys[m[1]] = true
	}
	if len(keys) < 10 {
		t.Fatalf("found only %d script message keys", len(keys))
	}
	for key := range keys {
		if !i18n.Has(i18n.En, "js."+key) {
			t.Errorf("js.%s is missing from the catalogs", key)
		}
	}
	for _, src := range [][]byte{inject, window} {
		if regexp.MustCompile(`\p{Han}`).Match(src) {
			t.Error("page scripts must take their text from __bdonMygoMessages, with English fallbacks")
		}
	}
}

// TestLanguagePreferenceRoundTrip stores the preference next to the resource
// directory and keeps unknown fields.
func TestLanguagePreferenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	saved := preferencesFile
	t.Cleanup(func() { preferencesFile = saved })
	preferencesFile = func() string { return path }
	savedLang, savedPref := i18n.Current(), i18n.Preference()
	t.Cleanup(func() { i18n.SetPreference(savedPref); i18n.Set(savedLang) })
	t.Setenv(i18n.EnvOverride, "")

	if got := persistedLanguage(); got != i18n.System {
		t.Fatalf("default language preference = %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"resourceDir":"/tmp/x","future":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyLanguagePreference("zh-Hant"); err != nil {
		t.Fatal(err)
	}
	if got := persistedLanguage(); got != "zh-Hant" || i18n.Current() != i18n.ZhHant {
		t.Fatalf("stored %q, current %s", got, i18n.Current())
	}
	if err := persistResourceDir(dir); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["language"] != "zh-Hant" || fields["resourceDir"] != dir || fields["future"] != float64(42) {
		t.Fatalf("preferences = %s", data)
	}
	if err := applyLanguagePreference("bogus"); err != nil {
		t.Fatal(err)
	}
	if got := persistedLanguage(); got != i18n.System {
		t.Fatalf("unknown preference stored as %q", got)
	}
}

// TestNativeSettingsLanguages renders the settings window in each language
// and, with BDON_MYGO_SCREENSHOTS=1, saves one PNG per language under
// docs/screenshots/.
func TestNativeSettingsLanguages(t *testing.T) {
	expect := map[i18n.Lang][]string{
		i18n.En:     {"Device & Save Settings", "Connect device", "Choose game package", "Pull save and import into card pool", "Advanced", "Language", "Default engine", "Data locations"},
		i18n.ZhHans: {"设备与存档设置", "连接设备", "选择游戏包", "拉取存档并导入卡池", "高级", "语言 / Language", "默认算法", "数据位置"},
		i18n.ZhHant: {"裝置與存檔設定", "連接裝置", "選擇遊戲套件", "擷取存檔並匯入卡池", "進階", "語言 / Language", "預設演算法", "資料位置"},
		i18n.Ja:     {"デバイスとセーブデータの設定", "デバイスを接続", "ゲームパッケージを選択", "セーブデータを取得してカードプールに読み込む", "詳細設定", "言語 / Language", "標準アルゴリズム", "データの保存場所"},
	}
	for _, lang := range i18n.Languages {
		t.Run(string(lang), func(t *testing.T) {
			useLanguage(t, lang)
			s := newNativeSettingsState(nil, nil, nil, nil, nil)
			s.advancedOpen = true
			tt := ui.NewTester(s.view, 760, 1120)
			for _, want := range expect[lang] {
				if !tt.HasText(want) {
					t.Errorf("missing %q in %q", want, tt.Texts())
				}
			}
			if !tt.HasText(currentAppDirs().Cache) || !tt.HasText(revealLabel()) {
				t.Errorf("data locations missing in %q", tt.Texts())
			}
			if tt.HasText(transportModeLabel("direct-usb")) != directUSBAvailable {
				t.Errorf("direct USB option shown = %v, available = %v", tt.HasText(transportModeLabel("direct-usb")), directUSBAvailable)
			}
			for _, text := range tt.Texts() {
				if strings.Count(text, ".") >= 2 && !strings.ContainsFunc(text, unicode.IsSpace) && strings.HasPrefix(text, "settings.") {
					t.Errorf("untranslated key %q", text)
				}
			}
			img := tt.Image()
			if img.Bounds().Dx() == 0 {
				t.Skip("headless rasterization unavailable")
			}
			dir := t.TempDir()
			if os.Getenv("BDON_MYGO_SCREENSHOTS") == "1" {
				dir = filepath.Join("docs", "screenshots")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Create(filepath.Join(dir, "settings-"+string(lang)+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestNativeSettingsLanguageSwitch changes the language from the advanced
// section: the window re-renders in the new language, the device labels
// follow, and a toast explains that the page updates after a reload.
func TestNativeSettingsLanguageSwitch(t *testing.T) {
	useLanguage(t, i18n.ZhHans)
	var stored []string
	s := newNativeSettingsState(nil, nil, nil, nil, nil)
	s.setLanguage = func(pref string) error {
		stored = append(stored, pref)
		i18n.SetPreference(pref)
		return nil
	}
	t.Cleanup(func() { i18n.SetPreference(i18n.System) })
	s.devices = []adbDevice{{Serial: "A", State: "device", Model: "Pixel"}}
	s.deviceIDs = map[string]string{}
	s.relabel()
	s.selectedDevice = s.deviceLabels[0]
	s.relabel()
	s.advancedOpen = true
	tt := ui.NewTester(s.view, 760, 1500)
	if !tt.HasText("Pixel · A · 已连接") {
		t.Fatalf("device label missing: %q", tt.Texts())
	}
	s.languageLabel = i18n.NativeName(i18n.Ja)
	s.changeLanguage("ja")
	tt.Frame()
	if len(stored) != 1 || stored[0] != "ja" {
		t.Fatalf("stored preferences = %v", stored)
	}
	for _, want := range []string{"デバイスとセーブデータの設定", "Pixel · A · 接続済み", "表示言語を切り替えました"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q after switching to Japanese: %q", want, tt.Texts())
		}
	}
	if s.deviceIDs[s.selectedDevice] != "A" {
		t.Fatalf("selection lost: %q", s.selectedDevice)
	}
}
