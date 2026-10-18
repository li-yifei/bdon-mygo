(() => {
  'use strict';
  if (location.origin !== 'https://bdon.moe' || window !== window.top) return;
  // Messages in the native interface language (window.__bdonMygoMessages),
  // with English fallbacks.
  const t = (key, fallback) => {
    const text = window.__bdonMygoMessages?.[key];
    return typeof text === 'string' ? text : fallback;
  };
  let patch = window.__ournotesEventPatch;
  // A successful native refresh replaces the launch snapshot on the next hydration.
  try {
    const refreshed = sessionStorage.getItem('ournotes-event-refresh');
    if (refreshed) { const cached = JSON.parse(refreshed); if (Date.parse(cached.generatedAt) >= (Date.parse(patch?.generatedAt) || 0)) patch = cached; }
  } catch { /* Keep the validated launch snapshot. */ }
  if (!patch?.master) return;
  const region = patch.region || 'jp';
  const events = patch.events || (patch.event ? [patch.event] : []);
  const rows = name => {
    const t = patch.master[name];
    return t ? t.rows.map(r => Object.fromEntries(t.columns.map((c, i) => [c, r[i]]))) : [];
  };
  const revision = v => String(v || '').split('/')[0].split('.').map(Number);
  const officialCurrent = version => {
    if (version === patch.version) return true;
    const a = revision(version), b = revision(patch.version);
    if (a.length !== b.length || a.length < 3 || [...a, ...b].some(x => !Number.isFinite(x))) return false;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return a[i] > b[i];
    // Same semantic revision with a different content hash is incomparable.
    return false;
  };
  const encode = v => Array.isArray(v) ? [1, v.map(encode)] : [0, v && typeof v === 'object' ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, encode(x)])) : v];
  const decode = ([t, v]) => t === 1 ? v.map(decode) : t === 0 && v && typeof v === 'object' ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, decode(x)])) : v;
  const text = id => patch.texts?.[id] || id || '';
  const char = id => (patch.characters || rows('MasterCharacter')).find(c => c._id === id);
  const date = value => value ? value + '+09:00' : '';
  window.ournotesEventOverride = {version: patch.version, ui: false, dataset: false, charts: []};
  // Cards bdon lists only on other servers have no artwork under this
  // server's path yet. Their artwork requests read another server's files.
  const ASSET_PATHS = {jp: 'jp/ja', tw: 'tw/zh-Hant', en: 'en/en', kr: 'kr/ko'};
  const artworkFallback = new Map();
  const artworkPattern = new RegExp('^https://assets\\.bdon\\.moe/' + (ASSET_PATHS[region] || '').replace('/', '\\/') + '/((?:MemberCard|SupportCard)/\\d+)/');
  const artworkURL = url => {
    if (typeof url !== 'string' || !artworkFallback.size) return url;
    const m = artworkPattern.exec(url), other = m && artworkFallback.get(m[1]);
    return other ? url.replace(ASSET_PATHS[region], ASSET_PATHS[other]) : url;
  };
  const srcDescriptor = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, 'src');
  Object.defineProperty(HTMLImageElement.prototype, 'src', {...srcDescriptor, set(value) { srcDescriptor.set.call(this, artworkURL(value)); }});
  const setAttribute = Element.prototype.setAttribute;
  Element.prototype.setAttribute = function(name, value) {
    return setAttribute.call(this, name, this instanceof HTMLImageElement && name === 'src' ? artworkURL(value) : value);
  };
  const pageFetch = window.fetch;
  window.fetch = function(input, init) {
    if (typeof input === 'string' || input instanceof URL) input = artworkURL(String(input));
    else if (input instanceof Request && artworkURL(input.url) !== input.url) input = new Request(artworkURL(input.url), input);
    return pageFetch.call(this, input, init);
  };
  const get = Element.prototype.getAttribute;
  Element.prototype.getAttribute = function(name) {
    const original = get.call(this, name);
    if (name !== 'props' || !original || this.localName !== 'astro-island' || !String(get.call(this, 'component-url')).includes('/DeckWorkspace.')) return original;
    try {
      const props = JSON.parse(original);
      const catalogues = decode(props.playerCatalogues);
      const catalogue = catalogues.find(c => c.server === region);
      if (officialCurrent(catalogue?.masterVersion)) {
        window.ournotesEventOverride.uiSource = 'official';
        return original;
      }
      let changed = false;
      const update = (key, value) => { props[key] = encode(value); changed = true; };
      const deckEvents = decode(props.deckEvents), serverEvents = deckEvents.find(e => e.server === region);
      const addedEvents = [];
      if (serverEvents) {
        for (const e of events) if (!serverEvents.events.some(x => x.id === e.id)) { serverEvents.events.push(e); addedEvents.push(e); }
        if (addedEvents.length) update('deckEvents', deckEvents);
      }
      if (catalogue?.profile?.events?.choices) {
        for (const e of addedEvents) {
          const row = rows('MasterEvent').find(r => r._id === e.id);
          if (!catalogue.profile.events.choices.some(c => String(c.id) === String(e.id))) {
            catalogue.profile.events.choices.push({id: String(e.id), label: {id: row?._nameTextId || '', japanese: e.name}, type: row?._eventType, startAt: row?._startAt, endAt: row?._endAt});
          }
        }
        if (addedEvents.length) update('playerCatalogues', catalogues);
      }
      const members = decode(props.members), bands = rows('MasterBand');
      const bandName = id => members.find(c => c.bandId === id)?.bandName || text(bands.find(b => b._id === id)?._nameTextID);
      const metadata = (row, snap) => {
        const ids = snap ? row._characterIDs : [row._characterID], c = char(ids[0]);
        if (!c) throw new Error(t('supplement.noCharacter', 'A game card lacks character metadata'));
        const power = {performancePower: row._performancePowerMax, technicPower: row._technicPowerMax, visualPower: row._visualPowerMax};
        const common = {id: row._id, assetId: row._assetID, rarity: row._rarity, cardType: row._cardType, ...power,
          totalPower: Object.values(power).reduce((a,b) => a+b, 0), startAt: date(row._startAt), servers: [region], serverVariants: {},
          bandId: c._bandID, bandName: bandName(c._bandID), title: text(snap ? row._descriptionTextID : row._subtitleTextID)};
        if (snap) Object.assign(common, {name: text(row._nameTextID), characterIds: ids, characters: ids.map(id => ({id, name: text(char(id)._nameTextID), color: char(id)._mainColorCode})),
          supportSkillId01: row._supportSkillId01, supportSkillId02: row._supportSkillId02, gekisouSupportSkillId01: row._gekisouSupportSkillId01, gekisouSupportSkillId02: row._gekisouSupportSkillId02, diaryText: ''});
        else Object.assign(common, {characterId: c._id, characterName: text(c._nameTextID), characterColor: c._mainColorCode, liveSkillId: row._liveSkillID, leaderSkillId: row._leaderSkillID, gekisouSkillId: row._gekisouSkillID,
          formationBackgroundKey: row._rarity === 1 ? bands.find(b => b._id === c._bandID)?._memberRarityRBackgroundAssetPath : `MemberCard/${row._assetID}/member_background[formation]`});
        common.searchText = [common.title, common.name || common.characterName, common.bandName, common.id].join(' ');
        return common;
      };
      const saves = decode(props.saveTables), save = saves.find(s => s.server === region);
      for (const [key, table, snap] of [['members', 'MasterMemberCard', false], ['snaps', 'MasterSupportCard', true]]) {
        const list = key === 'members' ? members : decode(props[key]);
        let additions = 0;
        for (const row of rows(table)) {
          const existing = list.find(x => x.id === row._id);
          if (!existing) { list.push(metadata(row, snap)); additions++; }
          else if (!existing.servers?.includes(region)) {
            const other = ['tw', 'en', 'kr'].find(r => existing.servers?.includes(r));
            if (other) artworkFallback.set(`${snap ? 'SupportCard' : 'MemberCard'}/${existing.assetId ?? row._assetID}`, other);
            existing.servers = [...(existing.servers || []), region];
            existing.serverVariants = {...existing.serverVariants, [region]: {startAt: date(row._startAt)}}; additions++;
          }
          if (save && save[key][row._id] === undefined) { save[key][row._id] = row[snap ? '_supportCardLevelGroup' : '_memberCardLevelGroup']; changed = true; }
        }
        if (additions) update(key, list);
      }
      if (save) props.saveTables = encode(saves);
      const songs = decode(props.songs), scores = rows('MasterLiveMusicScore');
      let addedSongs = 0;
      for (const music of rows('MasterLiveMusic')) {
        const existing = songs.find(s => s.id === music._id);
        if (existing) {
          if (!existing.servers?.includes(region)) { existing.servers = [...(existing.servers || []), region]; existing.serverVariants = {...existing.serverVariants, [region]: {startAt: date(music._startAt)}}; addedSongs++; }
          continue;
        }
        const template = songs.find(s => s.jacketAssetName && s.jacketUrl);
        if (!template) continue;
        const title = text(music._titleTextID), bandIds = music._bandIDs || [];
        songs.push({id: music._id, title, titleJa: title, jacketAssetName: music._jacketAssetName,
          jacketUrl: template.jacketUrl.split(template.jacketAssetName).join(music._jacketAssetName),
          startAt: date(music._startAt), servers: [region], serverVariants: {}, musicType: music._musicType,
          bandId: bandIds[0], bandIds, bandName: bandName(bandIds[0]), sortOrder: music._sortOrder, categoryIds: music._musicCategories || [],
          gekisouMissions: [music._gekisouMission1, music._gekisouMission2, music._gekisouMission3],
          composer: text(music._composerTextID), lyricist: text(music._lyricistTextID), arranger: text(music._arrangerTextID),
          searchText: [title, text(music._phoneticTextID), music._id].join(' '),
          vocalistIds: music._vocalCharacterIDs || [], vocalists: (music._vocalCharacterIDs || []).map(id => ({id, name: text(char(id)?._nameTextID)})),
          difficulties: ['easy','normal','hard','expert'].flatMap(difficulty => {const r = scores.find(s => s._id === music['_'+difficulty+'ID']); return r ? [{difficulty, scoreId:r._id, level:r._musicScoreLevel, displayLevel:r._musicScoreDisplayLevel, notesCount:r._fullComboCount, chartKey:r._musicScoreTextFileName}] : [];})});
        addedSongs++;
      }
      if (addedSongs) update('songs', songs);
      if (changed && !document.getElementById('ournotes-event-source')) {
        const notice = document.createElement('p'); notice.id = 'ournotes-event-source';
        notice.style.cssText = 'padding:10px 16px;margin:12px;border:1px solid #7292b6;border-radius:8px;font-size:13px';
        notice.textContent = t('supplement.supplemented', 'Content not yet listed was supplemented from the device\'s game data.'); this.before(notice);
      }
      window.ournotesEventOverride = {...window.ournotesEventOverride, ui: changed, uiSource: changed ? 'device' : 'official', eventIds: addedEvents.map(e => e.id), artworkFallback: Object.fromEntries(artworkFallback)};
      return changed ? JSON.stringify(props) : original;
    } catch (error) { window.ournotesEventOverride.error = String(error); return original; }
  };
  window.ournotesPatchDeckData = data => {
    if (data.provenance?.region !== region) return false;
    if (data.format !== 'nnnotes.deck-data/1') { window.ournotesEventOverride.error = 'unsupported-dataset-format'; return false; }
    let changed = false;
    const additions = {};
    if (!officialCurrent(data.provenance?.master?.version)) {
      // Fill absent IDs only. Official rows, including revised rules, always win.
      for (const [name, supplement] of Object.entries(patch.master)) {
        const target = data.master[name];
        if (!target || !target.columns.includes('_id') || !supplement.columns.includes('_id')) continue;
        const idIndex = target.columns.indexOf('_id'), sourceId = supplement.columns.indexOf('_id');
        const ids = new Set(target.rows.map(r => r[idIndex]));
        const indices = target.columns.map(c => supplement.columns.indexOf(c));
        if (indices.some(i => i < 0)) continue;
        for (const row of supplement.rows) if (!ids.has(row[sourceId])) {
          target.rows.push(indices.map(i => row[i])); ids.add(row[sourceId]); additions[name] = (additions[name] || 0) + 1; changed = true;
        }
      }
      if (changed) data.provenance.desktopEventOverride = {version: patch.version, region, addedRows: additions};
    }
    const added = [];
    for (const chart of patch.charts || []) {
      if (data.charts.some(c => c.scoreId === chart.scoreId)) continue;
      const score = data.master.MasterLiveMusicScore;
      if (!score?.rows.some(r => r[score.columns.indexOf('_id')] === chart.scoreId)) continue;
      data.charts.push(chart); added.push({scoreId: chart.scoreId, sha256: chart.asset.sha256}); changed = true;
    }
    if (added.length) { data.charts.sort((a,b) => a.scoreId - b.scoreId); data.provenance.desktopChartOverrides = {source: 'supplement', charts: added}; }
    window.ournotesMissingEventCharts = events.flatMap(e => e.challengeMusics).filter(m => !data.charts.some(c => Math.floor(c.scoreId / 100) === m.musicId)).map(m => m.id);
    window.ournotesEventOverride = {...window.ournotesEventOverride, dataset: changed, datasetSource: changed ? 'device' : 'official', addedRows: additions, charts: added.map(c => c.scoreId)};
    return changed;
  };
  // Generic dataset extension hooks consumed by shell/inject.js.
  window.ournotesDatasetExtension = {
    patchDeckData: window.ournotesPatchDeckData,
    checkRequest(request) {
      if (window.ournotesMissingEventCharts?.includes(request.goal?.challengeMusicId)) throw new Error(t('supplement.missingChart', 'The chart for this challenge song is not available yet; update the game data or choose another song'));
    },
  };
})();
