import { validationMessage } from './validation-i18n.js';

const safePath = (value, component = false) => typeof value === 'string' && value !== '' && !/[\\:\x00-\x1f\x7f]/.test(value) && (!component || !value.includes('/')) && value.split('/').every(part => part && part !== '.' && part !== '..');

export function quickDisc(source) {
  const path = source.split('/').at(-1);
  return { entrypoint: path, files: [{ source, path }] };
}

export function suggestedGameID(platform, source) {
  const filename = source.split('/').at(-1).replace(/\.[^.]+$/, '');
  const slug = filename.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '');
  return platform && slug ? `${platform}-${slug}` : '';
}

// Mirrors content.Game.Validate and identifies the control to focus.
export function gameIssue(game) {
  const issue = (field, message, label) => ({ field, message, label });
  if (!game.platform_id) return issue('game-platform', 'Выберите существующую платформу.');
  if (!game.canonical_name || game.canonical_name.includes('\0')) {
    return issue('game-canonical_name', 'Укажите основное название без NUL; отображаемое название также не должно содержать NUL.',
      !game.canonical_name ? 'requiredTitle' : 'invalidTitle');
  }
  if ((game.display_name || '').includes('\0')) {
    return issue('game-display_name', 'Укажите основное название без NUL; отображаемое название также не должно содержать NUL.', 'invalidTitle');
  }
  if (!game.discs.length) return issue('add-disc', 'Добавьте хотя бы один диск.');
  const paths = new Set();
  const conflicts = path => [...paths].some(existing =>
    existing === path || existing.startsWith(path + '/') || path.startsWith(existing + '/'));
  for (const [index, disc] of game.discs.entries()) {
    const prefix = `disc-${index}`;
    if (!disc.files.length) return issue(`${prefix}-add-file`, 'Для каждого диска укажите корректную точку входа и файлы.');
    for (const [fileIndex, file] of disc.files.entries()) {
      if (!safePath(file.source)) return issue(`${prefix}-source-${fileIndex}`, 'Пути файлов должны быть безопасными относительными путями.');
      if (!safePath(file.path)) return issue(`${prefix}-path-${fileIndex}`, 'Пути файлов должны быть безопасными относительными путями.');
      if (conflicts(file.path)) return issue(`${prefix}-path-${fileIndex}`, 'Пути файлов в игре не должны повторяться.', paths.has(file.path) ? undefined : 'pathConflict');
      paths.add(file.path);
    }
    if (!safePath(disc.entrypoint)) return issue(`${prefix}-entrypoint`, 'Для каждого диска укажите корректную точку входа и файлы.');
    if (!disc.files.some(file => file.path === disc.entrypoint)) return issue(`${prefix}-entrypoint`, 'Точка входа диска должна совпадать с путём одного из его файлов.');
  }
  if (!safePath(game.id, true)) return issue('game-id', 'ID должен быть безопасным компонентом пути.');
  if (game.discs.length > 1 && !game.playlist) return issue('game-playlist', 'Для нескольких дисков укажите путь плейлиста.');
  if (game.playlist) {
    if (!safePath(game.playlist) || conflicts(game.playlist)) return issue('game-playlist', 'Путь плейлиста должен быть безопасным и отличаться от путей файлов.');
    const directory = game.playlist.split('/').slice(0, -1);
    for (const disc of game.discs) {
      const parts = disc.entrypoint.split('/');
      let common = 0;
      while (common < directory.length && common < parts.length && directory[common] === parts[common]) common++;
      const relative = [...directory.slice(common).map(() => '..'), ...parts.slice(common)].join('/');
      if (relative.startsWith('#')) return issue('game-playlist', 'Ссылка на диск в плейлисте не должна начинаться с #.');
    }
  }
  return null;
}

export function validateGame(game, locale = 'ru') {
  const issue = gameIssue(game);
  return issue ? validationMessage(locale, issue.message) : '';
}
