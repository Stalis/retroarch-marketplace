import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateGame } from './game.js';

const game = { id: 'example', canonical_name: 'Example', display_name: '', platform_id: 'ps', playlist: '', discs: [{ entrypoint: 'Disc/game.dat', files: [{ source: 'input/game.dat', path: 'Disc/game.dat' }] }] };
const multi = { ...game, discs: [...game.discs, { entrypoint: 'Disc2/game.dat', files: [{ source: 'other.dat', path: 'Disc2/game.dat' }] }] };

test('accepts a valid game', () => {
  assert.equal(validateGame(game), '');
  assert.equal(validateGame({ ...game, display_name: undefined, playlist: undefined }), '');
});

test('rejects unsafe paths, duplicate destinations and missing entrypoints', () => {
  for (const path of ['', '../file', '/file', 'a//b', 'a/./b', 'a\\b', 'a:b', 'a\u0000b']) {
    for (const field of ['source', 'path']) {
      const candidate = { ...game, discs: [{ ...game.discs[0], files: [{ ...game.discs[0].files[0], [field]: path }] }] };
      assert.ok(validateGame(candidate), `${field}: ${JSON.stringify(path)}`);
    }
  }
  assert.ok(validateGame({ ...game, id: '../bad' }));
  assert.ok(validateGame({ ...game, id: 'a/b' }));
  assert.ok(validateGame({ ...game, platform_id: '' }));
  assert.ok(validateGame({ ...game, canonical_name: '' }));
  assert.ok(validateGame({ ...game, discs: [] }));
  assert.ok(validateGame({ ...game, discs: [{ ...game.discs[0], entrypoint: 'missing' }] }));
  assert.ok(validateGame({ ...game, discs: [{ ...game.discs[0], files: [...game.discs[0].files, game.discs[0].files[0]] }] }));
});

test('requires and validates a playlist for ordered multi-disc games', () => {
  assert.ok(validateGame(multi));
  assert.ok(validateGame({ ...multi, playlist: 'Disc/game.dat' }));
  assert.equal(validateGame({ ...multi, playlist: 'game.m3u', display_name: 'Пример' }), '');
  assert.ok(validateGame({ ...multi, playlist: '../game.m3u' }));
  assert.ok(validateGame({ ...multi, playlist: 'Disc/#bad', discs: [{ entrypoint: 'Disc/#game', files: [{ source: 'game', path: 'Disc/#game' }] }, multi.discs[1]] }));
});

test('localizes validation errors', () => {
  const unsafe = { ...game, discs: [{ ...game.discs[0], files: [{ source: '../secret', path: 'Disc/game.dat' }] }] };
  const duplicate = { ...game, discs: [{ ...game.discs[0], files: [...game.discs[0].files, game.discs[0].files[0]] }] };
  const cases = [
    [{ ...game, id: '../unsafe' }, 'ID должен быть безопасным компонентом пути.', 'ID must be a safe path component.'],
    [unsafe, 'Пути файлов должны быть безопасными относительными путями.', 'File paths must be safe relative paths.'],
    [duplicate, 'Пути файлов в игре не должны повторяться.', 'File paths within a game must not be duplicated.'],
    [multi, 'Для нескольких дисков укажите путь плейлиста.', 'Enter a playlist path for multiple discs.'],
  ];
  for (const [candidate, ru, en] of cases) {
    assert.equal(validateGame(candidate, 'ru'), ru);
    assert.equal(validateGame(candidate, 'en'), en);
    assert.equal(validateGame(candidate, 'unsupported'), ru);
    assert.equal(validateGame(candidate), ru);
  }
});
