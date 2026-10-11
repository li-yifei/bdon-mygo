package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

func TestAppMenuTemplate(t *testing.T) {
	useLanguage(t, i18n.ZhHans)
	opened := 0
	items := appMenuTemplateFor("darwin", func() { opened++ })
	app := items[0]
	if app.Role != "appMenu" {
		t.Fatalf("first menu role = %q", app.Role)
	}
	var settings bool
	for _, it := range app.Submenu {
		if it.ID == "settings" {
			settings = true
			if it.Label != "设置…" || it.Accelerator != "CmdOrCtrl+," {
				t.Fatalf("settings item = %q %q", it.Label, it.Accelerator)
			}
			it.Click(it, nil)
		}
	}
	if !settings || opened != 1 {
		t.Fatal("app menu must open the settings window")
	}
	roles := []mygo.MenuRole{mygo.RoleAppMenu, mygo.RoleEditMenu, mygo.RoleViewMenu, mygo.RoleWindowMenu}
	if len(items) != len(roles) {
		t.Fatalf("top-level menus = %d", len(items))
	}
	for i, it := range items {
		if it.Role != roles[i] || it.Label == "" {
			t.Fatalf("top-level menu %d = %q %q", i, it.Role, it.Label)
		}
	}
	if items[1].Label != "编辑" {
		t.Fatalf("edit menu label = %q", items[1].Label)
	}
}

// TestAppMenuLanguages checks that every menu item carries a label in each
// language and that the settings item follows the language.
func TestAppMenuLanguages(t *testing.T) {
	want := map[i18n.Lang]string{i18n.En: "Settings…", i18n.ZhHans: "设置…", i18n.ZhHant: "設定…", i18n.Ja: "設定…"}
	for lang, settings := range want {
		useLanguage(t, lang)
		var walk func(items []*mygo.MenuItem)
		found := false
		walk = func(items []*mygo.MenuItem) {
			for _, it := range items {
				if it.Type == mygo.MenuItemSeparator || (it.Label == "" && it.Role == "" && it.Submenu == nil) {
					continue
				}
				if it.Label == "" || strings.HasPrefix(it.Label, "menu.") {
					t.Errorf("%s: menu item %q has no label", lang, it.Role)
				}
				if it.ID == "settings" {
					found = it.Label == settings
				}
				walk(it.Submenu)
			}
		}
		walk(appMenuTemplateFor("darwin", func() {}))
		if !found {
			t.Errorf("%s: settings item should read %q", lang, settings)
		}
	}
}

// On Linux and Windows the application menu role is macOS-only and MyGo
// drops it: settings must stay reachable from a plain first menu.
func TestAppMenuTemplateOtherPlatforms(t *testing.T) {
	useLanguage(t, i18n.En)
	for _, goos := range []string{"linux", "windows"} {
		opened := 0
		items := appMenuTemplateFor(goos, func() { opened++ })
		app := items[0]
		if app.Role != "" || app.Label != product.Name {
			t.Fatalf("%s: first menu = %q %q", goos, app.Role, app.Label)
		}
		found := false
		for _, it := range app.Submenu {
			if it.Role == mygo.RoleAbout || it.Role == mygo.RoleServices || it.Role == mygo.RoleHide {
				t.Fatalf("%s: macOS-only item %q", goos, it.Role)
			}
			if it.ID == "settings" {
				found = true
				it.Click(it, nil)
			}
		}
		if !found || opened != 1 {
			t.Fatalf("%s: settings item missing", goos)
		}
	}
}
