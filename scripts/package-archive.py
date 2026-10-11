"""Package a Windows or Linux build of MyGo as an archive that runs in place.

Usage: python3 scripts/package-archive.py <build dir> <archive> <top folder> <os> <name> <version>

Everything the app runs from in <build dir> (the executable, the engine and
the other resources, and on Linux the desktop entry and icon) goes into one
<top folder>, with licenses/ and README.txt. MyGo's own packages there (the
NSIS installer, the Debian package, the Linux archive and install.sh) are
left out. A .zip is written for Windows, a .tar.gz for Linux.
"""
import io
import os
import sys
import tarfile
import time
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LICENSES = ["LICENSE", "THIRD_PARTY_NOTICES.md", "ACKNOWLEDGEMENTS.md"]


def skipped(name, version):
    return (
        name.startswith(".")
        or name == "install.sh"
        or name.endswith(".deb")
        or name.endswith(".tar.gz")
        or name.endswith(f" Setup {version}.exe")
    )


def readme(os_name, name):
    if os_name == "windows":
        return f"""{name}

Run {name}.exe from this folder; keep the other files next to it. There is
no installer: to remove the app, delete the folder.

Requirements: Windows 10 or 11 (x64 or ARM64) with the Microsoft Edge
WebView2 Runtime, which Windows 10 and 11 include. If the app does not open,
install the Evergreen runtime from
https://developer.microsoft.com/microsoft-edge/webview2/

Where data is kept:
  settings  %APPDATA%\\{name}
  data      %LOCALAPPDATA%\\{name}\\Data
  cache     %LOCALAPPDATA%\\{name}\\Cache
  logs      %LOCALAPPDATA%\\{name}\\Logs
Portable mode: create a folder named "data" next to {name}.exe to keep
everything inside this folder instead.

USB devices connect through the official ADB compatibility mode (Platform-
Tools are downloaded on first use). Wireless debugging and direct TCP work too.
"""
    return f"""{name}

Run ./{name} from this folder; keep the other files next to it. There is no
installer: to remove the app, delete the folder.

Requirements: GTK 3 and WebKitGTK 4.1 (libwebkit2gtk-4.1.so.0).
  Debian/Ubuntu: sudo apt install libwebkit2gtk-4.1-0
  Fedora:        sudo dnf install webkit2gtk4.1
  Arch:          sudo pacman -S webkit2gtk-4.1
  openSUSE:      sudo zypper install libwebkit2gtk-4_1-0

To add it to the applications menu, copy {name}.desktop to
~/.local/share/applications/ and {name}.png to
~/.local/share/icons/hicolor/512x512/apps/, and put this folder's {name}
on your PATH (or change Exec= in the desktop entry to its full path).

Where data is kept:
  settings  $XDG_CONFIG_HOME/{name}  (~/.config/{name})
  data      $XDG_DATA_HOME/{name}    (~/.local/share/{name})
  cache     $XDG_CACHE_HOME/{name}   (~/.cache/{name})
  logs      $XDG_STATE_HOME/{name}   (~/.local/state/{name})
Portable mode: create a folder named "data" next to {name} to keep
everything inside this folder instead.

USB devices connect through the official ADB compatibility mode (Platform-
Tools are downloaded on first use; your user needs access to the device,
e.g. through the android-udev rules). Wireless debugging and direct TCP work
too.
"""


def entries(src, version):
    """Yields (archive path relative to the top folder, source path)."""
    for base, dirs, files in os.walk(src):
        dirs[:] = sorted(d for d in dirs if not d.startswith("."))
        for f in sorted(files):
            if base == src and skipped(f, version):
                continue
            path = os.path.join(base, f)
            yield os.path.relpath(path, src).replace(os.sep, "/"), path
    for f in LICENSES:
        yield "licenses/" + f, os.path.join(ROOT, f)


def main():
    src, dest, top, os_name, name, version = sys.argv[1:]
    note = readme(os_name, name)
    now = time.time()
    if os_name == "windows":
        with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            for rel, path in entries(src, version):
                z.write(path, f"{top}/{rel}")
            z.writestr(f"{top}/README.txt", note.replace("\n", "\r\n"))
        return
    with tarfile.open(dest, "w:gz", compresslevel=9) as t:
        def add_dir(rel):
            info = tarfile.TarInfo(rel)
            info.type, info.mode, info.mtime = tarfile.DIRTYPE, 0o755, now
            t.addfile(info)
        add_dir(top)
        made = {top}
        for rel, path in entries(src, version):
            parts = rel.split("/")[:-1]
            for i in range(1, len(parts) + 1):
                d = top + "/" + "/".join(parts[:i])
                if d not in made:
                    add_dir(d)
                    made.add(d)
            info = t.gettarinfo(path, f"{top}/{rel}")
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            # Executables (the app, the engine) keep their mode; data files
            # are readable by everyone.
            info.mode = 0o755 if os.access(path, os.X_OK) else 0o644
            with open(path, "rb") as f:
                t.addfile(info, f)
        data = note.encode()
        info = tarfile.TarInfo(f"{top}/README.txt")
        info.size, info.mode, info.mtime = len(data), 0o644, now
        t.addfile(info, io.BytesIO(data))


if __name__ == "__main__":
    main()
