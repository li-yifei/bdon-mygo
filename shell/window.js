// Desktop chrome uses a hidden-inset title bar and native drag regions.
(() => {
  'use strict';
  if (location.origin !== 'https://bdon.moe' || window !== window.top) return;
  const install = () => {
    if (document.getElementById('ournotes-window-bar')) return;
    const style = document.createElement('style');
    style.textContent = `
      :root { --ournotes-chrome-height: max(var(--mygo-titlebar-height, 44px), 44px); }
      #ournotes-window-bar {
        position: fixed; inset: 0 0 auto; height: var(--ournotes-chrome-height);
        z-index: 100; display: flex; align-items: center; gap: 12px;
        /* mygo reports the room its window controls take on each side:
           macOS traffic lights on the left, Windows caption buttons on the right. */
        padding-left: calc(var(--mygo-titlebar-inset-left, 0px) + 12px);
        padding-right: calc(var(--mygo-titlebar-inset-right, 0px) + 12px);
        background: var(--mn-bg, #0f1a2e); color: var(--mn-text, #dce6f6);
        border-bottom: 1px solid var(--mn-border, #293b57);
        font: 12px system-ui, -apple-system, sans-serif;
        user-select: none; -webkit-user-select: none; --app-region: drag;
      }
      #ournotes-window-title { flex: 1; font-weight: 600; --app-region: drag; }
      #ournotes-window-settings {
        --app-region: no-drag; border: 1px solid var(--mn-border, #293b57);
        border-radius: 6px; background: transparent; color: inherit;
        padding: 4px 10px; cursor: pointer; font: inherit;
      }
      #ournotes-window-settings:hover { background: var(--mn-surface, #21324d); }
      #ournotes-window-settings:focus-visible { outline: 2px solid #78aef3; outline-offset: 2px; }
      header.fixed { top: calc(var(--ournotes-chrome-height) + 12px) !important; }
      .mn-desktop-sidebar {
        top: calc(var(--ournotes-chrome-height) + 96px) !important;
        height: calc(100dvh - var(--ournotes-chrome-height) - 120px) !important;
      }
      main#content { padding-top: calc(var(--ournotes-chrome-height) + max(var(--mn-header-bottom, 5rem), 9rem)) !important; }
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
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', install, {once: true});
  else install();
})();
