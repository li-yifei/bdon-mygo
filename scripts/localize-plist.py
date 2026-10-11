"""Localize the bundle's Info.plist usage descriptions from the native catalogs.

Usage: python3 scripts/localize-plist.py <Info.plist> <Contents/Resources>

English goes into Info.plist itself; every language gets
Contents/Resources/<lang>.lproj/InfoPlist.strings, which macOS picks by the
user's language. The texts are the "plist.<Key>" entries of
internal/i18n/locales/<lang>.json.
"""
import json
import os
import plistlib
import sys

KEYS = ['NSLocalNetworkUsageDescription']
LANGUAGES = ['en', 'zh-Hans', 'zh-Hant', 'ja']


def quote(text):
    return '"' + text.replace('\\', '\\\\').replace('"', '\\"').replace('\n', '\\n') + '"'


def main(plist_path, resources):
    locales = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'internal', 'i18n', 'locales')
    catalogs = {}
    for lang in LANGUAGES:
        with open(os.path.join(locales, lang + '.json'), encoding='utf-8') as f:
            catalogs[lang] = json.load(f)
    with open(plist_path, 'rb') as f:
        info = plistlib.load(f)
    for key in KEYS:
        info[key] = catalogs['en']['plist.' + key]
    info.setdefault('CFBundleDevelopmentRegion', 'en')
    info['CFBundleLocalizations'] = LANGUAGES
    with open(plist_path, 'wb') as f:
        plistlib.dump(info, f)
    for lang in LANGUAGES:
        lproj = os.path.join(resources, lang + '.lproj')
        os.makedirs(lproj, exist_ok=True)
        with open(os.path.join(lproj, 'InfoPlist.strings'), 'w', encoding='utf-8') as f:
            for key in KEYS:
                f.write(f'{key} = {quote(catalogs[lang]["plist." + key])};\n')


if __name__ == '__main__':
    main(*sys.argv[1:3])
