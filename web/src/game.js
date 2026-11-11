import { validationMessage } from './validation-i18n.js';

const safePath = (value, component = false) => typeof value === 'string' && value !== '' && !/[\\:\x00-\x1f\x7f]/.test(value) && (!component || !value.includes('/')) && value.split('/').every(part => part && part !== '.' && part !== '..');

// Client-side mirror of content.Game.Validate; returns '' or a localized message.
export function validateGame(game, locale = 'ru') {
  const fail = (message) => validationMessage(locale, message);
  if (!safePath(game.id, true)) return fail('ID должен быть безопасным компонентом пути.');
  if (!game.platform_id) return fail('Выберите существующую платформу.');
  if (!game.canonical_name || game.canonical_name.includes('\0') || (game.display_name || '').includes('\0')) return fail('Укажите основное название без NUL; отображаемое название также не должно содержать NUL.');
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
  return '';
}
