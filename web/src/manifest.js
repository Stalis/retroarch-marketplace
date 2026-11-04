import { applyEdits, format, modify, parseTree, findNodeAtLocation } from 'jsonc-parser';
import { validationMessage } from './validation-i18n.js';

export function parseManifest(text) {
  try {
    return { value: JSON.parse(text), error: '' };
  } catch (error) {
    return { value: null, error: error.message };
  }
}

export function formatManifest(text) {
  if (parseManifest(text).error) return text;
  return applyEdits(text, format(text, undefined, { insertSpaces: true, tabSize: 2 }));
}

export function updateCanonicalName(text, index, name) {
  const tree = parseTree(text);
  const rootKeys = tree?.children?.map(property => property.children?.[0]?.value) || [];
  if (tree?.type !== 'object' || rootKeys.filter(key => key === 'contents').length !== 1) return text;
  const content = findNodeAtLocation(tree, ['contents', index]);
  if (!content || content.type !== 'object') return text;
  const keys = content.children?.map(property => property.children?.[0]?.value) || [];
  if (keys.filter(key => key === 'canonical_name').length > 1) return text;
  return applyEdits(text, modify(text, ['contents', index, 'canonical_name'], name, {}));
}

export function addPlatform(text, platform, locale = 'ru') {
  const fail = (message, values) => ({ text, error: validationMessage(locale, message, values) });
  if (parseManifest(text).error) return fail('Исправьте некорректный JSON перед добавлением платформы.');
  const tree = parseTree(text);
  if (tree?.type !== 'object') return fail('Манифест должен быть объектом JSON.');
  const properties = tree.children?.filter(property => property.children?.[0]?.value === 'platforms') || [];
  if (properties.length > 1) return fail('В манифесте повторяется поле platforms.');
  const list = properties[0]?.children?.[1];
  if (list && list.type !== 'array') return fail('Поле platforms должно быть массивом.');
  const safe = value => typeof value === 'string' && value !== '' && value !== '.' && value !== '..' && !/[\/\\:\x00-\x1f\x7f]/.test(value);
  if (!safe(platform.id)) return fail('ID должен быть безопасным компонентом пути.');
  if (typeof platform.name !== 'string' || !platform.name || platform.name.includes('\0')) return fail('Укажите непустое название без NUL.');
  if (!safe(platform.libretro_name)) return fail('Libretro name должен быть безопасным компонентом пути.');
  for (const item of list?.children || []) {
    if (item.type !== 'object') return fail('Каждая платформа должна быть объектом.');
    const fields = item.children || [];
    for (const key of ['id', 'libretro_name']) {
      const matching = fields.filter(property => property.children?.[0]?.value === key);
      if (matching.length > 1) return fail('В платформе повторяется поле {key}.', { key });
      if (matching[0]?.children?.[1]?.value === platform[key]) return fail('Платформа с таким {key} уже существует.', { key });
    }
  }
  const value = { id: platform.id, name: platform.name, libretro_name: platform.libretro_name };
  const path = list ? ['platforms', list.children?.length || 0] : ['platforms'];
  return { text: applyEdits(text, modify(text, path, list ? value : [value], { isArrayInsertion: !!list, formattingOptions: { insertSpaces: true, tabSize: 2 } })), error: '' };
}

export function addGame(text, game, replacement = null, locale = 'ru') {
  const fail = (message, values) => ({ text, error: validationMessage(locale, message, values) });
  if (parseManifest(text).error) return fail('Исправьте некорректный JSON перед добавлением игры.');
  const tree = parseTree(text);
  if (tree?.type !== 'object') return fail('Манифест должен быть объектом JSON.');
  const fields = key => tree.children?.filter(property => property.children?.[0]?.value === key) || [];
  const contents = fields('contents');
  const platforms = fields('platforms');
  if (contents.length !== 1 || contents[0].children?.[1]?.type !== 'array' || platforms.length !== 1 || platforms[0].children?.[1]?.type !== 'array') return fail('Поля contents и platforms должны быть массивами без повторений.');
  const list = contents[0].children[1];
  if (replacement) {
    const item = list.children?.[replacement.index];
    if (!item || text.slice(item.offset, item.offset + item.length) !== replacement.original) return fail('Добавленная игра была изменена в JSON. Чтобы не потерять изменения, начните добавление заново.');
  }
  const safePath = (value, component = false) => typeof value === 'string' && value !== '' && !/[\\:\x00-\x1f\x7f]/.test(value) && (!component || !value.includes('/')) && value.split('/').every(part => part && part !== '.' && part !== '..');
  if (!safePath(game.id, true)) return fail('ID должен быть безопасным компонентом пути.');
  for (const [index, item] of (list.children || []).entries()) {
    if (item.type !== 'object') return fail('Каждая игра должна быть объектом.');
    const ids = item.children?.filter(property => property.children?.[0]?.value === 'id') || [];
    if (ids.length > 1) return fail('В игре повторяется поле id.');
    if (ids[0]?.children?.[1]?.value === game.id && index !== replacement?.index) return fail('Игра с таким ID уже существует.');
  }
  const platformItems = platforms[0].children[1].children || [];
  if (!platformItems.some(item => item.type === 'object' && (item.children || []).filter(field => field.children?.[0]?.value === 'id').length === 1 && item.children.find(field => field.children?.[0]?.value === 'id')?.children?.[1]?.value === game.platform_id)) return fail('Выберите существующую платформу.');
  if (!game.canonical_name || game.canonical_name.includes('\0') || game.display_name.includes('\0')) return fail('Укажите основное название без NUL; отображаемое название также не должно содержать NUL.');
  if (!game.discs.length) return fail('Добавьте хотя бы один диск.');
  const paths = new Set();
  for (const disc of game.discs) {
    if (!safePath(disc.entrypoint) || !disc.files.length) return fail('Для каждого диска укажите корректную точку входа и файлы.');
    for (const file of disc.files) {
      if (!safePath(file.source) || !safePath(file.path)) return fail('Пути файлов должны быть безопасными относительными путями.');
      if (paths.has(file.path)) return fail('Пути файлов в игре не должны повторяться.');
      paths.add(file.path);
    }
    if (!disc.files.some(file => file.path === disc.entrypoint)) return fail('Точка входа диска должна совпадать с путём одного из его файлов.');
  }
  if (game.discs.length > 1 && !game.playlist) return fail('Для нескольких дисков укажите путь плейлиста.');
  if (game.playlist) {
    if (!safePath(game.playlist) || paths.has(game.playlist)) return fail('Путь плейлиста должен быть безопасным и отличаться от путей файлов.');
    const directory = game.playlist.split('/').slice(0, -1);
    for (const disc of game.discs) {
      const parts = disc.entrypoint.split('/');
      let common = 0;
      while (common < directory.length && common < parts.length && directory[common] === parts[common]) common++;
      const relative = [...directory.slice(common).map(() => '..'), ...parts.slice(common)].join('/');
      if (relative.startsWith('#')) return fail('Ссылка на диск в плейлисте не должна начинаться с #.');
    }
  }
  const value = { id: game.id, canonical_name: game.canonical_name, ...(game.display_name ? { display_name: game.display_name } : {}), platform_id: game.platform_id, discs: game.discs, ...(game.playlist ? { playlist: game.playlist } : {}) };
  const index = replacement ? replacement.index : list.children?.length || 0;
  const updated = applyEdits(text, modify(text, ['contents', index], value, { isArrayInsertion: !replacement, formattingOptions: { insertSpaces: true, tabSize: 2 } }));
  const node = findNodeAtLocation(parseTree(updated), ['contents', index]);
  return { text: updated, error: '', replacement: { index, original: updated.slice(node.offset, node.offset + node.length) } };
}
