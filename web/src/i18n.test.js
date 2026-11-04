import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialLocale, messages, persistLocale } from './i18n.js';

test('language preference survives reload and unsupported values fall back to Russian', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const values = new Map();
  globalThis.localStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) };
  try {
    assert.equal(initialLocale(), 'ru');
    persistLocale('en');
    assert.equal(initialLocale(), 'en');
    values.set('locale', 'unsupported');
    assert.equal(initialLocale(), 'ru');
    globalThis.localStorage = { getItem() { throw new Error('unavailable'); }, setItem() { throw new Error('unavailable'); } };
    assert.equal(initialLocale(), 'ru');
    persistLocale('en');
  } finally {
    if (previous === undefined) delete globalThis.localStorage;
    else Object.defineProperty(globalThis, 'localStorage', previous);
  }
});

test('both languages provide the same interface messages', () => {
  assert.deepEqual(Object.keys(messages.en).sort(), Object.keys(messages.ru).sort());
  assert.equal(messages.ru.save, 'Сохранить');
  assert.equal(messages.en.save, 'Save');
});
