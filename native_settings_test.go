package main

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// useLanguage selects lang for one test.
func useLanguage(t *testing.T, lang i18n.Lang) {
	t.Helper()
	saved := i18n.Current()
	i18n.Set(lang)
	t.Cleanup(func() { i18n.Set(saved) })
}

func TestNativeSettingsGuidedFlow(t *testing.T) {
	useLanguage(t, i18n.ZhHans)
	s := newNativeSettingsState(nil, nil, nil, nil, nil)
	tt := ui.NewTester(s.view, 760, 1200)
	for _, want := range []string{"连接设备", "选择设备", "选择游戏包", "导入卡池", "拉取存档并导入卡池", "高级"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// Advanced settings stay collapsed for beginners.
	for _, hidden := range []string{"实验性替换算法", "转交 USB 给独立通信层"} {
		if tt.HasText(hidden) {
			t.Errorf("%q should be inside the collapsed 高级 section", hidden)
		}
	}
	if err := tt.Click("高级"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"实验性替换算法", "官方 ADB 兼容模式"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q after opening 高级", want)
		}
	}
}

func TestNativeSettingsExtensionControls(t *testing.T) {
	useLanguage(t, i18n.ZhHans)
	saved := extensionControls
	t.Cleanup(func() { extensionControls = saved })
	extensionControls = func(s *nativeSettingsState, c *ui.Context) {
		ui.Text(c, "扩展控件")
	}
	s := newNativeSettingsState(nil, nil, nil, nil, nil)
	tt := ui.NewTester(s.view, 760, 1200)
	if tt.HasText("扩展控件") {
		t.Error("extension controls should be inside the collapsed 高级 section")
	}
	if err := tt.Click("高级"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("扩展控件") {
		t.Errorf("extension controls missing after opening 高级: %q", tt.Texts())
	}
}

func TestNativeSettingsWirelessAndErrors(t *testing.T) {
	useLanguage(t, i18n.ZhHans)
	s := newNativeSettingsState(nil, nil, nil, nil, nil)
	tt := ui.NewTester(s.view, 760, 1200)
	if err := tt.Click("无线调试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("显示配对二维码") || !tt.HasText("无法扫码？改用配对码") {
		t.Errorf("wireless step incomplete: %q", tt.Texts())
	}
	s.fail(msg("settings.failed.devices"), errors.New("adb: device unauthorized"))
	tt.Frame()
	if !tt.HasText("adb: device unauthorized") {
		t.Errorf("raw error not shown: %q", tt.Texts())
	}
	if !tt.HasText("无法读取设备列表。请在手机上点「允许 USB 调试」后重试") {
		t.Errorf("failure sentence with hint not shown: %q", tt.Texts())
	}
	if err := tt.Click("知道了"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.failure != nil {
		t.Error("error block should be dismissed")
	}
}

func TestNativeSettingsPackagePreference(t *testing.T) {
	useLanguage(t, i18n.ZhHans)
	s := newNativeSettingsState(nil, nil, nil, nil, nil)
	if s.packageName != defaultGamePackage {
		t.Fatalf("default package = %q", s.packageName)
	}
	if s.canPull() {
		t.Fatal("pull must need a ready device")
	}
	s.deviceLabels = []string{"Pixel · A · 已连接"}
	s.deviceIDs = map[string]string{"Pixel · A · 已连接": "A"}
	s.deviceStates = map[string]string{"Pixel · A · 已连接": "device"}
	s.selectedDevice, s.serial = s.deviceLabels[0], "A"
	if !s.canPull() {
		t.Fatal("ready device with default package should allow pull")
	}
}
