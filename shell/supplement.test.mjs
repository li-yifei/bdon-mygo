import {test} from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import fs from 'node:fs';
const script = fs.readFileSync(new URL('./supplement.js', import.meta.url), 'utf8');

const table = (columns, rows) => ({columns, rows});
// A supplement from a device at 1.0.0.700 with event 3, card 68 and the charts of song 100114.
const patch = {
  version: '1.0.0.700/b', region: 'jp', events: [{id: 3, name: 'E', challengeMusics: [{id: 7, musicId: 100114}]}],
  master: {
    MasterEvent: table(['_id', '_name'], [[2, 'old'], [3, 'new']]),
    MasterMemberCard: table(['_id', '_assetID'], [[67, 67], [68, 68]]),
    MasterLiveMusicScore: table(['_id'], [[10011400]]),
  },
  texts: {}, characters: [], charts: [{scoreId: 10011400, asset: {key: 'k', sha256: ''}, notes: {id: [1], op: [1], judgementType: [1], timeMs: [0]}}],
};

function load() {
  class Element { getAttribute() { return null; } setAttribute(name, value) { this[name] = value; } }
  class HTMLImageElement extends Element {}
  Object.defineProperty(HTMLImageElement.prototype, 'src', {configurable: true, get() { return this._src; }, set(v) { this._src = v; }});
  const context = {Element, HTMLImageElement, URL, Request, fetch: async url => url, location: {origin: 'https://bdon.moe'},
    sessionStorage: {getItem: () => null}, document: {getElementById: () => null}, __ournotesEventPatch: structuredClone(patch)};
  context.window = context; context.top = context;
  vm.createContext(context); vm.runInContext(script, context);
  return context;
}

// Published data at version, holding event 2 and card 67, with or without the chart.
const published = (version, chart = false) => ({
  format: 'nnnotes.deck-data/1', provenance: {region: 'jp', master: {version}},
  master: {
    MasterEvent: table(['_id', '_name'], [[2, 'official']]),
    MasterMemberCard: table(['_id', '_assetID'], [[67, 67]]),
    MasterLiveMusicScore: table(['_id'], [[10011400]]),
  },
  charts: chart ? [{scoreId: 10011400, official: true}] : [],
});

test('a device ahead of bdon.moe fills only what the published data lacks', () => {
  const {ournotesDatasetExtension} = load();
  const data = published('1.0.0.600/a');
  assert.equal(ournotesDatasetExtension.patchDeckData(data), true);
  assert.deepEqual(data.master.MasterEvent.rows, [[2, 'official'], [3, 'new']]);
  assert.deepEqual(data.master.MasterMemberCard.rows, [[67, 67], [68, 68]]);
  assert.deepEqual(data.charts.map(c => c.scoreId), [10011400]);
});

test('the supplement steps back once bdon.moe has the data', () => {
  const {ournotesDatasetExtension} = load();
  const data = published('1.0.0.700/b', true), before = structuredClone(data);
  assert.equal(ournotesDatasetExtension.patchDeckData(data), false);
  assert.deepEqual(data, before);
});

test('a published chart always wins over a supplemented one', () => {
  const {ournotesDatasetExtension} = load();
  const data = published('1.0.0.600/a', true);
  ournotesDatasetExtension.patchDeckData(data);
  assert.equal(data.charts.length, 1);
  assert.equal(data.charts[0].official, true);
});

test('other servers pass through unchanged', () => {
  const {ournotesDatasetExtension} = load();
  const data = {...published('1.0.0.600/a'), provenance: {region: 'tw', master: {version: '1.0.0.600/a'}}}, before = structuredClone(data);
  assert.equal(ournotesDatasetExtension.patchDeckData(data), false);
  assert.deepEqual(data, before);
});
