import { applyEdits, format, modify, parseTree, findNodeAtLocation } from 'jsonc-parser';

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

export function addPlatform(text, platform) {
  const fail = error => ({ text, error });
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
      if (matching.length > 1) return fail(`В платформе повторяется поле ${key}.`);
      if (matching[0]?.children?.[1]?.value === platform[key]) return fail(`Платформа с таким ${key} уже существует.`);
    }
  }
  const value = { id: platform.id, name: platform.name, libretro_name: platform.libretro_name };
  const path = list ? ['platforms', list.children?.length || 0] : ['platforms'];
  return { text: applyEdits(text, modify(text, path, list ? value : [value], { isArrayInsertion: !!list, formattingOptions: { insertSpaces: true, tabSize: 2 } })), error: '' };
}
