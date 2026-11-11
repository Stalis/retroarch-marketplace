import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { MockBackend } from '../mock-backend.js';

async function withServer(fn) {
  const backend = new MockBackend();
  const server = createServer((req, res) => backend.handle(req, res));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}/api/admin`;
  try { await fn(base); } finally { server.close(); }
}

const json = { 'Content-Type': 'application/json' };
const newGame = { id: 'new-game', canonical_name: 'New Game', platform_id: 'snes', discs: [{ entrypoint: 'g.sfc', files: [{ source: 'SNES/missing/g.sfc', path: 'g.sfc' }] }] };

test('lists games with availability, search and paging', () => withServer(async base => {
  const all = await (await fetch(`${base}/games`)).json();
  assert.deepEqual(all.map(game => game.id), ['kings-field', 'parasite-eve-ii', 'super-mario-world', 'unreleased-prototype']);
  assert.equal(all.find(game => game.id === 'unreleased-prototype').available, false);
  assert.deepEqual((await (await fetch(`${base}/games?q=PARASITE`)).json()).map(game => game.id), ['parasite-eve-ii']);
  assert.deepEqual((await (await fetch(`${base}/games?skip=1&count=2`)).json()).map(game => game.id), ['parasite-eve-ii', 'super-mario-world']);
  const known = await (await fetch(`${base}/known-platforms`)).json();
  assert.equal(known.length, 82);
}));

test('GET returns the game with an ETag; stale PUT is rejected with 412', () => withServer(async base => {
  const response = await fetch(`${base}/games/super-mario-world`);
  const etag = response.headers.get('ETag');
  assert.equal(etag, '"1"');
  const { game, available, missing } = await response.json();
  assert.equal(available, true);
  assert.deepEqual(missing, []);
  const put = (tag, body = game) => fetch(`${base}/games/super-mario-world`, { method: 'PUT', headers: { ...json, ...(tag ? { 'If-Match': tag } : {}) }, body: JSON.stringify(body) });
  const saved = await put(etag, { ...game, canonical_name: 'Renamed' });
  assert.equal(saved.status, 204);
  assert.equal(saved.headers.get('ETag'), '"2"');
  assert.equal((await put(etag)).status, 412);
  assert.equal((await put(null)).status, 412);
  assert.equal((await put('"2"', { ...game, id: 'other' })).status, 400);
  assert.equal((await (await fetch(`${base}/games/super-mario-world`)).json()).game.canonical_name, 'Renamed');
}));

test('POST with a missing source returns 422, then allow_missing=1 stores it unavailable', () => withServer(async base => {
  const post = query => fetch(`${base}/games${query}`, { method: 'POST', headers: json, body: JSON.stringify(newGame) });
  const rejected = await post('');
  assert.equal(rejected.status, 422);
  assert.deepEqual(await rejected.json(), { missing: [{ id: 'new-game', path: 'SNES/missing/g.sfc' }] });
  assert.equal((await fetch(`${base}/games/new-game`)).status, 404);
  const created = await post('?allow_missing=1');
  assert.equal(created.status, 201);
  assert.equal(created.headers.get('ETag'), '"1"');
  assert.equal((await post('?allow_missing=1')).status, 409);
  const stored = await (await fetch(`${base}/games/new-game`)).json();
  assert.equal(stored.available, false);
  const publicGames = await (await fetch(`${base.replace('/admin', '')}/games`)).json();
  assert.ok(!publicGames.some(game => game.id === 'new-game'));
}));

test('DELETE requires the current ETag', () => withServer(async base => {
  const del = tag => fetch(`${base}/games/kings-field`, { method: 'DELETE', headers: tag ? { 'If-Match': tag } : {} });
  assert.equal((await del('"9"')).status, 412);
  assert.equal((await del('"1"')).status, 204);
  assert.equal((await del('"1"')).status, 404);
}));

test('platforms: create, duplicate, delete in use and unused', () => withServer(async base => {
  const platforms = await (await fetch(`${base}/platforms`)).json();
  assert.equal(platforms.find(p => p.id === 'playstation').games, 2);
  const create = () => fetch(`${base}/platforms`, { method: 'POST', headers: json, body: JSON.stringify({ id: 'nes', name: 'NES', libretro_name: 'Nintendo - Nintendo Entertainment System' }) });
  assert.equal((await create()).status, 201);
  assert.equal((await create()).status, 409);
  const inUse = await fetch(`${base}/platforms/playstation`, { method: 'DELETE' });
  assert.equal(inUse.status, 409);
  assert.equal(await inUse.text(), 'platform has games');
  assert.equal((await fetch(`${base}/platforms/nes`, { method: 'DELETE' })).status, 204);
  assert.equal((await fetch(`${base}/platforms/nes`, { method: 'DELETE' })).status, 404);
}));

test('platform totals include unavailable games', () => withServer(async base => {
  const platforms = await (await fetch(`${base}/platforms`)).json();
  const genesis = platforms.find(platform => platform.id === 'genesis');
  assert.equal(genesis.games, 0);
  assert.equal(genesis.total_games, 1);
  assert.equal((await fetch(`${base}/platforms/genesis`, { method: 'DELETE' })).status, 409);
}));

test('BoxArt fixtures cover matches, no results and unavailable images', () => withServer(async base => {
  const platform = 'Sony - PlayStation';
  const lookup = query => fetch(`${base}/canonical-names?${new URLSearchParams({ platform, q: query })}`);
  const image = name => fetch(`${base}/mock/boxart?${new URLSearchParams({ platform, name })}`);
  const matches = await (await lookup('Parasite Eve II')).json();
  assert.ok(matches.includes('Parasite Eve II'));
  assert.ok(matches.includes('Parasite Eve II (Europe)'));
  assert.deepEqual(await (await lookup('Unknown game')).json(), []);
  const cover = await image('Parasite Eve II (Europe)');
  assert.equal(cover.headers.get('Content-Type'), 'image/png');
  assert.deepEqual([...new Uint8Array(await cover.arrayBuffer()).slice(0, 4)], [137, 80, 78, 71]);
  assert.equal((await image('Parasite Eve II (Demo)')).status, 404);
}));
