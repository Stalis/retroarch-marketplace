import React, { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { validateGame } from './game.js';
import { initialLocale, messages, persistLocale } from './i18n.js';
import './style.css';

const api = '/api/admin';
const pageSize = 50;
const json = { 'Content-Type': 'application/json' };
const gamePath = id => `${api}/games/${encodeURIComponent(id)}`;

const blankDisc = () => ({ entrypoint: '', files: [{ source: '', path: '' }] });
const blankGame = () => ({ id: '', canonical_name: '', display_name: '', platform_id: '', playlist: '', discs: [blankDisc()] });
// The form keeps optional fields as '' so inputs stay controlled; the API omits them.
const toForm = game => ({ ...game, display_name: game.display_name ?? '', playlist: game.playlist ?? '' });
const toBody = ({ display_name, playlist, ...game }) => ({ ...game, ...(display_name ? { display_name } : {}), ...(playlist ? { playlist } : {}) });

async function responseError(response) {
  return { status: response.status, detail: (await response.text()).trim() };
}

async function getJSON(url) {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) throw await responseError(response);
  return response;
}

function message(value, t) {
  if (typeof value === 'string') return t[value] || value;
  if (!value) return '';
  const { status, detail } = value;
  if (status === 412) return t.conflict;
  if (status === 403) return t.forbidden + (detail ? ` ${detail}` : '');
  if (status === 400 || status === 409) return detail || t.serverError(status);
  return t.serverError(status) + (detail ? `: ${detail}` : `. ${t.retry}`);
}

const networkOr = (err, key) => err instanceof TypeError ? key : err;

function App() {
  const [locale, setLocale] = useState(initialLocale);
  const t = messages[locale];
  const [activeTab, setActiveTab] = useState('games');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [serverVersion, setServerVersion] = useState('');
  const [platforms, setPlatforms] = useState([]);
  const [known, setKnown] = useState([]);
  const [query, setQuery] = useState('');
  const [games, setGames] = useState([]);
  const [hasMore, setHasMore] = useState(false);
  const [editing, setEditing] = useState(null); // { id, etag, available } of the loaded game; null = new game
  const [draft, setDraft] = useState(blankGame);
  const [savedDraft, setSavedDraft] = useState(() => JSON.stringify(blankGame()));
  const [gameError, setGameError] = useState('');
  const [missing, setMissing] = useState(null);
  const [conflict, setConflict] = useState(false);
  const [lookup, setLookup] = useState(null);
  const [platformMode, setPlatformMode] = useState('preset');
  const [preset, setPreset] = useState('');
  const [customPlatform, setCustomPlatform] = useState({ id: '', name: '', libretro_name: '' });
  const [platformError, setPlatformError] = useState('');
  const [addedPlatform, setAddedPlatform] = useState('');
  const listRequest = useRef(0);
  const lookupRequest = useRef(0);
  const dirty = JSON.stringify(draft) !== savedDraft;
  const platformOf = id => platforms.find(platform => platform.id === id);
  const libretroName = platformOf(draft.platform_id)?.libretro_name;
  const lookupCurrent = lookup && lookup.name === draft.canonical_name && lookup.platform === libretroName;
  // Exact listing match only: RetroArch fetches cover art by this exact name.
  const nameMismatch = lookupCurrent && lookup.status === 'done' && !lookup.candidates.includes(draft.canonical_name);

  useEffect(() => { document.documentElement.lang = locale; document.title = t.title; persistLocale(locale); }, [locale]);
  useEffect(() => {
    function warn(event) { if (dirty) { event.preventDefault(); event.returnValue = ''; } }
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  useEffect(() => { setMissing(null); }, [draft]);
  useEffect(() => {
    if (!libretroName || !draft.canonical_name.trim()) return;
    const timer = setTimeout(findCanonicalNames, 500);
    return () => clearTimeout(timer);
  }, [draft.canonical_name, libretroName]);

  async function run(action, networkKey = 'loadNetwork') {
    setBusy(true); setError(''); setNotice('');
    try { await action(); } catch (err) { setError(networkOr(err, networkKey)); } finally { setBusy(false); }
  }

  async function loadPlatforms() {
    const response = await getJSON(`${api}/platforms`);
    setServerVersion(response.headers.get('X-Catalog-Version') || '');
    setPlatforms(await response.json());
  }

  async function loadGames(skip = 0) {
    const request = ++listRequest.current;
    const params = new URLSearchParams({ q: query, skip, count: pageSize });
    const page = await (await getJSON(`${api}/games?${params}`)).json();
    if (request !== listRequest.current) return; // a newer search superseded this one
    setGames(current => skip ? [...current, ...page] : page);
    setHasMore(page.length === pageSize);
  }

  useEffect(() => {
    run(async () => {
      await loadPlatforms();
      setKnown(await (await getJSON(`${api}/known-platforms`)).json());
    });
  }, []);
  useEffect(() => {
    const timer = setTimeout(() => loadGames().catch(err => setError(networkOr(err, 'loadNetwork'))), 300);
    return () => clearTimeout(timer);
  }, [query]);

  function setForm(game, current) {
    const form = toForm(game);
    setDraft(form); setSavedDraft(JSON.stringify(form)); setEditing(current);
    setGameError(''); setMissing(null); setConflict(false); setLookup(null);
  }

  async function fetchGame(id) {
    const response = await getJSON(gamePath(id));
    const body = await response.json();
    setForm(body.game, { id, etag: response.headers.get('ETag'), available: body.available });
  }

  async function openGame(id, confirmDiscard = true) {
    if (confirmDiscard && dirty && !window.confirm(t.discard)) return;
    await run(() => fetchGame(id));
  }

  function newGame() {
    if (dirty && !window.confirm(t.discard)) return;
    setForm(blankGame(), null);
  }

  async function save(allowMissing = false) {
    const invalid = validateGame(draft, locale);
    if (invalid) { setGameError(invalid); return; }
    setGameError('');
    const suffix = allowMissing ? '?allow_missing=1' : '';
    await run(async () => {
      const response = editing
        ? await fetch(gamePath(editing.id) + suffix, { method: 'PUT', headers: { ...json, 'If-Match': editing.etag }, body: JSON.stringify(toBody(draft)) })
        : await fetch(`${api}/games${suffix}`, { method: 'POST', headers: json, body: JSON.stringify(toBody(draft)) });
      if (response.status === 422 && !allowMissing) {
        const body = await response.json().catch(() => null);
        if (Array.isArray(body?.missing)) { setMissing(body.missing); return; }
      }
      if (response.status === 412) { setConflict(true); return; }
      if (!response.ok) throw await responseError(response);
      await Promise.all([fetchGame(draft.id), loadGames(), loadPlatforms()]);
      setNotice('savedNotice');
    }, 'saveNetwork');
  }

  async function deleteGame() {
    if (!window.confirm(t.confirmDelete)) return;
    await run(async () => {
      const response = await fetch(gamePath(editing.id), { method: 'DELETE', headers: { 'If-Match': editing.etag } });
      if (response.status === 412) { setConflict(true); return; }
      if (!response.ok) throw await responseError(response);
      setForm(blankGame(), null);
      await Promise.all([loadGames(), loadPlatforms()]);
      setNotice('savedNotice');
    }, 'saveNetwork');
  }

  async function findCanonicalNames() {
    const request = { name: draft.canonical_name, platform: libretroName };
    const id = ++lookupRequest.current;
    // A newer lookup supersedes this one; its late result must not replace the newer state.
    const settle = state => { if (id === lookupRequest.current) setLookup({ ...request, ...state }); };
    settle({ status: 'loading' });
    try {
      const params = new URLSearchParams({ platform: request.platform, q: request.name });
      const response = await fetch(`${api}/canonical-names?${params}`);
      if (!response.ok) throw new Error('lookup');
      const candidates = await response.json();
      if (!Array.isArray(candidates) || !candidates.every(name => typeof name === 'string')) throw new Error('lookup');
      settle({ status: 'done', candidates });
    } catch {
      settle({ status: 'error' });
    }
  }

  async function submitPlatform(event) {
    event.preventDefault();
    const platform = platformMode === 'preset' ? known.find(item => item.libretro_name === preset) : customPlatform;
    if (!platform) { setPlatformError('choosePlatformError'); return; }
    setPlatformError(''); setAddedPlatform('');
    await run(async () => {
      const response = await fetch(`${api}/platforms`, { method: 'POST', headers: json, body: JSON.stringify(platform) });
      if (!response.ok) { setPlatformError(await responseError(response)); return; }
      setAddedPlatform(`${platform.name} (${platform.id}; ${platform.libretro_name})`);
      await loadPlatforms();
    }, 'saveNetwork');
  }

  async function deletePlatform(id) {
    setAddedPlatform(''); setPlatformError('');
    await run(async () => {
      const response = await fetch(`${api}/platforms/${encodeURIComponent(id)}`, { method: 'DELETE' });
      if (response.status === 409) { setError('platformInUse'); return; }
      if (!response.ok) throw await responseError(response);
      await loadPlatforms();
    }, 'saveNetwork');
  }

  function updateDisc(index, change) {
    setDraft(current => ({ ...current, discs: current.discs.map((disc, i) => i === index ? { ...disc, ...change } : disc) }));
  }

  return <div className="app">
    <header className="header">
      <div><p className="eyebrow">RETROARCH MARKETPLACE{serverVersion ? ` • ${serverVersion}` : ''}</p><h1>{t.title}</h1><p className="subtitle">{t.subtitle}</p></div>
      <div className="header-actions">
        <label className="language-selector">{t.language} <select value={locale} onChange={event => { setLocale(event.target.value); setGameError(''); }}><option value="ru">Русский</option><option value="en">English</option></select></label>
      </div>
    </header>
    <main>
      {error && <div className="alert error" role="alert">{message(error, t)}</div>}
      {notice && <div className="alert success" role="status">{message(notice, t)}</div>}
      {busy && <p role="status" className="working">{t.working}</p>}
      <nav className="tab-bar" aria-label="Sections">
        <button type="button" className={`tab-item ${activeTab === 'games' ? 'active' : ''}`} onClick={() => setActiveTab('games')}>{t.tabGames}</button>
        <button type="button" className={`tab-item ${activeTab === 'platforms' ? 'active' : ''}`} onClick={() => setActiveTab('platforms')}>
          {t.tabPlatforms} <span className="tab-count">{platforms.length}</span>
        </button>
      </nav>

      {activeTab === 'games' && <section className="workspace" aria-label={t.catalogGames}>
        <aside className="panel games-panel">
          <div className="panel-heading">
            <div><p className="eyebrow">{t.catalog}</p><h2>{t.games}</h2></div>
            <button type="button" className="secondary btn-small" onClick={newGame} disabled={busy}>{t.addGame}</button>
          </div>
          <div className="search-box">
            <input type="search" className="search-input" placeholder={t.searchPlaceholder} aria-label={t.searchPlaceholder} value={query} onChange={event => setQuery(event.target.value)} />
          </div>
          {games.length === 0 ? <p className="muted">{query ? t.noSearchResults : t.noGames}</p> :
            <div className="game-list">{games.map(item => <button key={item.id} className={`game-item ${editing?.id === item.id ? 'selected' : ''}`} onClick={() => openGame(item.id)} aria-pressed={editing?.id === item.id}>
              <span className="game-title">{item.canonical_name}</span>
              <div className="game-meta">
                <span className="game-platform">{platformOf(item.platform_id)?.name || item.platform_id}</span>
                {!item.available && <span className="badge warning">{t.unavailable}</span>}
              </div>
            </button>)}</div>}
          {hasMore && <button type="button" className="secondary btn-small" disabled={busy} onClick={() => run(() => loadGames(games.length))}>{t.loadMore}</button>}
        </aside>

        <section className="panel details" aria-labelledby="game-heading">
          <div className="panel-heading">
            <div><p className="eyebrow">{editing ? editing.id : t.addGame}</p><h2 id="game-heading">{editing ? draft.canonical_name || editing.id : t.gameData}</h2></div>
            <div className="game-meta">
              {editing && !editing.available && <span className="badge warning">{t.unavailable}</span>}
              {nameMismatch && <span className="badge warning">{t.nameMismatch}</span>}
              {dirty && <span className="status unsaved">{t.unsaved}</span>}
            </div>
          </div>
          <p className="hint">{t.gameHint}</p>
          {conflict && <div className="alert error" role="alert">{t.conflict}{' '}
            <button type="button" className="secondary btn-small" disabled={busy} onClick={() => openGame(editing.id, false)}>{t.reload}</button>
          </div>}
          {missing && <section className="alert error" aria-labelledby="missing-heading">
            <h2 id="missing-heading">{t.missingTitle}</h2>
            <p>{t.missingWarning}</p>
            <ul>{missing.map((file, index) => <li key={index}>{file.id}: {file.path}</li>)}</ul>
            <button className="secondary" disabled={busy} onClick={() => save(true)}>{t.saveAnyway}</button>{' '}
            <button className="secondary" disabled={busy} onClick={() => setMissing(null)}>{t.backEdit}</button>
          </section>}
          <form className="game-form-panel" onSubmit={event => { event.preventDefault(); save(); }} noValidate>
            <fieldset disabled={busy}>
              <div className="grid-fields">
                <div>
                  <label htmlFor="game-id">ID</label>
                  <input id="game-id" value={draft.id} readOnly={!!editing} onChange={event => setDraft({ ...draft, id: event.target.value })} />
                </div>
                <div>
                  <label htmlFor="game-canonical_name">{t.canonicalName}</label>
                  <input id="game-canonical_name" value={draft.canonical_name} onChange={event => setDraft({ ...draft, canonical_name: event.target.value })} />
                  <button className="secondary btn-small" type="button" onClick={findCanonicalNames} disabled={lookup?.status === 'loading' || !libretroName || !draft.canonical_name.trim()}>{t.lookupCanonical}</button>
                  {lookupCurrent && lookup.status === 'loading' && <p role="status" className="hint">{t.lookupLoading}</p>}
                  {lookupCurrent && lookup.status === 'error' && <p role="alert" className="hint">{t.lookupError}</p>}
                  {lookupCurrent && lookup.status === 'done' && nameMismatch && (lookup.candidates.length === 0 ? <p role="status" className="hint">{t.nameMismatchHint} {t.lookupEmpty}</p> :
                    <div><p role="status" className="hint">{t.nameMismatchHint}</p>{lookup.candidates.map(candidate => <button className="secondary btn-small" type="button" key={candidate} onClick={() => setDraft({ ...draft, canonical_name: candidate })}>{candidate}</button>)}</div>)}
                </div>
                <div>
                  <label htmlFor="game-display_name">{t.displayOptional}</label>
                  <input id="game-display_name" value={draft.display_name} onChange={event => setDraft({ ...draft, display_name: event.target.value })} />
                </div>
                <div>
                  <label htmlFor="game-platform">{t.platform}</label>
                  <select id="game-platform" value={draft.platform_id} onChange={event => setDraft({ ...draft, platform_id: event.target.value })}>
                    <option value="">{t.choosePlatform}</option>
                    {platforms.map(item => <option key={item.id} value={item.id}>{item.name} ({item.id})</option>)}
                  </select>
                </div>
              </div>
              {draft.discs.map((disc, index) => <fieldset className="disc-form" key={index}>
                <div className="disc-header">
                  <legend>{t.disc(index + 1)}</legend>
                  <button type="button" className="btn-small danger" onClick={() => setDraft({ ...draft, discs: draft.discs.filter((_, i) => i !== index) })}>{t.removeDisc(index + 1)}</button>
                </div>
                <div className="entrypoint-field">
                  <label htmlFor={`disc-${index}-entrypoint`}>{t.entrypoint}</label>
                  <input id={`disc-${index}-entrypoint`} value={disc.entrypoint} onChange={event => updateDisc(index, { entrypoint: event.target.value })} />
                </div>
                <div className="files-container">
                  {disc.files.map((file, fileIndex) => <div className="file-row" key={fileIndex}>
                    <div className="file-inputs">
                      <div>
                        <label htmlFor={`disc-${index}-source-${fileIndex}`}>{t.sourceFile(fileIndex + 1)}</label>
                        <input id={`disc-${index}-source-${fileIndex}`} value={file.source} onChange={event => updateDisc(index, { files: disc.files.map((item, i) => i === fileIndex ? { ...item, source: event.target.value } : item) })} />
                      </div>
                      <div>
                        <label htmlFor={`disc-${index}-path-${fileIndex}`}>{t.filePath(fileIndex + 1)}</label>
                        <input id={`disc-${index}-path-${fileIndex}`} value={file.path} onChange={event => updateDisc(index, { files: disc.files.map((item, i) => i === fileIndex ? { ...item, path: event.target.value } : item) })} />
                      </div>
                    </div>
                    <button type="button" className="btn-icon danger" title={t.removeFile(fileIndex + 1, index + 1)} onClick={() => updateDisc(index, { files: disc.files.filter((_, i) => i !== fileIndex) })}>✕</button>
                  </div>)}
                </div>
                <button type="button" className="secondary btn-small" onClick={() => updateDisc(index, { files: [...disc.files, { source: '', path: '' }] })}>{t.addFile(index + 1)}</button>
              </fieldset>)}
              <div className="game-form-actions">
                <button type="button" className="secondary btn-small" onClick={() => setDraft({ ...draft, discs: [...draft.discs, blankDisc()] })}>{t.addDisc}</button>
              </div>
              <div>
                <label htmlFor="game-playlist">{draft.discs.length > 1 ? t.playlistRequired : t.playlistOptional}</label>
                <input id="game-playlist" value={draft.playlist} onChange={event => setDraft({ ...draft, playlist: event.target.value })} />
              </div>
              {gameError && <p className="json-error" role="alert">{gameError}</p>}
              <div className="submit-row">
                <button className="primary" type="submit" disabled={editing && !dirty}>{editing ? t.save : t.addGame}</button>
                {editing && <button type="button" className="btn-small danger" onClick={deleteGame}>{t.deleteGame}</button>}
              </div>
            </fieldset>
          </form>
        </section>
      </section>}

      {activeTab === 'platforms' && <section className="panel platform-panel" aria-labelledby="platform-heading">
        <div className="panel-heading"><div><p className="eyebrow">{t.catalog}</p><h2 id="platform-heading">{t.platforms}</h2></div><span className="count">{platforms.length}</span></div>
        {platforms.length === 0 ? <p className="muted">{t.noPlatforms}</p> :
          <ul className="platform-list">{platforms.map(item => <li key={item.id}>
            <strong>{item.name}</strong> <span>{item.id} · {item.libretro_name} · {t.publishedGames(item.games)}</span>{' '}
            <button type="button" className="btn-small danger" disabled={busy} onClick={() => deletePlatform(item.id)}>{t.deletePlatform}</button>
          </li>)}</ul>}
        <form className="platform-form" onSubmit={submitPlatform}>
          <fieldset disabled={busy}>
            <legend>{t.addPlatform}</legend>
            <div className="platform-modes">
              <label><input type="radio" name="platform-mode" value="preset" checked={platformMode === 'preset'} onChange={() => { setPlatformMode('preset'); setPlatformError(''); }} /> {t.fromList}</label>
              <label><input type="radio" name="platform-mode" value="custom" checked={platformMode === 'custom'} onChange={() => { setPlatformMode('custom'); setPlatformError(''); }} /> {t.customPlatform}</label>
            </div>
            {platformMode === 'preset' ? <>
              <label htmlFor="platform-preset">{t.platformLibretro}</label>
              <select id="platform-preset" value={preset} onChange={event => setPreset(event.target.value)}>
                <option value="">{t.choosePlatform}</option>
                {known.map(item => <option key={item.libretro_name} value={item.libretro_name}>{item.libretro_name}</option>)}
              </select>
            </> : <>
              {[['id', 'ID'], ['name', t.name], ['libretro_name', 'Libretro name']].map(([key, label]) => <div key={key}>
                <label htmlFor={`platform-${key}`}>{label}</label>
                <input id={`platform-${key}`} value={customPlatform[key]} onChange={event => setCustomPlatform({ ...customPlatform, [key]: event.target.value })} />
              </div>)}
            </>}
            <button className="primary" type="submit">{t.addPlatform}</button>
          </fieldset>
        </form>
        {platformError && <p className="json-error" role="alert">{message(platformError, t)}</p>}
        {addedPlatform && <p role="status">{t.platformAdded(addedPlatform)}</p>}
      </section>}
    </main>
  </div>;
}

createRoot(document.getElementById('root')).render(<App />);
