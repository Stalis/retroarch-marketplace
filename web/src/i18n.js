export const messages = {
  ru: {
    title: 'Управление каталогом', subtitle: 'Локальный редактор каталога', language: 'Язык',
    lookupCanonical: 'Найти название BoxArt', lookupLoading: 'Поиск названий…', lookupEmpty: 'Совпадения не найдены.', lookupError: 'Не удалось найти названия.',
    unsaved: '● Есть изменения', reload: 'Загрузить заново', save: 'Сохранить',
    discard: 'Несохранённые изменения будут потеряны. Продолжить?',
    conflict: 'Игра изменена вне редактора. Ваши изменения остались в форме; загрузите актуальную версию и перенесите изменения вручную.',
    forbidden: 'Доступ запрещён. Откройте редактор с того же адреса, что и сервер.', serverError: status => `Ошибка сервера (${status})`, retry: 'Попробуйте ещё раз.',
    loadNetwork: 'Не удалось связаться с сервером. Убедитесь, что сервер запущен, и обновите страницу.',
    savedNotice: 'Изменения сохранены.', saveNetwork: 'Не удалось связаться с сервером. Изменения остались в редакторе; проверьте соединение и повторите попытку.',
    choosePlatformError: 'Выберите платформу из списка.',
    missingTitle: 'Не найдены исходные файлы', missingWarning: 'Игра с отсутствующими файлами не будет доступна в каталоге, пока файлы не появятся.', saveAnyway: 'Всё равно сохранить', backEdit: 'Вернуться к редактированию', working: 'Обращение к серверу…',
    catalogGames: 'Каталог игр', catalog: 'КАТАЛОГ', games: 'Игры', noGames: 'В каталоге нет игр.', platform: 'Платформа',
    addGame: 'Добавить игру', gameHint: 'Исходные пути указываются относительно директории базы данных.', gameData: 'Данные игры', canonicalName: 'Основное название', displayOptional: 'Отображаемое название (необязательно)', choosePlatform: 'Выберите платформу', disc: n => `Диск ${n}`, entrypoint: 'Точка входа (путь в архиве)', sourceFile: n => `Исходный файл ${n}`, filePath: n => `Путь файла ${n} в архиве`, removeFile: (f, d) => `Удалить файл ${f} диска ${d}`, addFile: n => `Добавить файл к диску ${n}`, removeDisc: n => `Удалить диск ${n}`, addDisc: 'Добавить диск', playlistRequired: 'Путь плейлиста в архиве (обязательно для нескольких дисков)', playlistOptional: 'Путь плейлиста в архиве (необязательно)',
    platforms: 'Платформы', noPlatforms: 'Платформ пока нет.', addPlatform: 'Добавить платформу', fromList: 'Из списка', customPlatform: 'Своя платформа', platformLibretro: 'Платформа (libretro name)', name: 'Название', platformAdded: value => `Добавлена платформа: ${value}.`,
    searchPlaceholder: 'Поиск по названию…', noSearchResults: 'Игры не найдены',
    tabGames: 'Игры', tabPlatforms: 'Платформы',
    publishedGames: n => `доступно игр: ${n}`,
    deleteGame: 'Удалить игру', confirmDelete: 'Удалить игру из каталога? Исходные файлы не будут затронуты.', loadMore: 'Загрузить ещё',
    deletePlatform: 'Удалить', platformInUse: 'Платформу нельзя удалить: к ней привязаны игры.', unavailable: 'недоступна',
    nameMismatch: 'нет в BoxArt', nameMismatchHint: 'Название не совпадает ни с одним названием Libretro BoxArt для этой платформы — обложка не найдётся. Похожие названия:',
  },
  en: {
    title: 'Catalog management', subtitle: 'Local catalog editor', language: 'Language',
    lookupCanonical: 'Find BoxArt name', lookupLoading: 'Searching names…', lookupEmpty: 'No matches found.', lookupError: 'Could not look up names.',
    unsaved: '● Unsaved changes', reload: 'Reload', save: 'Save',
    discard: 'Unsaved changes will be lost. Continue?',
    conflict: 'The game was changed outside the editor. Your changes remain in the form; reload the current version and transfer your changes manually.',
    forbidden: 'Access denied. Open the editor from the same address as the server.', serverError: status => `Server error (${status})`, retry: 'Please try again.',
    loadNetwork: 'Could not contact the server. Make sure it is running and reload the page.',
    savedNotice: 'Changes saved.', saveNetwork: 'Could not contact the server. Your changes remain in the editor; check the connection and try again.',
    choosePlatformError: 'Select a platform from the list.',
    missingTitle: 'Source files not found', missingWarning: 'A game with missing files will not be available in the catalog until the files appear.', saveAnyway: 'Save anyway', backEdit: 'Return to editing', working: 'Contacting server…',
    catalogGames: 'Game catalog', catalog: 'CATALOG', games: 'Games', noGames: 'There are no games in the catalog.', platform: 'Platform',
    addGame: 'Add game', gameHint: 'Source paths are relative to the database directory.', gameData: 'Game details', canonicalName: 'Primary title', displayOptional: 'Display name (optional)', choosePlatform: 'Select a platform', disc: n => `Disc ${n}`, entrypoint: 'Entrypoint (path in archive)', sourceFile: n => `Source file ${n}`, filePath: n => `File ${n} path in archive`, removeFile: (f, d) => `Remove file ${f} from disc ${d}`, addFile: n => `Add file to disc ${n}`, removeDisc: n => `Remove disc ${n}`, addDisc: 'Add disc', playlistRequired: 'Playlist path in archive (required for multiple discs)', playlistOptional: 'Playlist path in archive (optional)',
    platforms: 'Platforms', noPlatforms: 'There are no platforms yet.', addPlatform: 'Add platform', fromList: 'From list', customPlatform: 'Custom platform', platformLibretro: 'Platform (libretro name)', name: 'Name', platformAdded: value => `Platform added: ${value}.`,
    searchPlaceholder: 'Search by title…', noSearchResults: 'No games found',
    tabGames: 'Games', tabPlatforms: 'Platforms',
    publishedGames: n => `available games: ${n}`,
    deleteGame: 'Delete game', confirmDelete: 'Delete the game from the catalog? Source files are not touched.', loadMore: 'Load more',
    deletePlatform: 'Delete', platformInUse: 'The platform cannot be deleted: games use it.', unavailable: 'unavailable',
    nameMismatch: 'not in BoxArt', nameMismatchHint: 'The name matches no Libretro BoxArt name for this platform, so no cover art will be found. Similar names:',
  },
};

export function initialLocale() {
  try { return localStorage.getItem('locale') === 'en' ? 'en' : 'ru'; } catch { return 'ru'; }
}

export function persistLocale(locale) {
  try { localStorage.setItem('locale', locale); } catch { /* Storage may be unavailable. */ }
}
