package main

import "testing"

func TestADBValidation(t *testing.T) {
	for _, value := range []string{"", "a/b", "com example", "com.example/evil"} {
		if safePackage(value) {
			t.Errorf("safePackage(%q)=true", value)
		}
	}
	for _, value := range []string{"com.bushiroad.sirius", "com_bushiroad-1"} {
		if !safePackage(value) {
			t.Errorf("safePackage(%q)=false", value)
		}
	}
	for _, value := range []string{"0123456789abcdef", "AABBcc0011223344"} {
		if !hexAccount(value) {
			t.Errorf("hexAccount(%q)=false", value)
		}
	}
	for _, value := range []string{"short", "0123456789abcdeg", "../0123456789abcdef"} {
		if hexAccount(value) {
			t.Errorf("hexAccount(%q)=true", value)
		}
	}
}

func TestGamePackageFilter(t *testing.T) {
	for _, name := range []string{"com.bushiroad.sirius", "com.bushiroad.sirius.en", "com.bushiroad.sirius.global", "com.bushiroad.sirius.tw", "com.bilibili.sirius", "com.bilibili.sirius.official"} {
		if gamePackageVariant(name) == "" {
			t.Errorf("game package hidden: %s", name)
		}
	}
	for _, name := range []string{"com.android.settings", "com.google.chrome", "com.bushiroad.other", "com.bushiroad.siriusfake", "com.bilibili.siriusbackup", "com.bushiroad.sirius.evil/path"} {
		if gamePackageVariant(name) != "" {
			t.Errorf("unrelated package shown: %s", name)
		}
	}
}
