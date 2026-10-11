package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

var nativeSettingsWindow *mygo.Window

const (
	defaultGamePackage = "com.bushiroad.sirius"
	googleSDKTermsURL  = "https://developer.android.com/studio/terms"
	settingsLogLimit   = 30
	settingsLogShown   = 8

	// Action keys. Every key except actionEngine talks to the device and is
	// serialised; the algorithm switch only touches local files.
	actionDevices  = "devices"
	actionPackages = "packages"
	actionPull     = "pull"
	actionImport   = "import"
	actionQR       = "qr"
	actionPair     = "pair"
	actionRelease  = "release"
	actionEngine   = "engine"
	actionLayer    = "transport"

	connectUSB      = 0
	connectWireless = 1
)

// settingsLogEntry keeps its title as a message to render, so that the log
// follows a language change; details are raw text (adb output, paths).
type settingsLogEntry struct {
	at     time.Time
	kind   string // "ok", "error", "info"
	title  func() string
	detail string
}

// msg returns a title that renders key in the current language.
func msg(key string, kv ...any) func() string {
	return func() string { return i18n.T(key, kv...) }
}

type nativeSettingsState struct {
	win        *mygo.Window
	parent     *mygo.Window
	engines    *searchEngines
	adb        *adbService
	resources  *resourceService
	importSave func() error
	// toolsTermsRetry is set while the Android SDK License Agreement awaits
	// the user's answer; it re-runs the action that needed Platform-Tools.
	toolsTermsRetry func()

	// Committed service state, used to revert a radio when applying fails.
	engineMode, appliedEngine string
	mode, appliedMode         string
	address                   string

	serial, packageName   string
	pairAddress, pairCode string
	connectMethod         int

	// devices and packageList are the last scan results; the labels shown in
	// the selects derive from them (see relabel) and follow the language.
	devices         []adbDevice
	deviceLabels    []string
	deviceIDs       map[string]string
	deviceStates    map[string]string
	selectedDevice  string
	devicesScanned  bool
	packageList     []adbPackage
	packageLabels   []string
	packageIDs      map[string]string
	selectedPackage string
	packagesScanned bool
	lastImport      string

	// languageLabel is the selected option of the language select.
	languageLabel string
	// setLanguage applies and persists a language preference.
	setLanguage func(pref string) error
	stopI18n    func()

	// active maps an action key to its progress text.
	active map[string]string
	log    []settingsLogEntry
	// failure is the most recent error, shown as a block until dismissed or
	// until another action starts.
	failure *settingsLogEntry
	// toasts queued from background work, shown on the next build.
	toasts []ui.Toast

	advancedOpen, manualPairOpen, manualPackageOpen bool

	qr             *ui.Bitmap
	qrID, qrStatus string
	qrCancel       context.CancelFunc
	closed         chan struct{}
	closeOnce      sync.Once
}

// showNativeSettings uses MyGo's native GPU UI; the web page remains dedicated
// to bdon's card pool and recommendation workflow.
func showNativeSettings(parent *mygo.Window, adb *adbService, resources *resourceService, importSave func() error, engines *searchEngines) {
	if nativeSettingsWindow != nil {
		nativeSettingsWindow.Show()
		nativeSettingsWindow.Focus()
		return
	}
	state := newNativeSettingsState(parent, adb, resources, importSave, engines)
	state.win = mygo.NewWindow(mygo.WindowOptions{Title: settingsWindowTitle(), Parent: parent, Width: 760, Height: 820, MinWidth: 620, MinHeight: 540, StateKey: product.StateKeys.Settings, Content: ui.View(state.view)})
	// On Windows and Linux menu bars belong to windows and App.SetMenu would give
	// the settings window the main window's menu. An own empty menu shows none.
	if runtime.GOOS != "darwin" {
		state.win.SetMenu(mygo.NewMenu(nil))
	}
	nativeSettingsWindow = state.win
	win := state.win
	state.stopI18n = i18n.OnChange(func(i18n.Lang) {
		win.SetTitle(settingsWindowTitle())
		win.Update(state.relabel)
	})
	state.win.OnClosed(func() {
		state.closeOnce.Do(func() { close(state.closed) })
		if state.stopI18n != nil {
			state.stopI18n()
		}
		state.cancelQR()
		nativeSettingsWindow = nil
	})
	state.refresh()
}

func settingsWindowTitle() string {
	return i18n.T("settings.windowTitle", "name", product.Name)
}

func newNativeSettingsState(parent *mygo.Window, adb *adbService, resources *resourceService, importSave func() error, engines *searchEngines) *nativeSettingsState {
	s := &nativeSettingsState{parent: parent, engines: engines, adb: adb, resources: resources, importSave: importSave, packageName: defaultGamePackage, active: map[string]string{}, closed: make(chan struct{}), setLanguage: applyLanguagePreference}
	if engines != nil {
		s.engineMode = engines.current().Mode
	}
	if adb != nil {
		transport := adb.TransportStatus()
		s.mode, s.address = transport.Mode, transport.Address
	}
	if s.engineMode == "" {
		s.engineMode = "bundled"
	}
	if s.mode == "" {
		s.mode = "system"
	}
	s.appliedEngine, s.appliedMode = s.engineMode, s.mode
	if isolatedADB != nil {
		s.connectMethod = connectWireless
	}
	return s
}

func (s *nativeSettingsState) update(fn func()) {
	select {
	case <-s.closed:
		return
	default:
	}
	if s.win == nil {
		fn()
		return
	}
	s.win.Update(fn)
}

// ---------------------------------------------------------------------------
// Feedback: busy state, log, error block and toasts.

func (s *nativeSettingsState) busy(key string) bool { _, ok := s.active[key]; return ok }

// deviceBusy reports whether a device action is running. Device actions share
// one ADB connection, so they run one at a time.
func (s *nativeSettingsState) deviceBusy() bool {
	for key := range s.active {
		if key != actionEngine {
			return true
		}
	}
	return false
}

func (s *nativeSettingsState) record(kind string, title func() string, detail string) {
	entry := settingsLogEntry{at: time.Now(), kind: kind, title: title, detail: detail}
	s.log = append(s.log, entry)
	if len(s.log) > settingsLogLimit {
		s.log = s.log[len(s.log)-settingsLogLimit:]
	}
	if kind == "error" {
		s.failure = &entry
	}
}

func (s *nativeSettingsState) succeed(title func() string, detail string) {
	s.record("ok", title, detail)
	s.toasts = append(s.toasts, ui.Toast{Title: title(), Description: detail})
}

// fail records a failure. title is the short sentence of the action; the
// translated reason of a service error (an *i18n.Error) and a hint for
// common ADB failures follow it, and the untranslated rest of the error
// stays visible as the detail line.
func (s *nativeSettingsState) fail(title func() string, err error) {
	reason, raw := i18n.Describe(err)
	full := func() string {
		text := title()
		if r, _ := i18n.Describe(err); r != "" {
			text = i18n.T("settings.failure.withReason", "title", text, "reason", r)
		}
		if hint := settingsErrorHint(raw + " " + reason); hint != "" {
			text = i18n.T("settings.failure.withHint", "title", text, "hint", hint)
		}
		return text
	}
	s.record("error", full, raw)
	s.toasts = append(s.toasts, ui.Toast{Title: full(), Type: "error"})
}

// settingsErrorHint turns common ADB failures into an instruction. It
// matches the untranslated error text, so the hint does not depend on the
// language.
func settingsErrorHint(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "unauthorized"):
		return i18n.T("settings.hint.unauthorized")
	case strings.Contains(lower, "offline"):
		return i18n.T("settings.hint.offline")
	case strings.Contains(lower, "no devices") || strings.Contains(lower, "not found"):
		return i18n.T("settings.hint.noDevices")
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return i18n.T("settings.hint.timeout")
	case strings.Contains(lower, nativeBridgeNotReady):
		return i18n.T("settings.hint.bridgeNotReady")
	}
	return ""
}

// work runs network, USB and file operations away from the UI thread. The
// returned closure changes view state through Window.Update only. progress
// is the catalog key of the progress text and failure the catalog key of the
// short sentence shown above the raw error.
func (s *nativeSettingsState) work(key, progress, failure string, run func() (func(), error)) {
	if s.busy(key) || (key != actionEngine && s.deviceBusy()) {
		return
	}
	s.active[key] = progress
	s.failure = nil
	go func() {
		apply, err := run()
		s.update(func() {
			delete(s.active, key)
			if errors.Is(err, errPlatformToolsTermsRequired) {
				// Ask for the license agreement, then run the same action again.
				s.toolsTermsRetry = func() { s.work(key, progress, failure, run) }
				return
			}
			if err != nil {
				s.fail(msg(failure), err)
				return
			}
			if apply != nil {
				apply()
			}
		})
	}()
}

// ---------------------------------------------------------------------------
// Device and package discovery.

func deviceStateLabel(state string) string {
	switch state {
	case "device", "unauthorized", "offline", "usb":
		return i18n.T("device.state." + state)
	}
	return state
}

func deviceLabel(d adbDevice) string {
	label := d.Serial
	if d.Model != "" {
		label = d.Model + " · " + d.Serial
	}
	return label + " · " + deviceStateLabel(d.State)
}

func packageLabel(p adbPackage) string {
	label := gamePackageVariantLabel(p.Variant) + " · " + p.Package
	if p.HasFiles {
		label += " · " + i18n.T("package.hasSave")
	}
	return label
}

// relabel rebuilds the select options from the scan results in the current
// language, keeping the selected device and package.
func (s *nativeSettingsState) relabel() {
	serial, pkg := s.deviceIDs[s.selectedDevice], s.packageIDs[s.selectedPackage]
	s.deviceLabels, s.deviceIDs, s.deviceStates = nil, map[string]string{}, map[string]string{}
	s.selectedDevice = ""
	for _, d := range s.devices {
		label := deviceLabel(d)
		s.deviceLabels = append(s.deviceLabels, label)
		s.deviceIDs[label], s.deviceStates[label] = d.Serial, d.State
		if d.Serial == serial && s.selectedDevice == "" {
			s.selectedDevice = label
		}
	}
	if s.packageList == nil {
		return
	}
	s.packageLabels, s.packageIDs = nil, map[string]string{}
	s.selectedPackage = ""
	for _, p := range s.packageList {
		label := packageLabel(p)
		s.packageLabels = append(s.packageLabels, label)
		s.packageIDs[label] = p.Package
		if p.Package == pkg && s.selectedPackage == "" {
			s.selectedPackage = label
		}
	}
}

func (s *nativeSettingsState) deviceReady(state string) bool {
	return state == "device" || (s.mode == "direct-usb" && state == "usb")
}

func (s *nativeSettingsState) selectedDeviceState() string {
	return s.deviceStates[s.selectedDevice]
}

func (s *nativeSettingsState) refresh() {
	if s.adb == nil {
		return
	}
	s.work(actionDevices, "settings.progress.devices", "settings.failed.devices", func() (func(), error) {
		devices, err := s.adb.Devices()
		if err != nil {
			return nil, err
		}
		mode := s.adb.TransportStatus().Mode
		labels := []string{}
		ids := map[string]string{}
		states := map[string]string{}
		chosen := ""
		for _, d := range devices {
			label := deviceLabel(d)
			labels = append(labels, label)
			ids[label] = d.Serial
			states[label] = d.State
			if chosen == "" && (d.State == "device" || (mode == "direct-usb" && d.State == "usb")) {
				chosen = label
			}
		}
		if chosen == "" && len(labels) > 0 {
			chosen = labels[0]
		}
		return func() {
			previous := s.serial
			s.devices = devices
			s.deviceLabels, s.deviceIDs, s.deviceStates = labels, ids, states
			s.selectedDevice, s.serial = chosen, ids[chosen]
			s.devicesScanned = true
			switch {
			case len(labels) == 0:
				s.record("info", msg("settings.log.noDevices"), "")
			case !s.deviceReady(states[chosen]):
				s.record("info", msg("settings.log.devicesNotReady", "count", len(labels)), chosen)
			default:
				s.record("ok", msg("settings.log.devicesFound", "count", len(labels)), chosen)
			}
			if s.serial != previous || !s.packagesScanned {
				s.onDeviceChanged()
			}
		}, nil
	})
}

// onDeviceChanged clears the package list and scans the newly selected device
// when it is ready.
func (s *nativeSettingsState) onDeviceChanged() {
	s.serial = s.deviceIDs[s.selectedDevice]
	s.packageList, s.packageLabels, s.packageIDs, s.selectedPackage = nil, nil, nil, ""
	s.packagesScanned = false
	if s.serial != "" && s.deviceReady(s.selectedDeviceState()) {
		s.packages()
	}
}

func (s *nativeSettingsState) packages() {
	serial := s.serial
	s.work(actionPackages, "settings.progress.packages", "settings.failed.packages", func() (func(), error) {
		if serial == "" {
			return nil, i18n.New("settings.error.selectDevice")
		}
		packages, err := s.adb.Packages(serial)
		if err != nil {
			return nil, err
		}
		labels := []string{}
		ids := map[string]string{}
		chosen, rank := "", 0
		for _, p := range packages {
			label := packageLabel(p)
			labels = append(labels, label)
			ids[label] = p.Package
			// Prefer the default package with files, then any package with
			// files, then the default package, then the first one.
			r := 1
			switch {
			case p.Package == defaultGamePackage && p.HasFiles:
				r = 4
			case p.HasFiles:
				r = 3
			case p.Package == defaultGamePackage:
				r = 2
			}
			if r > rank {
				chosen, rank = label, r
			}
		}
		return func() {
			if serial != s.serial {
				return
			}
			s.packageList = packages
			if s.packageList == nil {
				s.packageList = []adbPackage{}
			}
			s.packageLabels, s.packageIDs, s.selectedPackage = labels, ids, chosen
			s.packagesScanned = true
			if chosen != "" {
				s.packageName = ids[chosen]
				s.record("ok", msg("settings.log.packagesFound", "count", len(labels)), s.packageName)
			} else {
				s.record("info", msg("settings.log.noPackages"), "")
			}
		}, nil
	})
}

// ---------------------------------------------------------------------------
// Save import.

func (s *nativeSettingsState) canPull() bool {
	return !s.deviceBusy() && s.serial != "" && s.deviceReady(s.selectedDeviceState()) && strings.TrimSpace(s.packageName) != ""
}

func (s *nativeSettingsState) pullAndImport() {
	if !s.canPull() {
		return
	}
	serial, pkg := s.serial, strings.TrimSpace(s.packageName)
	s.work(actionPull, "settings.progress.pull", "settings.failed.pull", func() (func(), error) {
		r, err := s.adb.Pull(serial, pkg)
		if err != nil {
			return nil, err
		}
		if s.importSave != nil {
			if err = s.importSave(); err != nil {
				return nil, i18n.Wrap(err, "settings.error.pulledImportFailed")
			}
		}
		return func() {
			s.lastImport = time.Now().Format("15:04")
			s.succeed(msg("settings.log.imported"), r.Message)
		}, nil
	})
}

func (s *nativeSettingsState) importExisting() {
	if s.importSave == nil {
		return
	}
	s.work(actionImport, "settings.progress.import", "settings.failed.import", func() (func(), error) {
		if err := s.importSave(); err != nil {
			return nil, err
		}
		return func() {
			s.lastImport = time.Now().Format("15:04")
			s.succeed(msg("settings.log.importedExisting"), "")
		}, nil
	})
}

// ---------------------------------------------------------------------------
// Wireless pairing.

func (s *nativeSettingsState) cancelQR() {
	if s.qrCancel != nil {
		s.qrCancel()
		s.qrCancel = nil
	}
	if id := s.qrID; id != "" && s.adb != nil {
		go s.adb.PairQRCancel(id)
	}
	s.qrID = ""
	s.qr = nil
	s.qrStatus = ""
}

func (s *nativeSettingsState) startQR() {
	s.cancelQR()
	s.work(actionQR, "settings.progress.qr", "settings.failed.qr", func() (func(), error) {
		r, err := s.adb.PairQR()
		if err != nil {
			return nil, err
		}
		png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.DataURL, "data:image/png;base64,"))
		if err != nil {
			s.adb.PairQRCancel(r.SessionID)
			return nil, err
		}
		bitmap, err := ui.DecodeBitmap(png)
		if err != nil {
			s.adb.PairQRCancel(r.SessionID)
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		select {
		case <-s.closed:
			cancel()
			s.adb.PairQRCancel(r.SessionID)
			return nil, i18n.New("settings.error.windowClosed")
		default:
		}
		return func() {
			s.qr = bitmap
			s.qrID = r.SessionID
			s.qrStatus = i18n.T("settings.qr.waiting")
			s.qrCancel = cancel
			s.record("info", msg("settings.log.qrCreated"), "")
			go s.pollQR(ctx, r.SessionID)
		}, nil
	})
}

func (s *nativeSettingsState) pollQR(ctx context.Context, id string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			s.adb.PairQRCancel(id)
			return
		case <-ticker.C:
			status, err := s.adb.PairQRStatus(id)
			if err != nil {
				s.update(func() {
					if s.qrID == id {
						s.fail(msg("settings.failed.qrInterrupted"), err)
						s.cancelQR()
					}
				})
				return
			}
			s.update(func() {
				if s.qrID == id && status.Message != "" {
					s.qrStatus = status.Message
				}
			})
			switch status.State {
			case "connected":
				transport, err := s.adb.SetTransport("system", "")
				s.update(func() {
					if s.qrID != id {
						return
					}
					if err != nil {
						s.fail(msg("settings.failed.pairedSwitch"), err)
						return
					}
					s.mode, s.appliedMode, s.address = transport.Mode, transport.Mode, transport.Address
					s.qrCancel, s.qrID, s.qr, s.qrStatus = nil, "", nil, ""
					s.succeed(msg("settings.log.qrConnected"), i18n.T("settings.log.refreshingDevices"))
					s.refresh()
				})
				return
			case "failed", "expired":
				s.update(func() {
					if s.qrID != id {
						return
					}
					s.fail(msg("settings.failed.qrIncomplete"), errors.New(status.Message))
					s.cancelQR()
				})
				return
			case "cancelled":
				return
			}
		}
	}
}

func (s *nativeSettingsState) pairWithCode() {
	address, code := strings.TrimSpace(s.pairAddress), strings.TrimSpace(s.pairCode)
	if address == "" || code == "" {
		s.fail(msg("settings.failed.pairMissing"), nil)
		return
	}
	s.pairCode = ""
	s.work(actionPair, "settings.progress.pair", "settings.failed.pair", func() (func(), error) {
		r, err := s.adb.Pair(address, code)
		if err != nil {
			return nil, err
		}
		transport, err := s.adb.SetTransport("system", "")
		if err != nil {
			return nil, i18n.Wrap(err, "settings.failed.pairedSwitch")
		}
		return func() {
			s.mode, s.appliedMode, s.address = transport.Mode, transport.Mode, transport.Address
			s.succeed(msg("settings.log.paired"), r.Message)
			s.refresh()
		}, nil
	})
}

// ---------------------------------------------------------------------------
// Advanced settings, applied as soon as a radio changes.

func engineModeLabel(mode string) string {
	if mode == "custom" {
		return i18n.T("engine.custom")
	}
	return i18n.T("engine.bundled")
}

func transportModeLabel(mode string) string {
	switch mode {
	case "direct-usb":
		return i18n.T("transport.directUSB")
	case "direct-tcp":
		return i18n.T("transport.directTCP")
	}
	return i18n.T("transport.system")
}

func (s *nativeSettingsState) applyEngine() {
	mode := s.engineMode
	if mode == s.appliedEngine || s.engines == nil {
		return
	}
	s.work(actionEngine, "settings.progress.engine", "settings.failed.engine", func() (func(), error) {
		config := searchEngineConfig{Mode: mode}
		if mode == "custom" {
			var err error
			config, err = s.engines.experimentalConfig()
			if err != nil {
				return nil, err
			}
		}
		if err := s.engines.selectEngine(config, true); err != nil {
			return nil, err
		}
		return func() {
			s.appliedEngine = mode
			s.succeed(func() string { return i18n.T("settings.log.engineSwitched", "engine", engineModeLabel(mode)) }, i18n.T("settings.log.pageReloaded"))
			s.parent.Page().Reload()
		}, nil
	})
}

func (s *nativeSettingsState) applyTransport() {
	mode, address := s.mode, strings.TrimSpace(s.address)
	if s.adb == nil {
		return
	}
	if mode == "direct-tcp" && address == "" {
		// Wait for the address; the field's Enter or the button applies it.
		return
	}
	s.work(actionLayer, "settings.progress.transport", "settings.failed.transport", func() (func(), error) {
		r, err := s.adb.SetTransport(mode, address)
		if err != nil {
			return nil, err
		}
		return func() {
			s.mode, s.appliedMode, s.address = r.Mode, r.Mode, r.Address
			mode := r.Mode
			s.succeed(func() string { return i18n.T("settings.log.transportSwitched", "transport", transportModeLabel(mode)) }, i18n.T("settings.log.refreshingDevices"))
			s.refresh()
		}, nil
	})
}

func (s *nativeSettingsState) releaseUSB() {
	serial := s.serial
	s.work(actionRelease, "settings.progress.release", "settings.failed.release", func() (func(), error) {
		if _, err := s.adb.ReleaseSystemDevice(serial); err != nil {
			return nil, err
		}
		return func() {
			s.succeed(msg("settings.log.released"), i18n.T("settings.log.releasedNext", "transport", transportModeLabel("direct-usb")))
		}, nil
	})
}

// revertFailedRadios puts a radio back on the committed value once its apply
// has failed or was refused. A TCP choice waiting for its address stays.
func (s *nativeSettingsState) revertFailedRadios() {
	if !s.busy(actionEngine) && s.engineMode != s.appliedEngine {
		s.engineMode = s.appliedEngine
	}
	if !s.busy(actionLayer) && s.mode != s.appliedMode && !(s.mode == "direct-tcp" && strings.TrimSpace(s.address) == "") {
		s.mode = s.appliedMode
	}
}

// ---------------------------------------------------------------------------
// View.

func (s *nativeSettingsState) view(c *ui.Context) {
	t := c.Theme()
	for _, toast := range s.toasts {
		c.AddToast(toast)
	}
	s.toasts = nil
	s.revertFailedRadios()

	c.OnShortcut(ui.Cmd, ui.KeyR, s.refresh)
	c.OnShortcut(ui.Cmd, ui.KeyEnter, s.pullAndImport)

	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(24).Gap(16).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).Gap(4).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, i18n.T("settings.heading")).FontSize(22).FontWeight(600)
						if isolatedADB != nil {
							ui.Badge(c, i18n.T("settings.isolatedBadge")).Background(t.Warning.Alpha(0.18))
						}
					})
					ui.Text(c, i18n.T("settings.intro")).TextColor(t.TextMuted).Wrap()
				})
				// Always visible: users look for the language before anything else.
				s.renderLanguageSelect(c)
			})
			s.renderFailure(c)
			s.renderToolsTerms(c)

			s.step(c, 1, i18n.T("settings.step.connect"), s.hasReadyDevice(), func() { s.renderConnect(c) })
			s.step(c, 2, i18n.T("settings.step.device"), s.serial != "" && s.deviceReady(s.selectedDeviceState()), func() { s.renderDevices(c) })
			s.step(c, 3, i18n.T("settings.step.package"), s.packagesScanned && strings.TrimSpace(s.packageName) != "", func() { s.renderPackages(c) })
			s.step(c, 4, i18n.T("settings.step.import"), s.lastImport != "", func() { s.renderImport(c) })

			s.renderLog(c)

			ui.Collapsible(c.Key("advanced"), i18n.T("settings.advanced"), &s.advancedOpen, func() {
				ui.Column(c).Gap(16).Padding(8, 0, 0, 0).FillWidth().Children(func() {
					s.renderAdvanced(c)
				})
			})
		})
	})
}

func (s *nativeSettingsState) hasReadyDevice() bool {
	for _, state := range s.deviceStates {
		if s.deviceReady(state) {
			return true
		}
	}
	return false
}

// step draws one numbered card of the guided flow.
func (s *nativeSettingsState) step(c *ui.Context, n int, title string, done bool, body func()) {
	t := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("step-%d", n))).FillWidth().Gap(10).Padding(16).Radius(10).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if done {
				ui.Badge(c, "✓").Background(t.Success).TextColor(t.AccentText).Label(i18n.T("settings.step.doneLabel", "n", n))
			} else {
				ui.Badge(c, fmt.Sprint(n)).Label(i18n.T("settings.step.label", "n", n))
			}
			ui.Text(c, title).FontSize(16).FontWeight(600)
		})
		body()
	})
}

// actionButton draws a button with a spinner and progress text while its
// action runs.
func (s *nativeSettingsState) actionButton(c *ui.Context, key, label string, primary, disabled bool, fn func()) ui.Element {
	var b ui.Element
	ui.Row(c.Key("action-" + key)).Gap(8).AlignItems(ui.Center).Children(func() {
		if primary {
			b = ui.PrimaryButton(c, label)
		} else {
			b = ui.Button(c, label)
		}
		b.Disabled(disabled).OnClick(fn)
		if s.busy(key) {
			progress := i18n.T(s.active[key])
			ui.Spinner(c).Label(progress)
			ui.Text(c, progress).FontSize(12).TextColor(c.Theme().TextMuted)
		}
	})
	return b
}

// renderToolsTerms asks for the Android SDK License Agreement before the app
// downloads Google's Platform-Tools for the ADB compatibility mode.
func (s *nativeSettingsState) renderToolsTerms(c *ui.Context) {
	if s.toolsTermsRetry == nil {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("tools-terms")).FillWidth().Gap(8).Padding(12, 14).Radius(8).Background(t.Accent.Alpha(0.10)).Border(1, t.Accent.Alpha(0.6)).Role(ui.RoleStatus).Children(func() {
		ui.Text(c, i18n.T("settings.tools.title")).FontWeight(600).Wrap()
		ui.Text(c, i18n.T("settings.tools.body")).Wrap()
		ui.Link(c, i18n.T("settings.tools.terms"), googleSDKTermsURL)
		ui.Row(c).Gap(10).Children(func() {
			ui.PrimaryButton(c, i18n.T("settings.tools.accept")).OnClick(func() {
				retry := s.toolsTermsRetry
				s.toolsTermsRetry = nil
				if err := acceptPlatformToolsTerms(); err != nil {
					s.fail(msg("settings.tools.saveFailed"), err)
					return
				}
				s.toasts = append(s.toasts, ui.Toast{Title: i18n.T("settings.tools.accepted")})
				retry()
			})
			ui.Button(c, i18n.T("settings.tools.decline")).OnClick(func() { s.toolsTermsRetry = nil })
		})
	})
}

func (s *nativeSettingsState) renderFailure(c *ui.Context) {
	if s.failure == nil {
		return
	}
	t := c.Theme()
	f := *s.failure
	ui.Row(c.Key("failure")).FillWidth().Gap(12).Padding(12, 14).Radius(8).Background(t.Danger.Alpha(0.10)).Border(1, t.Danger.Alpha(0.6)).Role(ui.RoleStatus).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(4).Children(func() {
			ui.Text(c, f.title()).FontWeight(600).TextColor(t.Danger).Wrap()
			if f.detail != "" {
				ui.Text(c, f.detail).FontSize(11).TextColor(t.TextMuted).Selectable().Wrap()
			}
		})
		ui.Button(c, i18n.T("settings.failure.dismiss")).OnClick(func() { s.failure = nil })
	})
}

func (s *nativeSettingsState) renderConnect(c *ui.Context) {
	t := c.Theme()
	ui.Segmented(c.Key("connect-method"), &s.connectMethod, i18n.T("settings.connect.usb"), i18n.T("settings.connect.wireless")).Label(i18n.T("settings.connect.method"))
	if s.connectMethod == connectUSB {
		ui.Text(c, i18n.T("settings.connect.usbHelp")).Wrap()
		ui.Row(c).Gap(4).Wrap().Children(func() {
			ui.Text(c, i18n.T("settings.connect.platformTools")).FontSize(12).TextColor(t.TextMuted)
			ui.Link(c, i18n.T("settings.connect.sdkTerms"), googleSDKTermsURL).FontSize(12)
		})
		return
	}
	s.renderWireless(c)
}

func (s *nativeSettingsState) renderWireless(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key("wireless")).Gap(8).FillWidth().Children(func() {
		ui.Text(c, i18n.T("settings.wireless.intro")).Wrap()
		for i, key := range []string{"settings.wireless.step1", "settings.wireless.step2", "settings.wireless.step3"} {
			ui.Text(c.Key(fmt.Sprint("hint-", i)), fmt.Sprintf("%d. %s", i+1, i18n.T(key))).Wrap()
		}
		if s.qr == nil {
			s.actionButton(c, actionQR, i18n.T("settings.wireless.showQR"), true, s.deviceBusy(), s.startQR)
		} else {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Padding(8).Radius(8).Background(ui.RGB(255, 255, 255)).Children(func() {
					ui.Image(c, s.qr).Size(220, 220).Label(i18n.T("settings.wireless.qrLabel"))
				})
				ui.Column(c).Gap(8).Grow(1).Shrink(1).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c).Label(i18n.T("settings.wireless.waiting"))
						ui.Text(c, s.qrStatus).Wrap()
					})
					ui.Text(c, i18n.T("settings.wireless.qrValidity")).FontSize(12).TextColor(t.TextMuted)
					ui.Button(c, i18n.T("settings.wireless.cancel")).OnClick(func() {
						s.cancelQR()
						s.record("info", msg("settings.log.qrCancelled"), "")
					})
				})
			})
		}
		ui.Collapsible(c.Key("manual-pair"), i18n.T("settings.wireless.manual"), &s.manualPairOpen, func() {
			ui.Column(c).Gap(8).FillWidth().Padding(4, 0, 0, 0).Children(func() {
				ui.Text(c, i18n.T("settings.wireless.manualHelp")).FontSize(12).TextColor(t.TextMuted).Wrap()
				ui.Row(c).Gap(8).FillWidth().Children(func() {
					ui.TextInput(c.Key("pair-address"), &s.pairAddress).Placeholder(i18n.T("settings.wireless.addressPlaceholder")).Label(i18n.T("settings.wireless.address")).Grow(1)
					ui.TextInput(c.Key("pair-code"), &s.pairCode).Placeholder(i18n.T("settings.wireless.code")).Label(i18n.T("settings.wireless.code")).Width(120).OnSubmit(s.pairWithCode)
				})
				s.actionButton(c, actionPair, i18n.T("settings.wireless.pair"), false, s.deviceBusy(), s.pairWithCode)
			})
		})
	})
}

func (s *nativeSettingsState) renderDevices(c *ui.Context) {
	t := c.Theme()
	if len(s.deviceLabels) == 0 {
		text := i18n.T("settings.devices.none")
		if !s.devicesScanned {
			text = i18n.T("settings.devices.searching")
		}
		ui.Text(c, text).TextColor(t.TextMuted).Wrap()
	} else {
		ui.Select(c.Key("device"), &s.selectedDevice, s.deviceLabels).Label(i18n.T("settings.devices.label")).FillWidth().Disabled(s.deviceBusy()).OnChange(s.onDeviceChanged)
		switch state := s.selectedDeviceState(); {
		case state == "unauthorized":
			ui.Text(c, i18n.T("settings.devices.unauthorized")).TextColor(t.Warning).Wrap()
		case state == "usb" && s.mode != "direct-usb":
			ui.Text(c, i18n.T("settings.devices.released", "transport", transportModeLabel("direct-usb"))).TextColor(t.Warning).Wrap()
		case state != "" && !s.deviceReady(state):
			ui.Text(c, i18n.T("settings.devices.notReady")).TextColor(t.Warning).Wrap()
		}
	}
	s.actionButton(c, actionDevices, i18n.T("settings.devices.refresh"), false, s.deviceBusy(), s.refresh).Tooltip("⌘R")
	ui.Text(c, i18n.T("settings.devices.transport", "transport", transportModeLabel(s.appliedMode))).FontSize(12).TextColor(t.TextMuted)
}

func (s *nativeSettingsState) renderPackages(c *ui.Context) {
	t := c.Theme()
	if s.busy(actionPackages) {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label(i18n.T("settings.progress.packages"))
			ui.Text(c, i18n.T("settings.progress.packages")).TextColor(t.TextMuted)
		})
	} else if len(s.packageLabels) > 0 {
		ui.Select(c.Key("package"), &s.selectedPackage, s.packageLabels).Label(i18n.T("settings.packages.label")).FillWidth().Disabled(s.deviceBusy()).OnChange(func() {
			s.packageName = s.packageIDs[s.selectedPackage]
		})
	} else if s.serial == "" {
		ui.Text(c, i18n.T("settings.packages.selectDevice")).TextColor(t.TextMuted).Wrap()
	} else if s.packagesScanned {
		ui.Text(c, i18n.T("settings.packages.none")).TextColor(t.TextMuted).Wrap()
	}
	manual := func() {
		ui.TextInput(c.Key("package-name"), &s.packageName).Placeholder(i18n.T("settings.packages.namePlaceholder", "example", defaultGamePackage)).Label(i18n.T("settings.packages.name")).FillWidth()
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		s.actionButton(c, actionPackages, i18n.T("settings.packages.rescan"), false, s.deviceBusy() || s.serial == "", s.packages)
	})
	if len(s.packageLabels) == 0 && s.packagesScanned {
		manual()
		return
	}
	ui.Collapsible(c.Key("manual-package"), i18n.T("settings.packages.manual"), &s.manualPackageOpen, manual)
}

func (s *nativeSettingsState) renderImport(c *ui.Context) {
	t := c.Theme()
	next := ""
	switch {
	case s.serial == "":
		next = i18n.T("settings.import.needDevice")
	case !s.deviceReady(s.selectedDeviceState()):
		next = i18n.T("settings.import.deviceNotReady")
	case strings.TrimSpace(s.packageName) == "":
		next = i18n.T("settings.import.needPackage")
	}
	ui.Text(c, i18n.T("settings.import.intro")).Wrap()
	ui.Row(c).Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
		s.actionButton(c, actionPull, i18n.T("settings.import.pull"), true, !s.canPull(), s.pullAndImport).Tooltip("⌘↩")
		s.actionButton(c, actionImport, i18n.T("settings.import.existing"), false, s.deviceBusy() || s.importSave == nil, s.importExisting).Tooltip(i18n.T("settings.import.existingTooltip"))
	})
	if next != "" {
		ui.Text(c, next).FontSize(12).TextColor(t.TextMuted)
	} else if s.lastImport != "" {
		ui.Text(c, i18n.T("settings.import.last", "time", s.lastImport)).FontSize(12).TextColor(t.Success)
	}
}

func (s *nativeSettingsState) renderLog(c *ui.Context) {
	if len(s.log) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("log")).FillWidth().Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.log.heading")).FontWeight(600).Grow(1)
			ui.Button(c, i18n.T("settings.log.clear")).OnClick(func() { s.log = nil })
		})
		shown := 0
		for i := len(s.log) - 1; i >= 0 && shown < settingsLogShown; i-- {
			e := s.log[i]
			shown++
			mark, color := "•", t.TextMuted
			switch e.kind {
			case "ok":
				mark, color = "✓", t.Success
			case "error":
				mark, color = "✕", t.Danger
			}
			ui.Row(c.Key(fmt.Sprint("log-", e.at.UnixNano(), "-", i))).Gap(8).FillWidth().Children(func() {
				ui.Text(c, mark).TextColor(color).Width(14)
				ui.Text(c, e.at.Format("15:04:05")).FontSize(12).TextColor(t.TextMuted)
				ui.Column(c).Grow(1).Shrink(1).Children(func() {
					title := ui.Text(c, e.title()).Wrap()
					if e.kind == "error" {
						title.TextColor(t.Danger)
					}
					if e.detail != "" {
						ui.Text(c, e.detail).FontSize(11).TextColor(t.TextMuted).Selectable().Wrap()
					}
				})
			})
		}
	})
}

// languageOptions returns the options of the language select, "follow the
// system" first, with the preference value of each.
func languageOptions() (labels []string, prefs map[string]string) {
	prefs = map[string]string{}
	system := i18n.T("settings.language.system", "lang", i18n.NativeName(i18n.SystemLanguage()))
	labels = append(labels, system)
	prefs[system] = i18n.System
	for _, lang := range []i18n.Lang{i18n.ZhHans, i18n.ZhHant, i18n.Ja, i18n.En} {
		name := i18n.NativeName(lang)
		labels = append(labels, name)
		prefs[name] = string(lang)
	}
	return labels, prefs
}

// changeLanguage applies the preference chosen in the language select.
func (s *nativeSettingsState) changeLanguage(pref string) {
	if s.setLanguage == nil {
		return
	}
	if err := s.setLanguage(pref); err != nil {
		s.fail(msg("settings.failed.language"), err)
		return
	}
	s.relabel()
	s.toasts = append(s.toasts, ui.Toast{Title: i18n.T("settings.language.changed"), Description: i18n.T("settings.language.changedDetail")})
}

func (s *nativeSettingsState) renderLanguageSelect(c *ui.Context) {
	labels, prefs := languageOptions()
	current := i18n.Preference()
	s.languageLabel = labels[0]
	for _, label := range labels {
		if prefs[label] == current {
			s.languageLabel = label
		}
	}
	ui.Select(c.Key("language"), &s.languageLabel, labels).Label(i18n.T("settings.language.title")).Width(200).Tooltip(i18n.T("settings.language.note")).OnChange(func() {
		s.changeLanguage(prefs[s.languageLabel])
	})
}

func (s *nativeSettingsState) renderAdvanced(c *ui.Context) {
	t := c.Theme()
	section := func(title, note string) {
		ui.Text(c, title).FontWeight(600)
		if note != "" {
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted).Wrap()
		}
	}

	ui.Column(c.Key("adv-engine")).Gap(8).FillWidth().Children(func() {
		section(i18n.T("settings.engine.title"), i18n.T("settings.engine.note"))
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.RadioGroup(c, func() {
				ui.Radio(c, &s.engineMode, "bundled", engineModeLabel("bundled")).Disabled(s.busy(actionEngine)).OnChange(s.applyEngine)
				ui.Radio(c, &s.engineMode, "custom", engineModeLabel("custom")).Disabled(s.busy(actionEngine)).OnChange(s.applyEngine)
			}).Row().Gap(16)
			if s.busy(actionEngine) {
				ui.Spinner(c).Label(i18n.T("settings.progress.engine"))
			}
		})
	})

	ui.Column(c.Key("adv-transport")).Gap(8).FillWidth().Children(func() {
		section(i18n.T("settings.transport.title"), i18n.T("settings.transport.note", "system", transportModeLabel("system")))
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.RadioGroup(c, func() {
				ui.Radio(c, &s.mode, "system", transportModeLabel("system")).Disabled(s.deviceBusy()).OnChange(s.applyTransport)
				if isolatedADB == nil {
					if directUSBAvailable {
						ui.Radio(c, &s.mode, "direct-usb", transportModeLabel("direct-usb")).Disabled(s.deviceBusy()).OnChange(s.applyTransport)
					}
					ui.Radio(c, &s.mode, "direct-tcp", transportModeLabel("direct-tcp")).Disabled(s.deviceBusy()).OnChange(s.applyTransport)
				}
			}).Row().Gap(16)
			if s.busy(actionLayer) {
				ui.Spinner(c).Label(i18n.T("settings.progress.transport"))
			}
		})
		if s.mode == "direct-tcp" {
			ui.Row(c).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("tcp-address"), &s.address).Placeholder(i18n.T("settings.transport.tcpPlaceholder")).Label(i18n.T("settings.transport.tcpAddress")).Grow(1).OnSubmit(s.applyTransport)
				ui.Button(c, i18n.T("settings.transport.connect")).Disabled(s.deviceBusy() || strings.TrimSpace(s.address) == "").OnClick(s.applyTransport)
			})
		}
		if isolatedADB == nil && directUSBAvailable {
			ui.Text(c, i18n.T("settings.transport.releaseHelp", "transport", transportModeLabel("direct-usb"))).FontSize(12).TextColor(t.TextMuted).Wrap()
			s.actionButton(c, actionRelease, i18n.T("settings.transport.release"), false, s.deviceBusy() || s.serial == "", s.releaseUSB)
		}
	})

	s.renderPaths(c, section)

	if extensionControls != nil {
		extensionControls(s, c)
	}
}

// revealLabel names the file manager of this system.
func revealLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return i18n.T("settings.paths.reveal.darwin")
	case "windows":
		return i18n.T("settings.paths.reveal.windows")
	}
	return i18n.T("settings.paths.reveal.linux")
}

// renderPaths shows the config, data, cache and logs directories, each with
// a button that opens it in the file manager.
func (s *nativeSettingsState) renderPaths(c *ui.Context, section func(title, note string)) {
	t := c.Theme()
	dirs := currentAppDirs()
	ui.Column(c.Key("adv-paths")).Gap(8).FillWidth().Children(func() {
		section(i18n.T("settings.paths.title"), i18n.T("settings.paths.note"))
		if dirs.Portable {
			ui.Text(c, i18n.T("settings.paths.portable")).FontSize(12).TextColor(t.Warning).Wrap()
		}
		for _, row := range []struct{ key, path string }{
			{"config", dirs.Config}, {"data", dirs.Data}, {"cache", dirs.Cache}, {"logs", dirs.Logs},
		} {
			path := row.path
			ui.Row(c.Key("path-" + row.key)).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, i18n.T("settings.paths."+row.key)).FontWeight(600).Width(72)
				ui.Text(c, path).FontSize(12).TextColor(t.TextMuted).Selectable().Grow(1)
				ui.Button(c, revealLabel()).OnClick(func() { s.openFolder(path) })
			})
		}
	})
}

// openFolder creates dir when needed and opens it in the file manager.
func (s *nativeSettingsState) openFolder(dir string) {
	go func() {
		err := os.MkdirAll(dir, 0o700)
		if err == nil {
			err = mygo.Shell.OpenPath(dir)
		}
		if err != nil {
			s.update(func() { s.fail(msg("settings.failed.openPath"), err) })
		}
	}()
}
