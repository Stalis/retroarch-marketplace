import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { addPlatform, formatManifest, parseManifest, updateCanonicalName } from './manifest.js';
import { knownPlatforms } from './platforms.js';
import './style.css';

const endpoint = '/api/admin/manifest';
const display = (value, fallback) => typeof value === 'string' && value ? value : fallback;

async function responseError(response) {
  const body = await response.text();
  let detail = body.trim();
  try {
    const json = JSON.parse(body);
    detail = typeof json === 'string' ? json : json.error || json.message || body;
  } catch { /* Plain text is also a valid error response. */ }
  if (response.status === 409 || response.status === 412) {
    return `Манифест изменён вне редактора. Ваши изменения сохранены здесь; скопируйте их, затем загрузите актуальную версию и перенесите изменения вручную.${detail ? ` ${detail}` : ''}`;
  }
  if (response.status === 403) return `Доступ запрещён. Откройте редактор с того же адреса, что и сервер.${detail ? ` ${detail}` : ''}`;
  return `Ошибка сервера (${response.status})${detail ? `: ${detail}` : '. Попробуйте ещё раз.'}`;
}

function App() {
  const [text, setText] = useState('');
  const [savedText, setSavedText] = useState('');
  const [etag, setEtag] = useState('');
  const [selected, setSelected] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [platformMode, setPlatformMode] = useState('preset');
  const [preset, setPreset] = useState('');
  const [customPlatform, setCustomPlatform] = useState({ id: '', name: '', libretro_name: '' });
  const [platformError, setPlatformError] = useState('');
  const [addedPlatform, setAddedPlatform] = useState('');
  const dirty = loaded && text !== savedText;
  const parsed = parseManifest(text);
  const manifest = parsed.value;
  const games = Array.isArray(manifest?.contents) ? manifest.contents : [];
  const platforms = Array.isArray(manifest?.platforms) ? manifest.platforms : [];
  const game = games[selected];
  const platformName = (id) => display(platforms.find(platform => platform?.id === id)?.name, display(id, 'Не указана'));

  useEffect(() => {
    function warn(event) {
      if (dirty) { event.preventDefault(); event.returnValue = ''; }
    }
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  async function load(confirmDiscard = true) {
    if (confirmDiscard && dirty && !window.confirm('Несохранённые изменения будут потеряны. Загрузить манифест с сервера?')) return;
    setBusy(true); setError(''); setNotice('');
    try {
      const response = await fetch(endpoint, { cache: 'no-store' });
      if (!response.ok) throw new Error(await responseError(response));
      const body = await response.text();
      const revision = response.headers.get('ETag');
      if (!revision) throw new Error('Сервер не вернул ETag. Сохранение небезопасно; проверьте настройки сервера.');
      const formatted = formatManifest(body);
      setText(formatted); setSavedText(formatted); setEtag(revision); setLoaded(true); setSelected(0);
      setNotice('Манифест загружен.'); setAddedPlatform(''); setPlatformError('');
    } catch (err) {
      setError(err instanceof TypeError ? 'Не удалось связаться с сервером. Убедитесь, что сервер запущен, и повторите загрузку.' : err.message);
    } finally { setBusy(false); }
  }

  useEffect(() => { load(false); }, []);

  async function save() {
    if (parsed.error || !etag) return;
    setBusy(true); setError(''); setNotice('');
    try {
      const response = await fetch(endpoint, {
        method: 'PUT', headers: { 'Content-Type': 'application/json', 'If-Match': etag }, body: text,
      });
      if (!response.ok) throw new Error(await responseError(response));
      const revision = response.headers.get('ETag');
      if (!revision) {
        setEtag('');
        throw new Error('Сервер принял изменения, но не вернул ETag. Скопируйте текст и загрузите манифест заново перед следующим сохранением.');
      }
      setEtag(revision); setSavedText(text); setNotice('Изменения сохранены.'); setAddedPlatform('');
    } catch (err) {
      setError(err instanceof TypeError ? 'Не удалось связаться с сервером. Изменения остались в редакторе; проверьте соединение и загрузите актуальную версию перед повторной попыткой.' : err.message);
    } finally { setBusy(false); }
  }

  function changeName(value) {
    const updated = updateCanonicalName(text, selected, value);
    if (updated === text && value !== game?.canonical_name) {
      setError('Не удалось изменить имя: проверьте повторяющиеся поля contents и canonical_name в JSON.');
    } else { setText(updated); setError(''); setNotice(''); }
  }

  function submitPlatform(event) {
    event.preventDefault();
    const platform = platformMode === 'preset'
      ? knownPlatforms.find(item => item.libretro_name === preset)
      : customPlatform;
    if (!platform) { setPlatformError('Выберите платформу из списка.'); return; }
    const result = addPlatform(text, platform);
    if (result.error) { setPlatformError(result.error); return; }
    setText(result.text); setPlatformError(''); setError(''); setNotice('');
    setAddedPlatform(`${platform.name} (${platform.id}; ${platform.libretro_name})`);
  }

  return <div className="app">
    <header className="header">
      <div><p className="eyebrow">RETROARCH MARKETPLACE</p><h1>Управление каталогом</h1><p className="subtitle">Локальный редактор манифеста</p></div>
      <div className="header-actions">
        <span className={`status ${dirty ? 'unsaved' : ''}`} role="status">{!loaded ? 'Нет данных' : dirty ? '● Есть изменения' : '● Сохранено'}</span>
        <button className="secondary" onClick={() => load()} disabled={busy}>Загрузить заново</button>
        <button className="primary" onClick={save} disabled={busy || !dirty || !!parsed.error || !etag}>Сохранить</button>
      </div>
    </header>
    <main>
      {error && <div className="alert error" role="alert">{error}</div>}
      {notice && <div className="alert success" role="status">{notice}</div>}
      {busy && <p role="status" className="working">Обращение к серверу…</p>}
      {loaded && <>
        <section className="workspace" aria-label="Каталог игр">
          <aside className="panel games-panel">
            <div className="panel-heading"><div><p className="eyebrow">КАТАЛОГ</p><h2>Игры</h2></div><span className="count">{games.length}</span></div>
            {parsed.error ? <p className="muted">Исправьте JSON, чтобы просмотреть игры.</p> : games.length === 0 ? <p className="muted">В манифесте нет игр.</p> :
              <div className="game-list">{games.map((item, index) => <button key={index} className={`game-item ${selected === index ? 'selected' : ''}`} onClick={() => setSelected(index)} aria-pressed={selected === index}>
                <span className="game-title">{display(item?.canonical_name, display(item?.id, `Игра ${index + 1}`))}</span><span className="game-platform">{platformName(item?.platform_id)}</span>
              </button>)}</div>}
          </aside>
          <section className="panel details">
            <p className="eyebrow">ВЫБРАННАЯ ИГРА</p><h2>Сведения</h2>
            {game && typeof game === 'object' && !Array.isArray(game) ? <>
              <label htmlFor="canonical">Canonical Name <span>— основное название</span></label>
              <input id="canonical" value={typeof game.canonical_name === 'string' ? game.canonical_name : ''} onChange={event => changeName(event.target.value)} disabled={busy} />
              <dl><div><dt>Идентификатор</dt><dd>{display(game.id, 'Не указан')}</dd></div><div><dt>Платформа</dt><dd>{platformName(game.platform_id)}</dd></div><div><dt>Отображаемое название</dt><dd>{display(game.display_name, 'Не указано')}</dd></div><div><dt>Диски</dt><dd>{Array.isArray(game.discs) ? game.discs.length : 'Не указаны'}</dd></div></dl>
              <p className="hint">Остальные поля, диски и файлы можно изменить в полном JSON ниже.</p>
            </> : <p className="muted">Выберите игру в списке.</p>}
          </section>
        </section>
        <section className="panel platform-panel" aria-labelledby="platform-heading">
          <div className="panel-heading"><div><p className="eyebrow">КАТАЛОГ</p><h2 id="platform-heading">Платформы</h2></div><span className="count">{platforms.length}</span></div>
          {parsed.error ? <p className="muted">Исправьте JSON, чтобы просмотреть платформы.</p> : platforms.length === 0 ? <p className="muted">В манифесте нет платформ.</p> :
            <ul className="platform-list">{platforms.map((item, index) => <li key={index}><strong>{display(item?.name, 'Без названия')}</strong> <span>{display(item?.id, 'Без ID')} · {display(item?.libretro_name, 'Без libretro name')}</span></li>)}</ul>}
          <form className="platform-form" onSubmit={submitPlatform}>
            <fieldset disabled={busy}>
              <legend>Добавить платформу</legend>
              <div className="platform-modes">
                <label><input type="radio" name="platform-mode" value="preset" checked={platformMode === 'preset'} onChange={() => { setPlatformMode('preset'); setPlatformError(''); }} /> Из списка</label>
                <label><input type="radio" name="platform-mode" value="custom" checked={platformMode === 'custom'} onChange={() => { setPlatformMode('custom'); setPlatformError(''); }} /> Своя платформа</label>
              </div>
              {platformMode === 'preset' ? <>
                <label htmlFor="platform-preset">Платформа (libretro name)</label>
                <select id="platform-preset" value={preset} onChange={event => setPreset(event.target.value)}>
                  <option value="">Выберите платформу</option>
                  {knownPlatforms.map(item => <option key={item.libretro_name} value={item.libretro_name}>{item.libretro_name}</option>)}
                </select>
              </> : <>
                {[['id', 'ID'], ['name', 'Название'], ['libretro_name', 'Libretro name']].map(([key, label]) => <div key={key}>
                  <label htmlFor={`platform-${key}`}>{label}</label>
                  <input id={`platform-${key}`} value={customPlatform[key]} onChange={event => setCustomPlatform({ ...customPlatform, [key]: event.target.value })} />
                </div>)}
              </>}
              <button className="secondary" type="submit">Добавить платформу</button>
            </fieldset>
          </form>
          {platformError && <p className="json-error" role="alert">{platformError}</p>}
          {addedPlatform && <p role="status">Добавлена платформа: {addedPlatform}. Сохраните изменения кнопкой «Сохранить».</p>}
        </section>
        <section className="panel json-panel">
          <div className="json-heading"><div><p className="eyebrow">РАСШИРЕННОЕ РЕДАКТИРОВАНИЕ</p><h2>Полный манифест JSON</h2><p className="muted">Здесь доступны все поля, включая платформы, диски и файлы. Изменения синхронизированы с карточкой игры.</p></div></div>
          <label htmlFor="manifest-json" className="sr-only">Полный манифест JSON</label>
          <textarea id="manifest-json" value={text} onChange={event => { setText(event.target.value); setError(''); setNotice(''); setAddedPlatform(''); setPlatformError(''); }} spellCheck="false" disabled={busy} aria-invalid={!!parsed.error} aria-describedby={parsed.error ? 'json-error' : undefined} />
          {parsed.error && <p id="json-error" className="json-error" role="alert">Некорректный JSON: {parsed.error}. Исправьте синтаксис перед сохранением.</p>}
        </section>
      </>}
    </main>
  </div>;
}

createRoot(document.getElementById('root')).render(<App />);
