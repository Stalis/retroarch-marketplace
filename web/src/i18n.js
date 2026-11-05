export const messages = {
  ru: {
    title: 'Управление каталогом', subtitle: 'Локальный редактор манифеста', language: 'Язык',
    noData: 'Нет данных', unsaved: '● Есть изменения', saved: '● Сохранено', reload: 'Загрузить заново', save: 'Сохранить',
    discard: 'Несохранённые изменения будут потеряны. Загрузить манифест с сервера?',
    conflict: 'Манифест изменён вне редактора. Ваши изменения сохранены здесь; скопируйте их, затем загрузите актуальную версию и перенесите изменения вручную.',
    forbidden: 'Доступ запрещён. Откройте редактор с того же адреса, что и сервер.', serverError: status => `Ошибка сервера (${status})`, retry: 'Попробуйте ещё раз.',
    noEtag: 'Сервер не вернул ETag. Сохранение небезопасно; проверьте настройки сервера.', loaded: 'Манифест загружен.',
    loadNetwork: 'Не удалось связаться с сервером. Убедитесь, что сервер запущен, и повторите загрузку.',
    staleApproval: 'Манифест изменился после проверки. Сохраните его заново для повторной проверки.',
    changedDraft: 'Форма игры изменена. Сначала примените изменения к манифесту и сохраните его заново.',
    savedNoEtag: 'Сервер принял изменения, но не вернул ETag. Скопируйте текст и загрузите манифест заново перед следующим сохранением.',
    savedNotice: 'Изменения сохранены.', saveNetwork: 'Не удалось связаться с сервером. Изменения остались в редакторе; проверьте соединение и загрузите актуальную версию перед повторной попыткой.',
    nameError: 'Не удалось изменить имя: проверьте повторяющиеся поля contents и canonical_name в JSON.', choosePlatformError: 'Выберите платформу из списка.',
    gameAdded: 'Игра добавлена в манифест. Сохраните изменения кнопкой «Сохранить».',
    missingTitle: 'Не найдены исходные файлы', missingWarning: 'Игра с отсутствующими файлами не будет доступна в каталоге, пока файлы не появятся.', saveAnyway: 'Всё равно сохранить', backEdit: 'Вернуться к редактированию', working: 'Обращение к серверу…',
    catalogGames: 'Каталог игр', catalog: 'КАТАЛОГ', games: 'Игры', fixGames: 'Исправьте JSON, чтобы просмотреть игры.', noGames: 'В манифесте нет игр.', gameNumber: n => `Игра ${n}`, notSpecified: 'Не указана', notSpecifiedId: 'Не указан', notSpecifiedName: 'Не указано', notSpecifiedDiscs: 'Не указаны', missingAllowedShort: ' · разрешены отсутствующие файлы',
    selectedGame: 'ВЫБРАННАЯ ИГРА', details: 'Сведения', canonicalHint: '— основное название', identifier: 'Идентификатор', platform: 'Платформа', displayName: 'Отображаемое название', discs: 'Диски', missingAllowed: 'Разрешены отсутствующие файлы. Игра появится в каталоге, когда все её исходные файлы будут доступны.', otherFields: 'Остальные поля, диски и файлы можно изменить в полном JSON ниже.', selectGame: 'Выберите игру в списке.',
    addGame: 'Добавить игру', gameHint: 'Исходные пути указываются относительно директории манифеста. Игра добавляется только в редактор; затем нажмите «Сохранить».', gameData: 'Данные игры', canonicalName: 'Основное название', displayOptional: 'Отображаемое название (необязательно)', choosePlatform: 'Выберите платформу', disc: n => `Диск ${n}`, entrypoint: 'Точка входа (путь в архиве)', sourceFile: n => `Исходный файл ${n}`, filePath: n => `Путь файла ${n} в архиве`, removeFile: (f, d) => `Удалить файл ${f} диска ${d}`, addFile: n => `Добавить файл к диску ${n}`, removeDisc: n => `Удалить диск ${n}`, addDisc: 'Добавить диск', playlistRequired: 'Путь плейлиста в архиве (обязательно для нескольких дисков)', playlistOptional: 'Путь плейлиста в архиве (необязательно)', applyGame: 'Применить изменения игры', anotherGame: 'Добавить другую игру',
    platforms: 'Платформы', fixPlatforms: 'Исправьте JSON, чтобы просмотреть платформы.', noPlatforms: 'В манифесте нет платформ.', unnamed: 'Без названия', noId: 'Без ID', noLibretro: 'Без libretro name', addPlatform: 'Добавить платформу', fromList: 'Из списка', customPlatform: 'Своя платформа', platformLibretro: 'Платформа (libretro name)', name: 'Название', platformAdded: value => `Добавлена платформа: ${value}. Сохраните изменения кнопкой «Сохранить».`,
    advanced: 'РАСШИРЕННОЕ РЕДАКТИРОВАНИЕ', fullJson: 'Полный манифест JSON', jsonHint: 'Здесь доступны все поля, включая платформы, диски и файлы. Изменения синхронизированы с карточкой игры.', invalidJson: detail => `Некорректный JSON: ${detail}. Исправьте синтаксис перед сохранением.`,
    searchPlaceholder: 'Поиск по названию или ID…', formatJson: 'Форматировать JSON', noSearchResults: 'Игры не найдены',
    tabGames: 'Игры', tabPlatforms: 'Платформы', tabJson: 'JSON-манифест',
  },
  en: {
    title: 'Catalog management', subtitle: 'Local manifest editor', language: 'Language',
    noData: 'No data', unsaved: '● Unsaved changes', saved: '● Saved', reload: 'Reload', save: 'Save',
    discard: 'Unsaved changes will be lost. Reload the manifest from the server?',
    conflict: 'The manifest was changed outside the editor. Your changes remain here; copy them, then reload the current version and transfer your changes manually.',
    forbidden: 'Access denied. Open the editor from the same address as the server.', serverError: status => `Server error (${status})`, retry: 'Please try again.',
    noEtag: 'The server did not return an ETag. Saving is unsafe; check the server configuration.', loaded: 'Manifest loaded.',
    loadNetwork: 'Could not contact the server. Make sure it is running and try reloading.',
    staleApproval: 'The manifest changed after the check. Save it again to repeat the check.',
    changedDraft: 'The game form has changed. Apply the changes to the manifest and save it again first.',
    savedNoEtag: 'The server accepted the changes but did not return an ETag. Copy the text and reload the manifest before saving again.',
    savedNotice: 'Changes saved.', saveNetwork: 'Could not contact the server. Your changes remain in the editor; check the connection and reload the current version before trying again.',
    nameError: 'Could not change the name: check for duplicate contents and canonical_name fields in the JSON.', choosePlatformError: 'Select a platform from the list.',
    gameAdded: 'Game added to the manifest. Use Save to save your changes.',
    missingTitle: 'Source files not found', missingWarning: 'A game with missing files will not be available in the catalog until the files appear.', saveAnyway: 'Save anyway', backEdit: 'Return to editing', working: 'Contacting server…',
    catalogGames: 'Game catalog', catalog: 'CATALOG', games: 'Games', fixGames: 'Fix the JSON to view games.', noGames: 'There are no games in the manifest.', gameNumber: n => `Game ${n}`, notSpecified: 'Not specified', notSpecifiedId: 'Not specified', notSpecifiedName: 'Not specified', notSpecifiedDiscs: 'Not specified', missingAllowedShort: ' · missing files allowed',
    selectedGame: 'SELECTED GAME', details: 'Details', canonicalHint: '— primary title', identifier: 'Identifier', platform: 'Platform', displayName: 'Display name', discs: 'Discs', missingAllowed: 'Missing files are allowed. The game will appear in the catalog when all its source files are available.', otherFields: 'Other fields, discs and files can be edited in the full JSON below.', selectGame: 'Select a game from the list.',
    addGame: 'Add game', gameHint: 'Source paths are relative to the manifest directory. The game is added only to the editor; then press Save.', gameData: 'Game details', canonicalName: 'Primary title', displayOptional: 'Display name (optional)', choosePlatform: 'Select a platform', disc: n => `Disc ${n}`, entrypoint: 'Entrypoint (path in archive)', sourceFile: n => `Source file ${n}`, filePath: n => `File ${n} path in archive`, removeFile: (f, d) => `Remove file ${f} from disc ${d}`, addFile: n => `Add file to disc ${n}`, removeDisc: n => `Remove disc ${n}`, addDisc: 'Add disc', playlistRequired: 'Playlist path in archive (required for multiple discs)', playlistOptional: 'Playlist path in archive (optional)', applyGame: 'Apply game changes', anotherGame: 'Add another game',
    platforms: 'Platforms', fixPlatforms: 'Fix the JSON to view platforms.', noPlatforms: 'There are no platforms in the manifest.', unnamed: 'Unnamed', noId: 'No ID', noLibretro: 'No libretro name', addPlatform: 'Add platform', fromList: 'From list', customPlatform: 'Custom platform', platformLibretro: 'Platform (libretro name)', name: 'Name', platformAdded: value => `Platform added: ${value}. Use Save to save your changes.`,
    advanced: 'ADVANCED EDITING', fullJson: 'Full JSON manifest', jsonHint: 'All fields are available here, including platforms, discs and files. Changes are synchronized with the game details.', invalidJson: detail => `Invalid JSON: ${detail}. Fix the syntax before saving.`,
    searchPlaceholder: 'Search by title or ID…', formatJson: 'Format JSON', noSearchResults: 'No games found',
    tabGames: 'Games', tabPlatforms: 'Platforms', tabJson: 'JSON Manifest',
  },
};

export function initialLocale() {
  try { return localStorage.getItem('locale') === 'en' ? 'en' : 'ru'; } catch { return 'ru'; }
}

export function persistLocale(locale) {
  try { localStorage.setItem('locale', locale); } catch { /* Storage may be unavailable. */ }
}
