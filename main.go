package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// frontendFS is a build-time fallback page. The app loads bdon directly;
// Mygo dev rebuilds Go UI and embedded preload scripts on source changes.
//
//go:embed frontend
var frontendFS embed.FS

//go:embed shell/inject.js
var shellPreload string

//go:embed shell/window.js
var windowPreload string

// bundledEngineModel is the upstream ournotes-deck commit whose model the
// pinned engine implements, for engines that do not report one themselves.
//
//go:embed docs/native-engine-model.txt
var bundledEngineModel string

type rustDeck struct {
	jobStart         func(*byte, *byte, *byte) *byte
	jobPoll          func(*byte) *byte
	jobCancel        func(*byte) *byte
	recommendAccount func(*byte, *byte, *byte) *byte
	datasetInfo      func(*byte) *byte
	capabilities     func() *byte
	stringLen        func(*byte) uintptr
	recommend        func(*byte, *byte, *byte) *byte
	summary          func(*byte, *byte) *byte
	free             func(*byte)
	// model is the upstream ournotes-deck commit whose model the engine
	// implements, or "" when unknown.
	model string
}

func loadRust(path string) (*rustDeck, error) {
	lib, err := openNativeLibrary(path)
	if err != nil {
		return nil, err
	}
	// Check ABI exports before registration so incompatible libraries return an
	// error instead of causing RegisterLibFunc to panic.
	for _, symbol := range []string{"ournotes_recommend_account_json", "ournotes_dataset_info_json", "ournotes_capabilities_json", "ournotes_string_len", "ournotes_free_string"} {
		if _, err := nativeSymbol(lib, symbol); err != nil {
			_ = closeNativeLibrary(lib)
			return nil, i18n.Wrap(err, "engine.error.missingSymbol", "symbol", symbol)
		}
	}
	api := &rustDeck{}
	purego.RegisterLibFunc(&api.recommendAccount, lib, "ournotes_recommend_account_json")
	purego.RegisterLibFunc(&api.datasetInfo, lib, "ournotes_dataset_info_json")
	purego.RegisterLibFunc(&api.capabilities, lib, "ournotes_capabilities_json")
	purego.RegisterLibFunc(&api.stringLen, lib, "ournotes_string_len")
	if _, err := nativeSymbol(lib, "ournotes_recommend_json"); err == nil {
		purego.RegisterLibFunc(&api.recommend, lib, "ournotes_recommend_json")
	}
	if _, err := nativeSymbol(lib, "ournotes_local_summary_json"); err == nil {
		purego.RegisterLibFunc(&api.summary, lib, "ournotes_local_summary_json")
	}
	purego.RegisterLibFunc(&api.free, lib, "ournotes_free_string")
	if _, err := nativeSymbol(lib, "ournotes_account_job_start_json"); err == nil {
		if _, err := nativeSymbol(lib, "ournotes_account_job_poll_json"); err == nil {
			if _, err := nativeSymbol(lib, "ournotes_account_job_cancel_json"); err == nil {
				purego.RegisterLibFunc(&api.jobStart, lib, "ournotes_account_job_start_json")
				purego.RegisterLibFunc(&api.jobPoll, lib, "ournotes_account_job_poll_json")
				purego.RegisterLibFunc(&api.jobCancel, lib, "ournotes_account_job_cancel_json")
			}
		}
	}
	var capabilities struct {
		ModelCommit string `json:"modelCommit"`
	}
	if json.Unmarshal([]byte(api.take(api.capabilities())), &capabilities) == nil {
		api.model = capabilities.ModelCommit
	}
	return api, nil
}

func cstring(s string) []byte { return append([]byte(s), 0) }

func (r *rustDeck) Recommend(dataPath, roster, request string) string {
	a, b, c := cstring(dataPath), cstring(roster), cstring(request)
	result := r.recommend(&a[0], &b[0], &c[0])
	if result == nil {
		return `{"error":"Rust FFI returned null"}`
	}
	defer r.free(result)
	bytes := unsafe.Slice(result, 1<<20)
	for i, b := range bytes {
		if b == 0 {
			return string(bytes[:i])
		}
	}
	return `{"error":"Rust FFI result exceeds 1 MiB"}`
}

func (r *rustDeck) Summary(manifestPath, roster string) string {
	a, b := cstring(manifestPath), cstring(roster)
	result := r.summary(&a[0], &b[0])
	if result == nil {
		return `{"error":"Rust FFI returned null"}`
	}
	defer r.free(result)
	bytes := unsafe.Slice(result, 1<<20)
	for i, b := range bytes {
		if b == 0 {
			return string(bytes[:i])
		}
	}
	return `{"error":"Rust FFI result exceeds 1 MiB"}`
}

type deckService struct{ rust *rustDeck }

func (s deckService) Recommend(dataPath, roster, request string) string {
	if !json.Valid([]byte(roster)) || !json.Valid([]byte(request)) {
		return `{"error":"invalid JSON input"}`
	}
	return s.rust.Recommend(dataPath, roster, request)
}

func (s deckService) Summary(manifestPath, roster string) string {
	if roster != "" && !json.Valid([]byte(roster)) {
		return `{"error":"invalid roster JSON"}`
	}
	return s.rust.Summary(manifestPath, roster)
}

type resourceStatus struct {
	Path        string `json:"path"`
	Exists      bool   `json:"exists"`
	IsDirectory bool   `json:"isDirectory"`
	Assets      bool   `json:"assets"`
	Data        bool   `json:"data"`
	DeckData    bool   `json:"deckData"`
	SaveCache   bool   `json:"saveCache"`
	Message     string `json:"message"`
}

// assetRef is the only asset shape exposed to the WebView.  The path points at
// the user's staged files; URL is convenient for an <img src> or CSS url().
type assetRef struct {
	Found    bool   `json:"found"`
	Key      string `json:"key"`
	Relative string `json:"relative"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	Message  string `json:"message,omitempty"`
}

type resourceService struct {
	mu   sync.RWMutex
	path string
}

// nativeEngineName is the file name of the bundled engine on this platform.
func nativeEngineName() string { return "libournotes_ffi" + nativeLibrarySuffix() }

// defaultFFILib finds the bundled engine: OURNOTES_FFI_LIB, else the app's
// resources (Contents/Resources on macOS, next to the executable elsewhere,
// where mygo build installs resources/<os>-<arch>/), else next to the
// executable, else the working directory.
func defaultFFILib() string {
	if p := os.Getenv("OURNOTES_FFI_LIB"); p != "" {
		return p
	}
	for _, dir := range nativeLibraryDirs() {
		if p := filepath.Join(dir, nativeEngineName()); isFile(p) {
			return p
		}
	}
	return ""
}

// nativeLibraryDirs lists where shipped native libraries are looked up, in
// order: the app's resources, the executable's directory, the working
// directory.
func nativeLibraryDirs() []string {
	var dirs []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		dir = filepath.Clean(dir)
		for _, d := range dirs {
			if d == dir {
				return
			}
		}
		dirs = append(dirs, dir)
	}
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		add(dir)
		// Under go run and go test the resources are the project's
		// directory as it is, platform directories included.
		if platform := filepath.Join(dir, runtime.GOOS+"-"+runtime.GOARCH); isDir(platform) {
			add(platform)
		}
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
	}
	return dirs
}

func nativeLibrarySuffix() string {
	switch runtime.GOOS {
	case "windows":
		return ".dll"
	case "darwin":
		return ".dylib"
	}
	return ".so"
}

func inspectResourceDir(path string) resourceStatus {
	status := resourceStatus{Path: filepath.Clean(path)}
	if status.Path == "." || status.Path == "" {
		status.Message = i18n.T("resources.choose")
		return status
	}
	info, err := os.Stat(status.Path)
	if err != nil {
		status.Message = i18n.T("resources.missing", "error", err.Error())
		return status
	}
	status.Exists, status.IsDirectory = true, info.IsDir()
	if !status.IsDirectory {
		status.Message = i18n.T("resources.notDirectory")
		return status
	}
	status.Assets = isDir(filepath.Join(status.Path, "assets"))
	status.Data = isDir(filepath.Join(status.Path, "data"))
	status.DeckData = isFile(filepath.Join(status.Path, "nnnotes.deck-data", "1")) || isFile(filepath.Join(status.Path, "data", "nnnotes.deck-data", "1"))
	status.SaveCache = isDir(filepath.Join(status.Path, "android", "account")) && isDir(filepath.Join(status.Path, "android", "Master"))
	status.Message = i18n.T("resources.ready")
	if status.SaveCache {
		status.Message = i18n.T("resources.complete")
	}
	if !status.Assets && !status.Data && !status.DeckData {
		status.Message = i18n.T("resources.empty")
	}
	return status
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (s *resourceService) Status() resourceStatus {
	s.mu.RLock()
	path := s.path
	s.mu.RUnlock()
	return inspectResourceDir(path)
}

func (s *resourceService) SuggestedDataPath() string {
	s.mu.RLock()
	path := s.path
	s.mu.RUnlock()
	candidates := []string{
		filepath.Join(path, "nnnotes.deck-data", "1"),
		filepath.Join(path, "data", "nnnotes.deck-data", "1"),
		filepath.Join(path, "deck-data"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

func (s *resourceService) SetPath(path string) (resourceStatus, error) {
	path = filepath.Clean(path)
	status := inspectResourceDir(path)
	if !status.Exists || !status.IsDirectory {
		return status, fmt.Errorf("resource directory unavailable: %s", status.Message)
	}
	s.mu.Lock()
	s.path = path
	s.mu.Unlock()
	if err := persistResourceDir(path); err != nil {
		return status, i18n.Wrap(err, "resources.error.save")
	}
	return status, nil
}

// Asset resolves a staged asset key to a local file. Keys are relative to the
// manifest's assetsDir (usually assets/). Traversal and absolute paths are
// rejected so a page cannot escape the selected resource directory.
func (s *resourceService) Asset(key string) assetRef {
	s.mu.RLock()
	root := s.path
	s.mu.RUnlock()
	result := assetRef{Key: key}
	key = strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	if key == "" || strings.HasPrefix(key, "/") || filepath.IsAbs(key) {
		result.Message = "asset key must be a relative path"
		return result
	}
	assetsRoot := filepath.Join(root, "assets")
	if manifest, err := os.ReadFile(filepath.Join(root, "manifest.json")); err == nil {
		var m struct {
			AssetsDir string `json:"assetsDir"`
		}
		if json.Unmarshal(manifest, &m) == nil && m.AssetsDir != "" {
			assetsRoot = filepath.Join(root, filepath.FromSlash(m.AssetsDir))
		}
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	joined := filepath.Join(assetsRoot, clean)
	rel, err := filepath.Rel(assetsRoot, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		result.Message = "asset path escapes assets directory"
		return result
	}
	info, err := os.Stat(joined)
	if err != nil || info.IsDir() {
		result.Relative = filepath.ToSlash(rel)
		result.Message = "asset file not found"
		return result
	}
	result.Found = true
	result.Relative = filepath.ToSlash(rel)
	result.Path = joined
	result.URL = (&url.URL{Scheme: "file", Path: joined}).String()
	if strings.EqualFold(filepath.Ext(joined), ".png") {
		if data, err := os.ReadFile(joined); err == nil {
			result.URL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
		}
	}
	return result
}

func (s *resourceService) Pick(ctx context.Context) (resourceStatus, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       i18n.T("resources.dialog.title", "name", product.Name),
		Message:     i18n.T("resources.dialog.message"),
		Directory:   true,
		ButtonLabel: i18n.T("resources.dialog.button"),
	})
	if err != nil {
		return resourceStatus{}, err
	}
	if len(paths) == 0 {
		return s.Status(), nil
	}
	return s.SetPath(paths[0])
}

// appMenuTemplate builds the menu bar in the current language. The
// application menu keeps the macOS standard items and adds Settings… (Cmd+,)
// for the native settings window. Every standard item is spelled out with a
// translated label, since MyGo's role defaults are English; the menu is
// rebuilt when the language changes.
func appMenuTemplate(openSettings func()) []*mygo.MenuItem {
	return appMenuTemplateFor(runtime.GOOS, openSettings)
}

// appMenuTemplateFor builds the menu bar of goos. MyGo leaves the macOS-only
// application menu out on Linux and Windows, where menu bars belong to the
// windows, so there the app's first menu is a plain one holding Settings…
// and Quit.
func appMenuTemplateFor(goos string, openSettings func()) []*mygo.MenuItem {
	role := func(r mygo.MenuRole, key string) *mygo.MenuItem {
		return &mygo.MenuItem{Role: r, Label: i18n.T(key, "name", product.Name)}
	}
	sub := func(r mygo.MenuRole, key string, items ...*mygo.MenuItem) *mygo.MenuItem {
		it := role(r, key)
		it.Submenu = items
		return it
	}
	settings := &mygo.MenuItem{ID: "settings", Label: i18n.T("menu.settings"), Accelerator: "CmdOrCtrl+,", Click: func(_ *mygo.MenuItem, _ *mygo.Window) { openSettings() }}
	appMenu := sub(mygo.RoleAppMenu, "menu.app",
		role(mygo.RoleAbout, "menu.about"),
		mygo.Separator(),
		settings,
		mygo.Separator(),
		role(mygo.RoleServices, "menu.services"),
		mygo.Separator(),
		role(mygo.RoleHide, "menu.hide"),
		role(mygo.RoleHideOthers, "menu.hideOthers"),
		role(mygo.RoleUnhide, "menu.showAll"),
		mygo.Separator(),
		role(mygo.RoleQuit, "menu.quit"),
	)
	if goos != "darwin" {
		appMenu = &mygo.MenuItem{Label: i18n.T("menu.app", "name", product.Name), Submenu: []*mygo.MenuItem{
			settings,
			mygo.Separator(),
			role(mygo.RoleQuit, "menu.quit"),
		}}
	}
	editMenu := sub(mygo.RoleEditMenu, "menu.edit",
		role(mygo.RoleUndo, "menu.undo"),
		role(mygo.RoleRedo, "menu.redo"),
		mygo.Separator(),
		role(mygo.RoleCut, "menu.cut"),
		role(mygo.RoleCopy, "menu.copy"),
		role(mygo.RolePaste, "menu.paste"),
		role(mygo.RolePasteAndMatchStyle, "menu.pasteAndMatchStyle"),
		role(mygo.RoleDelete, "menu.delete"),
		role(mygo.RoleSelectAll, "menu.selectAll"),
	)
	if goos == "darwin" {
		// Speech holds macOS-only items; elsewhere it would be empty.
		editMenu.Submenu = append(editMenu.Submenu, mygo.Separator(), &mygo.MenuItem{Label: i18n.T("menu.speech"), Submenu: []*mygo.MenuItem{
			role(mygo.RoleStartSpeaking, "menu.startSpeaking"),
			role(mygo.RoleStopSpeaking, "menu.stopSpeaking"),
		}})
	}
	return []*mygo.MenuItem{
		appMenu,
		editMenu,
		sub(mygo.RoleViewMenu, "menu.view",
			role(mygo.RoleReload, "menu.reload"),
			role(mygo.RoleForceReload, "menu.forceReload"),
			role(mygo.RoleToggleDevTools, "menu.toggleDevTools"),
			mygo.Separator(),
			role(mygo.RoleResetZoom, "menu.actualSize"),
			role(mygo.RoleZoomIn, "menu.zoomIn"),
			role(mygo.RoleZoomOut, "menu.zoomOut"),
			mygo.Separator(),
			role(mygo.RoleToggleFullScreen, "menu.toggleFullScreen"),
		),
		sub(mygo.RoleWindowMenu, "menu.window",
			role(mygo.RoleMinimize, "menu.minimize"),
			role(mygo.RoleZoom, "menu.zoom"),
			mygo.Separator(),
			role(mygo.RoleFront, "menu.front"),
		),
	}
}

// nativeBridgeNotReady is thrown by the import script when the page has not
// installed the bridge yet; the settings window turns it into a hint.
const nativeBridgeNotReady = "native bridge not ready"

func main() {
	if err := configureADBIsolation(); err != nil {
		log.Fatal(err)
	}
	// mygo generate runs the app only to write the TypeScript client: it
	// touches no storage and calls no engine function.
	generating := os.Getenv("MYGO_GENERATE") != ""
	dirs := currentAppDirs()
	if !generating {
		if f, err := openLogFile(dirs.Logs); err != nil {
			log.Printf("log file unavailable: %v", err)
		} else {
			defer f.Close()
		}
	}
	lib := defaultFFILib()
	var rust *rustDeck
	if lib == "" && generating {
		rust = &rustDeck{}
	} else if lib == "" {
		log.Fatal(nativeEngineName() + " not found in the app resources, next to the executable or in the working directory; set OURNOTES_FFI_LIB or run scripts/fetch-native-engine.sh")
	}
	if rust == nil {
		log.Printf("native engine: %s", lib)
		var err error
		if rust, err = loadRust(lib); err != nil {
			log.Fatal(err)
		}
		if rust.model == "" {
			rust.model = strings.TrimSpace(bundledEngineModel)
		}
	}
	path := dirs.Data
	// Earlier releases kept storage inside the macOS bundle; other platforms
	// have no earlier release to migrate from.
	if !generating && !dirs.Development && runtime.GOOS == "darwin" {
		exe, _ := os.Executable()
		home, _ := os.UserHomeDir()
		if from, err := migrateLegacyStorage(path, legacyStorageDirs(exe, home)); err != nil {
			log.Printf("legacy storage migration failed: %v", err)
		} else if from != "" {
			log.Printf("migrated legacy storage from %s to %s", from, path)
		}
	}
	if !generating {
		prepareAppDirs(dirs)
		log.Printf("storage: config %s, data %s, cache %s, logs %s", dirs.Config, dirs.Data, dirs.Cache, dirs.Logs)
		if err := os.MkdirAll(path, 0700); err != nil {
			log.Fatal("creating app storage failed: ", err)
		}
	}
	resources := &resourceService{path: path}
	adb := &adbService{resources: resources}
	adb.loadADBTransport()
	native := &nativeDeck{rust: rust}
	engines := newSearchEngines(path, native, rust)
	engines.configRoot = dirs.Config
	if err := engines.restore(); err != nil {
		log.Printf("restoring the search engine failed, using the bundled one: %v", err)
	}
	mygo.Bind(native)
	mygo.App.SetName(product.Name)
	mygo.App.WhenReady(func() {
		// The language follows the stored preference, by default the system
		// language as macOS reports it (NSLocale.preferredLanguages).
		i18n.SystemLocale = mygo.App.Locale
		i18n.SetPreference(persistedLanguage())
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: product.Name, Width: 1280, Height: 860,
			MinWidth: 960, MinHeight: 640, StateKey: product.StateKeys.Main,
			TitleBarStyle:        mygo.TitleBarHiddenInset,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 15}, TitleBarHeight: 44,
			BackgroundColor: "#0f1a2e",
			URL:             "https://bdon.moe/tools/deck",
			Page:            mygo.PageOptions{TrustedOrigins: []string{"https://bdon.moe"}, PreloadScript: productPreload() + i18n.Preload() + supplementPreload(path) + extensionPreloadScript(path) + shellPreload + "\n" + windowPreload},
		})
		win.Page().OnPageTitleUpdated(func(e *mygo.TitleEvent) { e.PreventDefault() })
		importSave := func() error {
			resources.mu.RLock()
			root := resources.path
			resources.mu.RUnlock()
			text, id, server, err := readLocalPlayerSave(root)
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"playerText": text, "accountId": id, "server": server})
			_, err = win.Page().Eval("if (!window.ournotesNative) throw new Error('" + nativeBridgeNotReady + "'); return window.ournotesNative.importSave(" + string(payload) + ");")
			return err
		}
		native.openSettings = func() { showNativeSettings(win, adb, resources, importSave, engines) }
		setMenu := func() { mygo.App.SetMenu(mygo.NewMenu(appMenuTemplate(func() { native.openSettings() }))) }
		setMenu()
		i18n.OnChange(func(i18n.Lang) {
			setMenu()
			// Shell messages in the page switch now; the title bar button
			// follows after the page reloads.
			go func() { _, _ = win.Page().Eval(i18n.PageSwitchScript()) }()
		})
		win.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			u, err := url.Parse(e.URL)
			if err != nil || u.Scheme != "https" || u.Host != "bdon.moe" {
				e.PreventDefault()
				if e.UserInitiated && err == nil && (u.Scheme == "https" || u.Scheme == "http") {
					go mygo.Shell.OpenExternal(e.URL)
				}
			}
		})
		win.Center()
		win.Focus()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
