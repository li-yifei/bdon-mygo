(() => {
  'use strict';
  if (location.origin !== 'https://bdon.moe' || window !== window.top) return;
  const call = (method, ...args) => window.mygo.call('nativeDeck.' + method, ...args);
  // Desktop shell messages in the native interface language, prepended by Go
  // as window.__bdonMygoMessages; the English literals are the fallback.
  const t = (key, fallback, params = {}) => {
    const messages = window.__bdonMygoMessages;
    const text = messages && typeof messages[key] === 'string' ? messages[key] : fallback;
    return text.replace(/\{(\w+)\}/g, (match, name) => (name in params ? String(params[name]) : match));
  };
  // The page checks availability before enabling a goal. Report the same view
  // of the verified (optionally extended) dataset as the original Worker.
  const record = value => !!value && typeof value === 'object' && !Array.isArray(value);
  const validId = value => Number.isSafeInteger(value) && value > 0;
  const DIFFICULTIES = [['easy', '_easyID'], ['normal', '_normalID'], ['hard', '_hardID'], ['expert', '_expertID']];
  /** LUCK presence in the three mission slots; null denotes incomplete mission data. */
  function hasLuck(row) {
    const missions = [row._gekisouMission1, row._gekisouMission2, row._gekisouMission3];
    return missions.includes(2) ? true : missions.every(mission => mission === 1 || mission === 3) ? false : null;
  }

  /** A small availability view of the verified data. The solver still receives the original bytes. */
  function dataCatalog(data) {
    function rows(name, required = []) {
      const table = data?.master?.[name];
      if (!record(table) || !Array.isArray(table.columns) || !Array.isArray(table.rows)
        || table.columns.some(column => typeof column !== 'string') || new Set(table.columns).size !== table.columns.length
        || required.some(column => !table.columns.includes(column))) return [];
      const byId = new Map();
      for (const row of table.rows) {
        if (!Array.isArray(row) || row.length !== table.columns.length) { if (required.length) return []; continue; }
        const value = Object.fromEntries(table.columns.map((column, index) => [column, row[index]]));
        if (!validId(value._id)) { if (required.length) return []; continue; }
        if (required.length && byId.has(value._id)) return [];
        byId.set(value._id, byId.has(value._id) ? null : value);
      }
      return [...byId.values()].filter(value => value !== null).sort((a, b) => a._id - b._id);
    }
    // Each grade must have exactly one guaranteed row across all resources in its event group.
    function rewardGroups(name) {
      const groups = new Map();
      for (const row of rows(name, ['_eventGroup', '_scoreRank', '_probability'])) {
        if (!validId(row._eventGroup)) continue;
        const group = groups.get(row._eventGroup) ?? { grades: new Set(), supported: true };
        group.supported &&= Number.isSafeInteger(row._scoreRank) && row._scoreRank >= 0
          && !group.grades.has(row._scoreRank) && row._probability === 10000;
        group.grades.add(row._scoreRank);
        groups.set(row._eventGroup, group);
      }
      return groups;
    }
    const normalRewards = rewardGroups('MasterLiveEventReward'), challengeRewards = rewardGroups('MasterChallengeLiveEventReward');
    const events = rows('MasterEvent');
    const scoreIds = new Set(Array.isArray(data?.charts) ? data.charts.filter(chart => record(chart) && validId(chart.scoreId)).map(chart => chart.scoreId) : []);
    return {
      eventIds: events.map(row => row._id),
      eventItemRewards: events.filter(row => validId(row._eventItemId)).map(row => ({
        id: row._id, itemId: row._eventItemId,
        normal: normalRewards.get(row._liveEventRewardGroup)?.supported === true,
        challenge: challengeRewards.get(row._challengeLiveEventRewardGroup)?.supported === true,
      })),
      musics: rows('MasterLiveMusic').map(row => ({ id: row._id, hasLuck: hasLuck(row),
        difficulties: DIFFICULTIES.filter(([, column]) => validId(row[column]) && scoreIds.has(row[column])).map(([difficulty]) => difficulty) })),
      challengeMusics: rows('MasterChallengeMusic').filter(row => validId(row._eventId) && validId(row._liveMusicId))
        .map(row => ({ id: row._id, eventId: row._eventId, musicId: row._liveMusicId })),
      arenaMusics: rows('MasterArenaMusic').filter(row => validId(row._liveMusicId))
        .map(row => ({ id: row._id, musicId: row._liveMusicId })),
    };
  }
  const OriginalWorker = window.Worker;
  const host = window.ournotesNative = {version: 1, engine: 'native-rust', jobs: 0, completed: 0, lastError: null};
  class NativeWorker extends EventTarget {
    constructor(workerArgs) { super(); this.workerArgs = workerArgs; this.delegate = null; this.onmessage = null; this.onerror = null; this.closed = false; this.ready = null; this.queue = Promise.resolve(); }
    emit(data) { if (this.closed) return; const e = new MessageEvent('message', {data}); this.onmessage?.(e); this.dispatchEvent(e); }
    postMessage(message) {
      if (this.closed) return;
      if (this.delegate && message.type !== 'run') { this.delegate.postMessage(message); return; }
      if (message.type === 'init') {
        this.ready = (async () => {
          if (message.protocol !== 'moenotes.deck-worker/1') throw new Error(t('protocolChanged', 'The page calculation protocol has changed; please update the desktop app'));
          const url = new URL(message.deckDataUrl, location.href);
          if (url.protocol !== 'https:' || !['storage.bdon.moe', 'assets.bdon.moe', 'bdon.moe'].includes(url.hostname)) throw new Error(t('unsupportedSource', 'Unsupported calculation data source'));
          const response = await fetch(url.href);
          if (!response.ok) throw new Error(t('downloadFailed', 'Calculation data download failed: {status}', {status: response.status}));
          let bytes = await response.arrayBuffer();
          if (bytes.byteLength !== message.deckDataBytes || bytes.byteLength > 64 * 1024 * 1024) throw new Error(t('sizeMismatch', 'Calculation data size check failed'));
          const digest = async bytes => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
          let sha = await digest(bytes);
          if (sha !== message.deckDataSha256) throw new Error(t('checksumMismatch', 'Calculation data checksum failed'));
          let text = new TextDecoder('utf-8', {fatal: true}).decode(bytes);
          const data = JSON.parse(text);
          // Optional extension hook: may extend the verified dataset in place.
          const patched = window.ournotesDatasetExtension?.patchDeckData?.(data) === true;
          if (patched) { text = JSON.stringify(data); bytes = new TextEncoder().encode(text); sha = await digest(bytes); }
          this.catalog = dataCatalog(data);
          this.nativeText = text;
          this.nativeSHA = sha;
          this.modelCommit = message.modelCommit || '';
          this.luckMusicIds = new Set(this.catalog.musics.filter(music => music.hasLuck === true).map(music => music.id));
          this.nativeReady = null;
          // The current page uses hasLuck as an unconditional UI support gate.
          // Advertise native support only after the exact original data is prepared;
          // keep the factual catalogue for routing and leave all mission bytes intact.
          let nativeMode = await call('UseNativeSolver');
          if (nativeMode || this.luckMusicIds.size) {
            try { await this.prepareNative(); }
            catch (error) {
              // An engine of another model than the page's keeps the page's
              // solver, so results match the site; LUCK support is optional.
              if (nativeMode && !String(error).includes('engine-model-mismatch')) throw error;
              host.lastFallbackError = String(error);
              nativeMode = false;
            }
          }
          if (!nativeMode) {
            if (this.closed) return;
            host.engine = 'bdon-original';
            this.delegate = Reflect.construct(OriginalWorker, this.workerArgs);
            this.delegate.onmessage = event => {
              if (event.data.type === 'ready' && this.nativeLuckSupported) {
                this.emit({...event.data, catalog: this.supportCatalog(event.data.catalog || this.catalog)});
              } else this.emit(event.data);
            };
            this.delegate.onerror = event => {
              if (this.closed) return;
              this.onerror?.(event);
              this.dispatchEvent(new ErrorEvent('error', {message: event.message, filename: event.filename, lineno: event.lineno, colno: event.colno}));
            };
            if (patched) {
              this.dataURL = URL.createObjectURL(new Blob([text], {type: 'application/json'}));
              this.delegate.postMessage({...message, deckDataUrl: this.dataURL, deckDataSha256: sha, deckDataBytes: bytes.byteLength});
            } else this.delegate.postMessage(message);
            return;
          }
          host.engine = 'native-rust';
          const ready = this.nativeReady;
          this.emit({type: 'ready', ...ready, catalog: this.supportCatalog(this.catalog)});
          return ready;
        })();
        this.ready.catch(error => { host.lastError = String(error); this.emit({type: 'failed', code: 'init', message: String(error)}); });
      } else if (message.type === 'run') {
        this.queue = this.queue.then(async () => {
          try {
            if (!this.ready) throw new Error(t('notInitialized', 'Native calculation is not initialized'));
            await this.ready;
            if (this.closed) return;
            const request = JSON.parse(message.requestJson);
            window.ournotesDatasetExtension?.checkRequest?.(request);
            if (this.delegate && !this.needsNativeLuck(request)) {
              host.engine = 'bdon-original';
              this.delegate.postMessage(message); return;
            }
            host.engine = this.delegate ? 'native-luck-fallback' : 'native-rust';
            host.jobs++;
            const token = await call('RecommendStart', this.datasetId, message.accountJson, message.requestJson);
            let resultJson;
            if (token) {
              this.jobToken = token;
              if (this.closed) { await call('RecommendCancel', token); this.jobToken = null; return; }
              let finished = false;
              try {
                while (!this.closed) {
                  const state = JSON.parse(await call('RecommendPoll', token));
                  if (state.error) throw new Error(state.error);
                  if (state.progressJson) this.emit({type: 'progress', jobId: message.jobId, inputRevision: message.inputRevision, resultJson: state.progressJson});
                  if (state.done) { finished = true; resultJson = state.resultJson; break; }
                  await new Promise(resolve => setTimeout(resolve, 150));
                }
              } finally {
                if (this.jobToken === token) {
                  this.jobToken = null;
                  if (!finished) await call('RecommendCancel', token);
                }
              }
              if (this.closed) return;
              if (typeof resultJson !== 'string') throw new Error(t('missingResult', 'Native calculation returned no result'));
            } else {
              resultJson = await call('Recommend', this.datasetId, message.accountJson, message.requestJson);
            }
            host.completed++;
            this.emit({type: 'result', jobId: message.jobId, inputRevision: message.inputRevision, resultJson});
          } catch (error) {
            host.lastError = String(error);
            this.emit({type: 'failed', jobId: message.jobId, inputRevision: message.inputRevision, code: 'runtime', message: String(error)});
          }
        });
      } else { this.emit({type: 'failed', code: 'protocol', message: t('unknownRequest', 'Unknown native calculation request')}); }
    }
    async prepareNative() {
      if (!this.nativeReady) {
        this.nativeReady = await call('Prepare', this.nativeText, this.nativeSHA, this.modelCommit);
        this.datasetId = this.nativeReady.datasetId;
        const capabilities = JSON.parse(this.nativeReady.capabilitiesJson || '{}');
        this.nativeLuckSupported = capabilities.luckMissions === true;
      }
      return this.nativeReady;
    }
    supportCatalog(catalog) {
      if (!this.nativeLuckSupported) return catalog;
      return {...catalog, musics: catalog.musics.map(music => this.luckMusicIds.has(music.id) ? {...music, hasLuck: false} : music)};
    }
    needsNativeLuck(request) {
      if (!this.nativeLuckSupported) return false;
      const goal = request.goal;
      if (goal?.kind === 'battleLive' || goal?.kind === 'missionLive') return this.luckMusicIds.has(goal.musicId);
      if (goal?.kind === 'arenaLive') {
        const music = this.catalog.arenaMusics.find(row => row.id === goal.arenaMusicId);
        return !!music && this.luckMusicIds.has(music.musicId);
      }
      return false;
    }
    terminate() {
      this.closed = true; this.onmessage = null; this.onerror = null;
      this.delegate?.terminate();
      if (this.dataURL) { URL.revokeObjectURL(this.dataURL); this.dataURL = null; }
      if (this.jobToken) { const token = this.jobToken; this.jobToken = null; call('RecommendCancel', token).catch(error => { host.lastError = String(error); }); }
    }
  }
  window.Worker = new Proxy(OriginalWorker, {construct(Target, args) {
    const url = new URL(String(args[0]), location.href);
    if (url.origin === location.origin && url.pathname === '/deck/deck-worker.js') return new NativeWorker(args);
    return Reflect.construct(Target, args);
  }});
  const openDB = (name, store, keyPath) => new Promise((resolve, reject) => {
    const req = indexedDB.open(name, 1);
    req.onupgradeneeded = () => { if (!req.result.objectStoreNames.contains(store)) req.result.createObjectStore(store, {keyPath}); };
    req.onsuccess = () => resolve(req.result); req.onerror = req.onblocked = () => reject(new Error(t('storageUnavailable', 'Page local storage is unavailable')));
  });
  host.importSave = async ({playerText, accountId, server}) => {
    if (!['jp', 'intl'].includes(server) || !/^\d+$/.test(accountId)) throw new Error(t('badSaveIdentity', 'Invalid save server or account ID'));
    const player = JSON.parse(playerText);
    if (!Array.isArray(player._memberCards)) throw new Error(t('noMemberCards', 'The save has no member cards'));
    const bytes = new TextEncoder().encode(playerText);
    const sha256 = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
    const at = Date.now();
    const saves = await openDB('moenotes-game-saves', 'saves', 'key');
    await new Promise((resolve, reject) => { const tx = saves.transaction('saves', 'readwrite'); tx.objectStore('saves').put({key: server + '/' + accountId, server, accountId, sha256, uploadedAt: at, bytes: bytes.buffer}); tx.oncomplete = resolve; tx.onabort = tx.onerror = () => reject(tx.error); });
    saves.close();
    const regions = server === 'jp' ? ['jp'] : ['tw', 'en', 'kr'];
    const boxes = await openDB('moenotes-card-box', 'boxes', 'server');
    for (const region of regions) {
      await new Promise((resolve, reject) => {
        const tx = boxes.transaction('boxes', 'readwrite'), store = tx.objectStore('boxes'), req = store.get(region);
        req.onsuccess = () => {
          const unknown = () => ({status: 'unknown', value: null, history: [], needsReview: false});
          const box = req.result || {format: 'moenotes.card-box/1', id: crypto.randomUUID(), server: region, revision: 0, updatedAt: at, coverage: {member: {complete: false, declaredAt: null}, snap: {complete: false, declaredAt: null}}, cards: [], baseline: null, save: null, player: {characterRanks: {}, characterCoverage: 'partial', characterTotalRank: unknown(), vipRank: unknown(), bandItems: {}, bandItemStates: {}, catalogFields: {}, catalogIdentity: null, bandItemsComplete: false, memory: unknown(), eventIds: unknown()}};
          box.save = {server, accountId, sha256, uploadedAt: at}; box.revision++; box.updatedAt = at; store.put(box);
        };
        tx.oncomplete = resolve; tx.onabort = tx.onerror = () => reject(tx.error);
      });
      const channel = new BroadcastChannel('moenotes-card-box'); channel.postMessage({sender: 'ournotes-native', change: {server: region, revision: null}}); channel.close();
    }
    boxes.close();
    const settings = JSON.parse(localStorage.getItem('moenotes:settings') || '{}');
    settings.gameServer = server === 'jp' ? 'jp' : 'tw';
    const settingsText = JSON.stringify(settings);
    localStorage.setItem('moenotes:settings', settingsText);
    window.dispatchEvent(new StorageEvent('storage', {key: 'moenotes:settings', newValue: settingsText}));
    host.lastImport = {server, members: player._memberCards.length, snaps: player._supportCards?.length || 0};
    return host.lastImport;
  };
  // Raw game saves stay in the WebView's local storage. Cloud mutation requires
  // a separate product decision; the shell intercepts these account endpoints.
  const originalFetch = window.fetch.bind(window);
  window.fetch = (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input), location.href);
    const method = (init?.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
    if (url.origin === location.origin && /^\/api\/me\/(boxes|saves)(\/|$)/.test(url.pathname) && !['GET', 'HEAD'].includes(method)) return Promise.reject(new Error(t('localSavesOnly', 'Desktop saves stay on this computer; please use the local card pool')));
    return originalFetch(input, init);
  };
})();
