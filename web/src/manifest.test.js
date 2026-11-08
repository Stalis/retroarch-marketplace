import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addGame, addPlatform, formatManifest, parseManifest, updateCanonicalName } from './manifest.js';
import { knownPlatforms } from './platforms.js';

test('canonical name edits preserve unrelated JSON text', () => {
  const text = '{"contents":[{"canonical_name":"old","large":9007199254740993}],"platforms":[]}';
  assert.equal(updateCanonicalName(text, 0, 'new'), text.replace('"old"', '"new"'));
});

test('selected canonical name leaves other games and fields unchanged', () => {
  const text = '{"contents":[{"canonical_name":"First","display_name":"Shown"},{"canonical_name":"Second","id":"second"}],"platforms":[{"id":"p"}]}';
  const result = updateCanonicalName(text, 1, 'Second (USA)');
  assert.deepEqual(JSON.parse(result), { contents: [{ canonical_name: 'First', display_name: 'Shown' }, { canonical_name: 'Second (USA)', id: 'second' }], platforms: [{ id: 'p' }] });
});

test('invalid JSON is reported and duplicate names are not silently rewritten', () => {
  assert.ok(parseManifest('{').error);
  const text = '{"contents":[{"canonical_name":"one","canonical_name":"two"}]}';
  assert.equal(updateCanonicalName(text, 0, 'other'), text);
});

test('formats JSON without losing values or duplicate keys', () => {
  const text = '{"contents":[{"name":"one","name":"two","large":9007199254740993}]}';
  const formatted = formatManifest(text);
  assert.match(formatted, /\n  "contents": \[/);
  assert.match(formatted, /"name": "one",\n\s+"name": "two"/);
  assert.match(formatted, /9007199254740993/);
  assert.equal(formatManifest('{'), '{');
});

const platform = { id: 'ps', name: 'PlayStation', libretro_name: 'Sony - PlayStation' };

test('inserts platforms without changing unrelated values or duplicate unknown keys', () => {
  const text = '{"large":9007199254740993,"extra":1,"extra":2,"platforms":[{"id":"old","name":"Old","libretro_name":"Old"}]}';
  const result = addPlatform(text, platform);
  assert.equal(result.error, '');
  assert.match(result.text, /"large"\s*:\s*9007199254740993/);
  assert.match(result.text, /"extra"\s*:\s*1\s*,\s*"extra"\s*:\s*2/);
  assert.deepEqual(JSON.parse(result.text).platforms, [{ id: 'old', name: 'Old', libretro_name: 'Old' }, platform]);
  assert.deepEqual(JSON.parse(addPlatform('{"contents":[]}', platform).text).platforms, [platform]);
});

test('rejects unsafe platform components and invalid names', () => {
  for (const value of ['', '.', '..', 'a/b', 'a\\b', 'a:b', 'a\u0000b', 'a\u001fb', 'a\u007fb']) {
    for (const key of ['id', 'libretro_name']) {
      const text = '{"platforms":[]}';
      const result = addPlatform(text, { ...platform, [key]: value });
      assert.ok(result.error, `${key}: ${JSON.stringify(value)}`);
      assert.equal(result.text, text);
    }
  }
  for (const name of ['', 'a\0b']) assert.ok(addPlatform('{}', { ...platform, name }).error);
});

test('rejects duplicate platforms and ambiguous or malformed manifests without changes', () => {
  const existing = addPlatform('{"platforms":[]}', platform).text;
  for (const [text, value] of [
    [existing, platform],
    [existing, { ...platform, libretro_name: 'Other' }],
    [existing, { ...platform, id: 'other' }],
    ['{"platforms":[],"platforms":[]}', platform],
    ['{"platforms":[{"id":"x","id":"ps"}]}', platform],
    ['{"platforms":{}}', platform],
    ['{"platforms":[null]}', platform],
    ['{"platforms":[', platform],
    ['[]', platform],
  ]) {
    const result = addPlatform(text, value);
    assert.ok(result.error);
    assert.equal(result.text, text);
  }
});

test('predefined platforms can be added together without collisions', () => {
  let text = '{"platforms":[]}';
  for (const preset of knownPlatforms) {
    const result = addPlatform(text, preset);
    assert.equal(result.error, '', preset.libretro_name);
    text = result.text;
  }
});

const game = { id: 'example', canonical_name: 'Example', display_name: '', platform_id: 'ps', playlist: '', discs: [{ entrypoint: 'Disc/game.dat', files: [{ source: 'input/game.dat', path: 'Disc/game.dat' }] }] };
const gameManifest = '{"large":9007199254740993,"extra":1,"extra":2,"platforms":[{"id":"ps","name":"PlayStation","libretro_name":"Sony - PlayStation"}],"contents":[]}';

test('adds a game without changing unrelated JSON values', () => {
  const result = addGame(gameManifest, game);
  assert.equal(result.error, '');
  assert.match(result.text, /9007199254740993/);
  assert.match(result.text, /"extra"\s*:\s*1\s*,\s*"extra"\s*:\s*2/);
  assert.deepEqual(JSON.parse(result.text).contents, [{ id: 'example', canonical_name: 'Example', platform_id: 'ps', discs: game.discs }]);
  assert.ok(addGame(result.text, game).error);
});

test('rejects unsafe paths, duplicate destinations and missing entrypoints', () => {
  for (const path of ['', '../file', '/file', 'a//b', 'a/./b', 'a\\b', 'a:b', 'a\u0000b']) {
    for (const field of ['source', 'path']) {
      const candidate = { ...game, discs: [{ ...game.discs[0], files: [{ ...game.discs[0].files[0], [field]: path }] }] };
      assert.ok(addGame(gameManifest, candidate).error, `${field}: ${JSON.stringify(path)}`);
    }
  }
  assert.ok(addGame(gameManifest, { ...game, id: '../bad' }).error);
  assert.ok(addGame(gameManifest, { ...game, platform_id: 'unknown' }).error);
  assert.ok(addGame(gameManifest, { ...game, discs: [{ ...game.discs[0], entrypoint: 'missing' }] }).error);
  assert.ok(addGame(gameManifest, { ...game, discs: [{ ...game.discs[0], files: [...game.discs[0].files, game.discs[0].files[0]] }] }).error);
  assert.ok(addGame('{"platforms":[],"contents":[],"contents":[]}', game).error);
});

test('requires and validates a playlist for ordered multi-disc games', () => {
  const multi = { ...game, discs: [...game.discs, { entrypoint: 'Disc2/game.dat', files: [{ source: 'other.dat', path: 'Disc2/game.dat' }] }] };
  assert.ok(addGame(gameManifest, multi).error);
  assert.ok(addGame(gameManifest, { ...multi, playlist: 'Disc/game.dat' }).error);
  const result = addGame(gameManifest, { ...multi, playlist: 'game.m3u', display_name: 'Пример' });
  assert.equal(result.error, '');
  assert.deepEqual(JSON.parse(result.text).contents[0].discs, multi.discs);
  assert.equal(JSON.parse(result.text).contents[0].playlist, 'game.m3u');
  assert.ok(addGame(gameManifest, { ...multi, playlist: '../game.m3u' }).error);
  assert.ok(addGame(gameManifest, { ...multi, playlist: 'Disc/#bad', discs: [{ entrypoint: 'Disc/#game', files: [{ source: 'game', path: 'Disc/#game' }] }, multi.discs[1]] }).error);
});

test('replaces only the newly added unsaved game and preserves other content', () => {
  const existing = { id: 'old', canonical_name: 'Old', platform_id: 'ps', discs: game.discs, extra: 42 };
  const text = gameManifest.replace('"contents":[]', `"contents":[${JSON.stringify(existing)}]`);
  const added = addGame(text, game);
  const changed = { ...game, canonical_name: 'Исправлено', discs: [{ entrypoint: 'new.dat', files: [{ source: 'new.dat', path: 'new.dat' }] }] };
  const replaced = addGame(added.text, changed, added.replacement);
  assert.equal(replaced.error, '');
  assert.deepEqual(JSON.parse(replaced.text).contents, [existing, { id: game.id, canonical_name: changed.canonical_name, platform_id: game.platform_id, discs: changed.discs }]);
  assert.match(replaced.text, /9007199254740993/);
  assert.ok(addGame(added.text, { ...changed, id: existing.id }, added.replacement).error);
  const externallyEdited = added.text.replace('"canonical_name": "Example"', '"canonical_name": "Вручную"');
  const rejected = addGame(externallyEdited, changed, added.replacement);
  assert.ok(rejected.error);
  assert.equal(rejected.text, externallyEdited);
  const removed = JSON.stringify({ platforms: [platform], contents: [existing] });
  assert.ok(addGame(removed, changed, added.replacement).error);
});

test('localizes validation errors without changing manifest text', () => {
  const unsafe = { ...game, discs: [{ ...game.discs[0], files: [{ source: '../secret', path: 'Disc/game.dat' }] }] };
  const duplicate = { ...game, discs: [{ ...game.discs[0], files: [...game.discs[0].files, game.discs[0].files[0]] }] };
  const multi = { ...game, discs: [...game.discs, { entrypoint: 'Disc2/game.dat', files: [{ source: 'other.dat', path: 'Disc2/game.dat' }] }] };
  const cases = [
    ['{', locale => addPlatform('{', platform, locale), 'Исправьте некорректный JSON перед добавлением платформы.', 'Fix the invalid JSON before adding a platform.'],
    ['{', locale => addGame('{', game, null, locale), 'Исправьте некорректный JSON перед добавлением игры.', 'Fix the invalid JSON before adding a game.'],
    ['{}', locale => addPlatform('{}', { ...platform, id: '../unsafe' }, locale), 'ID должен быть безопасным компонентом пути.', 'ID must be a safe path component.'],
    ['{"platforms":[{"id":"other","id":"ps"}]}', locale => addPlatform('{"platforms":[{"id":"other","id":"ps"}]}', platform, locale), 'В платформе повторяется поле id.', 'The id field is duplicated in a platform.'],
    ['{"platforms":[{"id":"other","libretro_name":"Sony - PlayStation"}]}', locale => addPlatform('{"platforms":[{"id":"other","libretro_name":"Sony - PlayStation"}]}', platform, locale), 'Платформа с таким libretro_name уже существует.', 'A platform with this libretro_name already exists.'],
    [gameManifest, locale => addGame(gameManifest, unsafe, null, locale), 'Пути файлов должны быть безопасными относительными путями.', 'File paths must be safe relative paths.'],
    [gameManifest, locale => addGame(gameManifest, duplicate, null, locale), 'Пути файлов в игре не должны повторяться.', 'File paths within a game must not be duplicated.'],
    [gameManifest, locale => addGame(gameManifest, multi, null, locale), 'Для нескольких дисков укажите путь плейлиста.', 'Enter a playlist path for multiple discs.'],
  ];
  for (const [text, validate, ru, en] of cases) {
    assert.deepEqual(validate('ru'), { text, error: ru });
    assert.deepEqual(validate('en'), { text, error: en });
    assert.deepEqual(validate('unsupported'), { text, error: ru });
  }
  assert.equal(addPlatform('{', platform).error, cases[0][2]);
  assert.equal(addGame('{', game).error, cases[1][2]);
});

test('adding and replacing localized games preserves user data', () => {
  const candidate = { ...game, canonical_name: 'Имя {key}', display_name: 'Игра', discs: [...game.discs, { entrypoint: 'Disc2/game.dat', files: [{ source: 'диск.dat', path: 'Disc2/game.dat' }] }], playlist: 'игра.m3u' };
  for (const locale of ['ru', 'en']) {
    const added = addGame(gameManifest, candidate, null, locale);
    assert.equal(added.error, '');
    const updated = { ...candidate, canonical_name: 'New name' };
    const replaced = addGame(added.text, updated, added.replacement, locale);
    assert.equal(replaced.error, '');
    assert.deepEqual(JSON.parse(replaced.text).contents[0], { id: updated.id, canonical_name: updated.canonical_name, display_name: updated.display_name, platform_id: updated.platform_id, discs: updated.discs, playlist: updated.playlist });
    assert.match(replaced.text, /9007199254740993/);
    const edited = added.text.replace('Имя {key}', 'Ручное изменение');
    const refused = addGame(edited, updated, added.replacement, locale);
    assert.equal(refused.text, edited);
    assert.equal(refused.error, locale === 'ru' ? 'Добавленная игра была изменена в JSON. Чтобы не потерять изменения, начните добавление заново.' : 'The added game was changed in the JSON. Start adding it again to avoid losing changes.');
  }
});
