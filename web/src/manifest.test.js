import { test } from 'node:test';
import assert from 'node:assert/strict';
import { addPlatform, formatManifest, parseManifest, updateCanonicalName } from './manifest.js';
import { knownPlatforms } from './platforms.js';

test('canonical name edits preserve unrelated JSON text', () => {
  const text = '{"contents":[{"canonical_name":"old","large":9007199254740993}],"platforms":[]}';
  assert.equal(updateCanonicalName(text, 0, 'new'), text.replace('"old"', '"new"'));
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
