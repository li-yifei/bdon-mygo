// Desktop chrome uses a hidden-inset title bar and native drag regions.
(() => {
  'use strict';
  if (location.origin !== 'https://bdon.moe' || window !== window.top) return;
  const install = () => {
    if (document.getElementById('ournotes-window-bar')) return;
    const style = document.createElement('style');
    style.textContent = `
      /* The page offsets its fixed top app bar, sidebar and content by
         --md-app-bar-height (64px, or 104px with the narrow-screen breadcrumb
         row). Extend it by the window bar and move the app bar below it. */
      :root {
        --ournotes-chrome-height: max(var(--mygo-titlebar-height, 44px), 44px);
        --md-app-bar-height: calc(64px + var(--ournotes-chrome-height)) !important;
      }
      @media not all and (width >= 48rem) {
        :root:has(.mn-app-bar-trail) { --md-app-bar-height: calc(104px + var(--ournotes-chrome-height)) !important; }
      }
      .mn-top-app-bar { top: var(--ournotes-chrome-height) !important; }
      #ournotes-window-bar {
        position: fixed; inset: 0 0 auto; height: var(--ournotes-chrome-height);
        /* Above the MUI app bar (1100), below drawers and dialogs (1200+). */
        z-index: 1150; display: flex; align-items: center; gap: 12px;
        /* mygo reports the room its window controls take on each side:
           macOS traffic lights on the left, Windows caption buttons on the right. */
        padding-left: calc(var(--mygo-titlebar-inset-left, 0px) + 12px);
        padding-right: calc(var(--mygo-titlebar-inset-right, 0px) + 12px);
        /* --ournotes-bar-bg/fg mirror the page's app bar (see syncColors), so
           the two read as one bar in every theme. */
        background: var(--ournotes-bar-bg, var(--mui-palette-background-default, #121318));
        color: var(--ournotes-bar-fg, var(--mui-palette-text-primary, #e2e2e9));
        transition: background-color 200ms, color 200ms;
        font: 12px system-ui, -apple-system, sans-serif;
        user-select: none; -webkit-user-select: none; --app-region: drag;
      }
      #ournotes-window-title { flex: 1; font-weight: 600; --app-region: drag; }
      #ournotes-window-settings {
        --app-region: no-drag; border: 1px solid color-mix(in srgb, currentColor 25%, transparent);
        border-radius: 6px; background: transparent; color: inherit;
        padding: 4px 10px; cursor: pointer; font: inherit;
      }
      #ournotes-window-settings:hover { background: color-mix(in srgb, currentColor 8%, transparent); }
      #ournotes-window-settings:focus-visible { outline: 2px solid var(--mui-palette-primary-main, #afc6ff); outline-offset: 2px; }
    `;
    document.head.append(style);
    const bar = document.createElement('div');
    bar.id = 'ournotes-window-bar';
    const title = document.createElement('span');
    title.id = 'ournotes-window-title';
    title.textContent = window.__ournotesProduct?.name || document.title;
    const settings = document.createElement('button');
    settings.id = 'ournotes-window-settings';
    settings.type = 'button';
    const messages = window.__bdonMygoMessages || {};
    settings.textContent = messages.settingsButton || 'Local settings';
    settings.title = messages.settingsTooltip || 'ADB, saves and the experimental engine';
    settings.addEventListener('click', () => {
      window.mygo.call('nativeDeck.OpenSettings').catch(error => {
        window.ournotesNative.lastError = String(error);
      });
    });
    bar.append(title, settings);
    document.body.append(bar);
    // Copy the app bar's computed colors: the page switches themes through
    // attributes on <html>, the system scheme, and rerenders of the app bar.
    let pending = false;
    const syncColors = () => {
      pending = false;
      const appBar = document.querySelector('.mn-top-app-bar');
      if (!appBar) return;
      const {backgroundColor, color} = getComputedStyle(appBar);
      // Writing only changes keeps the body observer from seeing its own update.
      const set = (name, value) => { if (value && bar.style.getPropertyValue(name) !== value) bar.style.setProperty(name, value); };
      if (backgroundColor !== 'rgba(0, 0, 0, 0)') set('--ournotes-bar-bg', backgroundColor);
      set('--ournotes-bar-fg', color);
    };
    const scheduleSync = () => {
      if (pending) return;
      pending = true;
      requestAnimationFrame(syncColors);
    };
    new MutationObserver(scheduleSync).observe(document.documentElement, {attributes: true});
    new MutationObserver(scheduleSync).observe(document.body, {attributes: true, childList: true, subtree: true});
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', scheduleSync);
    document.addEventListener('transitionend', event => {
      if (event.target instanceof Element && event.target.matches('.mn-top-app-bar')) scheduleSync();
    });
    syncColors();
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', install, {once: true});
  else install();
})();
