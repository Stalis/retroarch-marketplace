const english = {
  'Исправьте некорректный JSON перед добавлением платформы.': 'Fix the invalid JSON before adding a platform.',
  'Исправьте некорректный JSON перед добавлением игры.': 'Fix the invalid JSON before adding a game.',
  'Манифест должен быть объектом JSON.': 'The manifest must be a JSON object.',
  'В манифесте повторяется поле platforms.': 'The platforms field is duplicated in the manifest.',
  'Поле platforms должно быть массивом.': 'The platforms field must be an array.',
  'ID должен быть безопасным компонентом пути.': 'ID must be a safe path component.',
  'Укажите непустое название без NUL.': 'Enter a non-empty name without NUL.',
  'Libretro name должен быть безопасным компонентом пути.': 'Libretro name must be a safe path component.',
  'Каждая платформа должна быть объектом.': 'Each platform must be an object.',
  'Поля contents и platforms должны быть массивами без повторений.': 'The contents and platforms fields must each appear once and be arrays.',
  'Добавленная игра была изменена в JSON. Чтобы не потерять изменения, начните добавление заново.': 'The added game was changed in the JSON. Start adding it again to avoid losing changes.',
  'Каждая игра должна быть объектом.': 'Each game must be an object.',
  'В игре повторяется поле id.': 'The id field is duplicated in a game.',
  'Игра с таким ID уже существует.': 'A game with this ID already exists.',
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
  'В платформе повторяется поле {key}.': 'The {key} field is duplicated in a platform.',
  'Платформа с таким {key} уже существует.': 'A platform with this {key} already exists.',
};

export function validationMessage(locale, message, values = {}) {
  const template = locale === 'en' ? english[message] : message;
  return template.replace(/\{(\w+)\}/g, (_, key) => values[key]);
}
