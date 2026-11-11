const english = {
  'ID должен быть безопасным компонентом пути.': 'ID must be a safe path component.',
  'Выберите существующую платформу.': 'Select an existing platform.',
  'Укажите основное название без NUL; отображаемое название также не должно содержать NUL.': 'Enter a canonical name without NUL; the display name must not contain NUL either.',
  'Добавьте хотя бы один диск.': 'Add at least one disc.',
  'Для каждого диска укажите корректную точку входа и файлы.': 'Enter a valid entry point and files for each disc.',
  'Пути файлов должны быть безопасными относительными путями.': 'File paths must be safe relative paths.',
  'Пути файлов в игре не должны повторяться.': 'File paths within a game must not be duplicated.',
  'Точка входа диска должна совпадать с путём одного из его файлов.': 'The disc entry point must match the path of one of its files.',
  'Для нескольких дисков укажите путь плейлиста.': 'Enter a playlist path for multiple discs.',
  'Путь плейлиста должен быть безопасным и отличаться от путей файлов.': 'The playlist path must be safe and different from the file paths.',
  'Ссылка на диск в плейлисте не должна начинаться с #.': 'A disc reference in the playlist must not start with #.',
};

export function validationMessage(locale, message) {
  return locale === 'en' ? english[message] : message;
}
