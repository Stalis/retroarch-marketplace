import React, { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { gameIssue, quickDisc, suggestedGameID } from './game.js';
import { validationMessage } from './validation-i18n.js';
import { BoxArt } from './BoxArt.jsx';
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

const emptyPlatform = { id: '', name: '', libretro_name: '' };
const simpleGame = game => game.discs.length === 1 && game.discs[0].files.length === 1;
const narrowScreen = () => window.matchMedia('(max-width: 840px)').matches;

function App() {
  const [locale, setLocale] = useState(initialLocale);
  const [theme, setTheme] = useState(() => document.documentElement.dataset.theme || 'system');
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
  const [listState, setListState] = useState('loading');
  const [hasMore, setHasMore] = useState(false);
  const [editorOpen, setEditorOpen] = useState(false);
  const [mobileEditor, setMobileEditor] = useState(false);
  const [editing, setEditing] = useState(null);
  const [draft, setDraft] = useState(blankGame);
  const [savedDraft, setSavedDraft] = useState(() => JSON.stringify(blankGame()));
  const [advanced, setAdvanced] = useState(false);
  const [manualID, setManualID] = useState(false);
  const [autoArchive, setAutoArchive] = useState(true);
  const [gameError, setGameError] = useState(null);
  const [missing, setMissing] = useState(null);
  const [conflict, setConflict] = useState(false);
  const [lookup, setLookup] = useState(null);
  const [platformForm, setPlatformForm] = useState(false);
  const [platformMode, setPlatformMode] = useState('preset');
  const [platformQuery, setPlatformQuery] = useState('');
  const [preset, setPreset] = useState('');
  const [customPlatform, setCustomPlatform] = useState(emptyPlatform);
  const [platformError, setPlatformError] = useState('');
  const [platformRowError, setPlatformRowError] = useState('');
  const [addedPlatform, setAddedPlatform] = useState('');
  const [focus, setFocus] = useState(null);
  const listRequest = useRef(0);
  const currentQuery = useRef('');
  const lookupRequest = useRef(0);
  const gameList = useRef(null);
  const listPosition = useRef(null);
  const dirty = JSON.stringify(draft) !== savedDraft;
  const platformOf = id => platforms.find(platform => platform.id === id);
  const libretroName = platformOf(draft.platform_id)?.libretro_name;
  const lookupCurrent = lookup?.name === draft.canonical_name && lookup?.platform === libretroName;
  const presets = known.filter(item => item.libretro_name.toLowerCase().includes(platformQuery.trim().toLowerCase()));

  useEffect(() => { document.documentElement.lang = locale; document.title = t.title; persistLocale(locale); }, [locale]);
  useEffect(() => {
    function warn(event) { if (dirty) { event.preventDefault(); event.returnValue = ''; } }
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  useEffect(() => {
    if (!focus || busy) return;
    if (focus.restore && listPosition.current) {
      if (gameList.current) gameList.current.scrollTop = listPosition.current.list;
      window.scrollTo(0, listPosition.current.page);
    }
    const target = document.getElementById(focus.id)
      || (focus.restore ? document.getElementById('game-search') : null);
    target?.focus({ preventScroll: true });
    if (!focus.restore) target?.scrollIntoView({ block: 'nearest' });
    setFocus(null);
  }, [focus, busy]);

  useEffect(() => {
    if (!editorOpen || !libretroName || !draft.canonical_name.trim() || lookupCurrent) return;
    const timer = setTimeout(findCanonicalNames, 500);
    return () => clearTimeout(timer);
  }, [editorOpen, draft.canonical_name, libretroName, lookupCurrent]);

  function changeTheme(value) {
    setTheme(value);
    if (value === 'system') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = value;
    try { localStorage.setItem('theme', value); } catch { /* Storage may be unavailable. */ }
  }

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
    const params = new URLSearchParams({ q: currentQuery.current.trim(), skip, count: pageSize });
    try {
      const page = await (await getJSON(`${api}/games?${params}`)).json();
      if (request !== listRequest.current) return;
      setGames(current => skip ? [...current, ...page] : page);
      setHasMore(page.length === pageSize);
      setListState('ready');
    } catch (err) {
      if (request !== listRequest.current) return;
      setListState(skip ? 'ready' : 'error');
      setError(networkOr(err, 'loadNetwork'));
    }
  }

  useEffect(() => {
    run(async () => {
      await loadPlatforms();
      setKnown(await (await getJSON(`${api}/known-platforms`)).json());
    });
  }, []);
  useEffect(() => {
    const timer = setTimeout(() => loadGames(), query ? 300 : 0);
    return () => clearTimeout(timer);
  }, [query]);

  function changeQuery(value) {
    if (value === query) return;
    currentQuery.current = value;
    listRequest.current++;
    setListState('loading');
    setQuery(value);
  }

  function setForm(game, current) {
    const form = toForm(game);
    lookupRequest.current++;
    setDraft(form); setSavedDraft(JSON.stringify(form)); setEditing(current);
    setAdvanced(!simpleGame(form));
    setManualID(!!current); setAutoArchive(!current);
    setGameError(null); setMissing(null); setConflict(false); setLookup(null);
  }

  async function fetchGame(id) {
    const response = await getJSON(gamePath(id));
    const body = await response.json();
    setForm(body.game, { id, etag: response.headers.get('ETag'), available: body.available, canonical_name: body.game.canonical_name, display_name: body.game.display_name ?? '' });
  }

  function showEditor(trigger) {
    if (trigger) listPosition.current = { id: trigger.id, page: window.scrollY, list: gameList.current?.scrollTop ?? 0 };
    setEditorOpen(true);
    setMobileEditor(true);
    if (narrowScreen()) window.scrollTo(0, 0);
    setFocus({ id: 'game-heading' });
  }

  function backToGames() {
    setMobileEditor(false);
    setFocus({ id: listPosition.current?.id || 'game-search', restore: true });
  }

  async function openGame(id, trigger) {
    if (editing?.id === id) { showEditor(trigger); return; }
    if (dirty && !window.confirm(t.discard)) return;
    await run(async () => {
      await fetchGame(id);
      showEditor(trigger);
    });
  }

  function newGame(event) {
    if (dirty && !window.confirm(t.discard)) return;
    setForm(blankGame(), null);
    showEditor(event.currentTarget);
  }

  function changeDraft(change) {
    setDraft(current => ({ ...current, ...change }));
    setGameError(null);
    setMissing(null);
  }

  function showGameError(issue) {
    setGameError(issue);
    const basicFields = ['game-platform', 'game-canonical_name'];
    if (simpleGame(draft)) basicFields.push('disc-0-source-0');
    if (!basicFields.includes(issue.field)) setAdvanced(true);
    setFocus({ id: issue.field });
  }

  async function save(allowMissing = false) {
    const invalid = gameIssue(draft);
    if (invalid) { showGameError(invalid); return; }
    setGameError(null);
    const suffix = allowMissing ? '?allow_missing=1' : '';
    await run(async () => {
      const response = editing
        ? await fetch(gamePath(editing.id) + suffix, { method: 'PUT', headers: { ...json, 'If-Match': editing.etag }, body: JSON.stringify(toBody(draft)) })
        : await fetch(`${api}/games${suffix}`, { method: 'POST', headers: json, body: JSON.stringify(toBody(draft)) });
      if (response.status === 422 && !allowMissing) {
        const body = await response.json().catch(() => null);
        if (Array.isArray(body?.missing)) {
          setMissing(body.missing);
          setFocus({ id: 'missing-heading' });
          return;
        }
      }
      if (response.status === 412) {
        setConflict(true);
        setFocus({ id: 'edit-conflict' });
        return;
      }
      if (!editing && response.status === 409) {
        showGameError({ field: 'game-id', label: 'duplicateID' });
        return;
      }
      if (!response.ok) throw await responseError(response);

      setSavedDraft(JSON.stringify(draft));
      setEditing({ id: draft.id, etag: response.headers.get('ETag'), available: !allowMissing, canonical_name: draft.canonical_name, display_name: draft.display_name });
      setMissing(null); setConflict(false);
      setNotice('savedGame');
      const refreshed = await Promise.allSettled([
        loadGames(),
        loadPlatforms(),
        getJSON(gamePath(draft.id)).then(async stored => {
          const game = await stored.json();
          setEditing(current => current?.etag === stored.headers.get('ETag') ? { ...current, available: game.available } : current);
        }),
      ]);
      if (refreshed.some(result => result.status === 'rejected')) setError('loadNetwork');
    }, 'saveNetwork');
  }

  async function reloadGame() {
    if (dirty && !window.confirm(t.confirmReload)) return;
    await run(async () => {
      await fetchGame(editing.id);
      setFocus({ id: 'game-heading' });
    });
  }

  async function deleteGame() {
    if (!window.confirm(t.confirmGameDelete(draft.display_name || draft.canonical_name || editing.id))) return;
    await run(async () => {
      const response = await fetch(gamePath(editing.id), { method: 'DELETE', headers: { 'If-Match': editing.etag } });
      if (response.status === 412) {
        setConflict(true);
        setFocus({ id: 'edit-conflict' });
        return;
      }
      if (!response.ok) throw await responseError(response);
      setForm(blankGame(), null);
      setEditorOpen(false);
      backToGames();
      setFocus({ id: 'game-search', restore: true });
      await Promise.all([loadGames(), loadPlatforms()]);
      setNotice('deletedGame');
    }, 'saveNetwork');
  }

  async function findCanonicalNames() {
    const request = { name: draft.canonical_name, platform: libretroName };
    const id = ++lookupRequest.current;
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

  function changeBoxArtQuery(change) {
    lookupRequest.current++;
    setLookup(null);
    changeDraft(change);
  }

  function selectCover(candidate) {
    lookupRequest.current++;
    setLookup({ ...lookup, name: candidate });
    changeDraft({ canonical_name: candidate });
    setFocus({ id: 'game-canonical_name' });
  }

  function openPlatformForm() {
    setPlatformMode('preset'); setPlatformQuery(''); setPreset(''); setCustomPlatform(emptyPlatform);
    setPlatformError(''); setAddedPlatform(''); setPlatformForm(true);
    setFocus({ id: 'platform-search' });
  }

  async function submitPlatform(event) {
    event.preventDefault();
    const platform = platformMode === 'preset' ? known.find(item => item.libretro_name === preset) : customPlatform;
    if (!platform) { setPlatformError('choosePlatformError'); setFocus({ id: 'platform-preset' }); return; }
    setPlatformError(''); setAddedPlatform('');
    await run(async () => {
      const response = await fetch(`${api}/platforms`, { method: 'POST', headers: json, body: JSON.stringify(platform) });
      if (!response.ok) { setPlatformError(await responseError(response)); setFocus({ id: 'platform-error' }); return; }
      await loadPlatforms();
      setAddedPlatform(platform.name);
      setPlatformForm(false);
      setFocus({ id: `platform-row-${platform.id}` });
    }, 'saveNetwork');
  }

  async function deletePlatform(platform) {
    if (!window.confirm(t.confirmPlatformDelete(platform.name))) return;
    setAddedPlatform(''); setPlatformError(''); setPlatformRowError('');
    await run(async () => {
      const response = await fetch(`${api}/platforms/${encodeURIComponent(platform.id)}`, { method: 'DELETE' });
      if (response.status === 409) {
        setPlatformRowError(platform.id);
        setFocus({ id: `platform-row-${platform.id}` });
        await loadPlatforms();
        return;
      }
      if (!response.ok) throw await responseError(response);
      await loadPlatforms();
      setNotice('deletedPlatform');
      setFocus({ id: 'add-platform' });
    }, 'saveNetwork');
  }

  function updateDisc(index, change) {
    changeDraft({ discs: draft.discs.map((disc, i) => i === index ? { ...disc, ...change } : disc) });
  }

  function changeSource(index, fileIndex, source) {
    if (!editing && autoArchive && simpleGame(draft)) {
      changeDraft({ discs: [quickDisc(source)], ...(!manualID && { id: suggestedGameID(draft.platform_id, source) }) });
      return;
    }
    const files = draft.discs[index].files.map((file, i) => i === fileIndex ? { ...file, source } : file);
    changeDraft({
      discs: draft.discs.map((disc, i) => i === index ? { ...disc, files } : disc),
      ...(!editing && !manualID && index === 0 && fileIndex === 0 && { id: suggestedGameID(draft.platform_id, source) }),
    });
  }

  function changeFilePath(index, fileIndex, path) {
    const disc = draft.discs[index];
    const entrypoint = disc.entrypoint === disc.files[fileIndex].path ? path : disc.entrypoint;
    setAutoArchive(false);
    updateDisc(index, { entrypoint, files: disc.files.map((file, i) => i === fileIndex ? { ...file, path } : file) });
  }

  function fixMissingPaths() {
    const missingPath = missing[0]?.path;
    const discIndex = draft.discs.findIndex(disc => disc.files.some(file => file.source === missingPath));
    const fileIndex = draft.discs[discIndex]?.files.findIndex(file => file.source === missingPath);
    if (!simpleGame(draft)) setAdvanced(true);
    setMissing(null);
    setFocus({ id: discIndex < 0 ? 'game-heading' : `disc-${discIndex}-source-${fileIndex}` });
  }

  const fieldStatus = id => gameError?.field === id
    ? { 'aria-invalid': true, 'aria-describedby': `${id}-error` }
    : {};
  const fieldError = id => gameError?.field === id && <p className="json-error field-error" role="alert" id={`${id}-error`}>
    {gameError.label ? t[gameError.label] : validationMessage(locale, gameError.message)}
  </p>;
  const hiddenBySearch = editing && query && listState === 'ready'
    && !games.some(game => game.id === editing.id)
    && ![editing.canonical_name, editing.display_name]
      .some(name => name?.toLowerCase().includes(query.trim().toLowerCase()));

  return <div className="app">
    <header className="header">
      <div><p className="eyebrow">RETROARCH MARKETPLACE{serverVersion ? ` · ${serverVersion}` : ''}</p><h1>{t.title}</h1></div>
      <div className="header-actions">
        <label className="language-selector">{t.language} <select value={locale} onChange={event => setLocale(event.target.value)}>
          <option value="ru">Русский</option><option value="en">English</option>
        </select></label>
        <label className="language-selector">{t.theme} <select value={theme} onChange={event => changeTheme(event.target.value)}>
          <option value="system">{t.systemTheme}</option><option value="light">{t.lightTheme}</option><option value="dark">{t.darkTheme}</option>
        </select></label>
      </div>
    </header>
    <main>
      <nav className="tab-bar" aria-label={t.sections}>
        <button type="button" disabled={busy} aria-current={activeTab === 'games' ? 'page' : undefined} className={`tab-item ${activeTab === 'games' ? 'active' : ''}`} onClick={() => setActiveTab('games')}>{t.tabGames}</button>
        <button type="button" disabled={busy} aria-current={activeTab === 'platforms' ? 'page' : undefined} className={`tab-item ${activeTab === 'platforms' ? 'active' : ''}`} onClick={() => setActiveTab('platforms')}>{t.tabPlatforms}</button>
      </nav>
      {error && <div className="alert error" role="alert">{message(error, t)}</div>}
      {notice && <div className="alert success" role="status">{message(notice, t)}</div>}
      {busy && <p role="status" className="sr-only">{t.working}</p>}

      <section hidden={activeTab !== 'games'} className={`workspace ${mobileEditor ? 'mobile-editor' : ''}`} aria-label={t.catalogGames}>
        <aside className="panel games-panel">
          <div className="panel-heading">
            <h2>{t.games}</h2>
            <button id="add-game" type="button" className="primary btn-small" onClick={newGame} disabled={busy}>{t.addGame}</button>
          </div>
          <div className="search-box">
            <input id="game-search" type="search" className="search-input" placeholder={t.searchPlaceholder} aria-label={t.searchPlaceholder} value={query} onChange={event => changeQuery(event.target.value)} />
          </div>
          {listState === 'loading' && <p className="muted" role="status">{t.searchingGames}</p>}
          {listState === 'ready' && !games.length && <p className="muted" role="status">{query.trim() ? t.noSearchResults : t.noGames}</p>}
          <div className="game-list" ref={gameList} hidden={listState !== 'ready'}>
            {games.map(item => <button key={item.id} id={`game-row-${item.id}`} type="button" className={`game-item ${editing?.id === item.id ? 'selected' : ''}`}
              disabled={busy} onClick={event => openGame(item.id, event.currentTarget)} aria-pressed={editing?.id === item.id}>
              <span className="game-title">{item.display_name || item.canonical_name}</span>
              {item.display_name && item.display_name !== item.canonical_name && <span className="muted">{item.canonical_name}</span>}
              <span className="game-meta">
                <span className="game-platform">{platformOf(item.platform_id)?.name || item.platform_id}</span>
                {!item.available && <span className="badge warning">{t.unavailable}</span>}
              </span>
            </button>)}
          </div>
          {listState === 'ready' && hasMore && <button type="button" className="secondary btn-small load-more" disabled={busy} onClick={() => run(() => loadGames(games.length))}>{t.loadMore}</button>}
        </aside>

        {editorOpen ? <section className="panel details" aria-labelledby="game-heading">
          <button type="button" className="secondary btn-small back-to-games" onClick={backToGames}>{t.backToGames}</button>
          <div className="panel-heading">
            <h2 id="game-heading" tabIndex={-1}>{editing ? draft.display_name || draft.canonical_name || editing.id : t.addGame}</h2>
            {editing && !editing.available && <span className="badge warning">{t.unavailable}</span>}
          </div>
          {hiddenBySearch && <div className="context-row">
            <p className="muted">{t.hiddenBySearch}</p>
            <button type="button" className="secondary btn-small" onClick={() => changeQuery('')}>{t.clearSearch}</button>
          </div>}
          {conflict && <div className="alert error" id="edit-conflict" tabIndex={-1} role="alert">
            <p>{t.conflict}</p>
            <button type="button" className="secondary btn-small" disabled={busy} onClick={reloadGame}>{t.reload}</button>
          </div>}
          {missing && <section className="alert error" aria-labelledby="missing-heading">
            <h3 id="missing-heading" tabIndex={-1}>{t.missingTitle}</h3>
            <p>{t.missingWarning}</p>
            <ul>{missing.map((file, index) => <li key={index}>{file.path}</li>)}</ul>
            <div className="context-row">
              <button type="button" className="secondary" disabled={busy} onClick={fixMissingPaths}>{t.fixPaths}</button>
              <button type="button" className="secondary" disabled={busy} onClick={() => save(true)}>{t.saveUnavailable}</button>
            </div>
          </section>}
          <form className="game-form-panel" onSubmit={event => { event.preventDefault(); save(); }} noValidate>
            <fieldset disabled={busy}>
              <div className="grid-fields">
                <div>
                  <label htmlFor="game-platform">{t.platform}</label>
                  <select id="game-platform" {...fieldStatus('game-platform')} value={draft.platform_id} onChange={event => {
                    const platform_id = event.target.value;
                    const source = draft.discs[0]?.files[0]?.source ?? '';
                    changeBoxArtQuery({ platform_id, ...(!manualID && !editing && { id: suggestedGameID(platform_id, source) }) });
                  }}>
                    <option value="">{t.choosePlatform}</option>
                    {platforms.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                  {fieldError('game-platform')}
                </div>
                <div>
                  <label htmlFor="game-canonical_name">{t.canonicalName}</label>
                  <input id="game-canonical_name" {...fieldStatus('game-canonical_name')} value={draft.canonical_name} onChange={event => changeBoxArtQuery({ canonical_name: event.target.value })} />
                  {fieldError('game-canonical_name')}
                </div>
              </div>
              {!advanced && simpleGame(draft) && <div>
                <label htmlFor="disc-0-source-0">{t.sourcePath}</label>
                <input id="disc-0-source-0" {...fieldStatus('disc-0-source-0')}
                  aria-describedby={gameError?.field === 'disc-0-source-0' ? 'source-hint disc-0-source-0-error' : 'source-hint'}
                  placeholder="SNES/game.sfc" value={draft.discs[0].files[0].source} onChange={event => changeSource(0, 0, event.target.value)} />
                {fieldError('disc-0-source-0')}
                <p id="source-hint" className="hint">{t.gameHint}</p>
              </div>}

              {libretroName && draft.canonical_name.trim() && <div className="boxart-section">
                <button className="secondary btn-small" type="button" onClick={findCanonicalNames} disabled={lookupCurrent && lookup.status === 'loading'}>{t.lookupCanonical}</button>
                <BoxArt lookup={lookupCurrent ? lookup : { status: 'loading' }} platform={libretroName} name={draft.canonical_name}
                  mock={serverVersion.endsWith('-mock')} onSelect={selectCover} t={t} />
              </div>}

              <div className="advanced-settings">
                <button className="secondary" type="button" aria-expanded={advanced} aria-controls="advanced-fields" onClick={() => setAdvanced(!advanced)}>{t.advanced}</button>
                <div id="advanced-fields" className="advanced-fields" hidden={!advanced}>
                  <div className="grid-fields">
                    <div>
                      <label htmlFor="game-id">ID</label>
                      <input id="game-id" {...fieldStatus('game-id')} value={draft.id} readOnly={!!editing} onChange={event => {
                        setManualID(true); changeDraft({ id: event.target.value });
                      }} />
                      {fieldError('game-id')}
                    </div>
                    <div>
                      <label htmlFor="game-display_name">{t.displayOptional}</label>
                      <input id="game-display_name" {...fieldStatus('game-display_name')} value={draft.display_name} onChange={event => changeDraft({ display_name: event.target.value })} />
                      {fieldError('game-display_name')}
                    </div>
                  </div>
                  <p className="hint">{t.gameHint}</p>
                  {advanced && draft.discs.map((disc, index) => <fieldset className="disc-form" key={index}>
                    <legend>{t.disc(index + 1)}</legend>
                    <div className="disc-header">
                      <button type="button" className="btn-small danger" disabled={draft.discs.length === 1} onClick={() => {
                        setAutoArchive(false); changeDraft({ discs: draft.discs.filter((_, i) => i !== index) });
                      }}>{t.removeDisc(index + 1)}</button>
                    </div>
                    <div className="files-container">
                      {disc.files.map((file, fileIndex) => {
                        const sourceID = `disc-${index}-source-${fileIndex}`;
                        const pathID = `disc-${index}-path-${fileIndex}`;
                        return <div className="file-row" key={fileIndex}>
                          <div className="file-inputs">
                            <div>
                              <label htmlFor={sourceID}>{t.sourceFile(fileIndex + 1)}</label>
                              <input id={sourceID} {...fieldStatus(sourceID)} value={file.source} onChange={event => changeSource(index, fileIndex, event.target.value)} />
                              {fieldError(sourceID)}
                            </div>
                            <div>
                              <label htmlFor={pathID}>{t.filePath(fileIndex + 1)}</label>
                              <input id={pathID} {...fieldStatus(pathID)} value={file.path} onChange={event => changeFilePath(index, fileIndex, event.target.value)} />
                              {fieldError(pathID)}
                            </div>
                          </div>
                          <button type="button" className="btn-icon danger" disabled={disc.files.length === 1}
                            aria-label={t.removeFile(fileIndex + 1, index + 1)} onClick={() => {
                              setAutoArchive(false); updateDisc(index, { files: disc.files.filter((_, i) => i !== fileIndex) });
                            }}>×</button>
                        </div>;
                      })}
                    </div>
                    <button id={`disc-${index}-add-file`} type="button" className="secondary btn-small" {...fieldStatus(`disc-${index}-add-file`)} onClick={() => {
                      setAutoArchive(false); updateDisc(index, { files: [...disc.files, { source: '', path: '' }] });
                    }}>{t.addFile(index + 1)}</button>
                    {fieldError(`disc-${index}-add-file`)}
                    <div className="entrypoint-field">
                      <label htmlFor={`disc-${index}-entrypoint`}>{t.entrypoint}</label>
                      <select id={`disc-${index}-entrypoint`} {...fieldStatus(`disc-${index}-entrypoint`)} value={disc.entrypoint}
                        onChange={event => { setAutoArchive(false); updateDisc(index, { entrypoint: event.target.value }); }}>
                        <option value="">{t.chooseEntrypoint}</option>
                        {disc.entrypoint && !disc.files.some(file => file.path === disc.entrypoint) && <option value={disc.entrypoint} disabled>{disc.entrypoint}</option>}
                        {[...new Set(disc.files.map(file => file.path).filter(Boolean))].map(path => <option key={path} value={path}>{path}</option>)}
                      </select>
                      {fieldError(`disc-${index}-entrypoint`)}
                    </div>
                  </fieldset>)}
                  <div>
                    <button id="add-disc" type="button" className="secondary btn-small" {...fieldStatus('add-disc')} onClick={() => {
                      setAutoArchive(false); changeDraft({ discs: [...draft.discs, blankDisc()] });
                    }}>{t.addDisc}</button>
                    {fieldError('add-disc')}
                  </div>
                  {(draft.discs.length > 1 || !!JSON.parse(savedDraft).playlist || !!draft.playlist) && <div>
                    <label htmlFor="game-playlist">{draft.discs.length > 1 ? t.playlistRequired : t.playlistOptional}</label>
                    <input id="game-playlist" {...fieldStatus('game-playlist')} value={draft.playlist} onChange={event => changeDraft({ playlist: event.target.value })} />
                    {fieldError('game-playlist')}
                  </div>}
                </div>
              </div>
            </fieldset>
            <div className="submit-row">
              <button className="primary" type="submit" disabled={busy || (!!editing && !dirty)}>{editing ? t.save : t.addGame}</button>
              {dirty && <span className="status unsaved">{t.unsaved}</span>}
              {editing && <button type="button" className="btn-small danger delete-game" disabled={busy} onClick={deleteGame}>{t.deleteGame}</button>}
            </div>
          </form>
        </section> : <div className="panel details empty-editor"><p className="muted">{t.chooseGame}</p></div>}
      </section>

      {activeTab === 'platforms' && <section className={`workspace platforms-workspace ${platformForm ? 'mobile-platform-form' : ''}`} aria-label={t.platforms}>
        <section className="panel platform-panel" aria-labelledby="platform-heading">
          <div className="panel-heading">
            <h2 id="platform-heading">{t.platforms}</h2>
            <button id="add-platform" type="button" className="primary btn-small" disabled={busy} onClick={openPlatformForm}>{t.addPlatform}</button>
          </div>
          {addedPlatform && <p className="alert success" role="status">{t.platformAdded(addedPlatform)}</p>}
          {!platforms.length ? <p className="muted">{t.noPlatforms}</p> :
            <ul className="platform-list">{platforms.map(item => <li key={item.id} id={`platform-row-${item.id}`} tabIndex={-1}>
              <strong>{item.name}</strong>
              <span>{item.libretro_name}</span>
              <span>{t.platformCounts(item.total_games, item.games)}</span>
              {platformRowError === item.id ? <p className="json-error" role="alert">{t.platformInUse}</p>
                : item.total_games > 0 ? <span>{t.platformInUse}</span> : null}
              <button type="button" className="btn-small danger" disabled={busy || item.total_games > 0}
                aria-label={`${t.deletePlatform}: ${item.name}`} onClick={() => deletePlatform(item)}>{t.deletePlatform}</button>
            </li>)}</ul>}
        </section>
        {platformForm && <section className="panel platform-editor" aria-labelledby="platform-form-heading">
          <h2 id="platform-form-heading">{t.addPlatform}</h2>
          <form className="platform-form" onSubmit={submitPlatform}>
            <fieldset disabled={busy}>
              <div className="platform-modes">
                <label><input type="radio" name="platform-mode" value="preset" checked={platformMode === 'preset'} onChange={() => { setPlatformMode('preset'); setPlatformError(''); }} /> {t.fromList}</label>
                <label><input type="radio" name="platform-mode" value="custom" checked={platformMode === 'custom'} onChange={() => { setPlatformMode('custom'); setPlatformError(''); }} /> {t.customPlatform}</label>
              </div>
              {platformMode === 'preset' ? <>
                <div>
                  <label htmlFor="platform-search">{t.platformSearch}</label>
                  <input id="platform-search" type="search" value={platformQuery} onChange={event => { setPlatformQuery(event.target.value); setPreset(''); setPlatformError(''); }} />
                </div>
                <div>
                  <label htmlFor="platform-preset">{t.platformLibretro}</label>
                  <select id="platform-preset" value={preset} onChange={event => setPreset(event.target.value)}>
                    <option value="">{t.choosePlatform}</option>
                    {presets.map(item => <option key={item.libretro_name} value={item.libretro_name}>{item.libretro_name}</option>)}
                  </select>
                  {!presets.length && <p className="hint" role="status">{t.noPresets}</p>}
                </div>
              </> : [['id', 'ID'], ['name', t.name], ['libretro_name', t.platformLibretro]].map(([key, label]) => <div key={key}>
                <label htmlFor={`platform-${key}`}>{label}</label>
                <input required id={`platform-${key}`} value={customPlatform[key]} onChange={event => setCustomPlatform({ ...customPlatform, [key]: event.target.value })} />
              </div>)}
              {platformError && <p id="platform-error" className="json-error" tabIndex={-1} role="alert">{message(platformError, t)}</p>}
              <div className="context-row">
                <button className="primary" type="submit">{t.addPlatform}</button>
                <button className="secondary" type="button" onClick={() => {
                  setPlatformForm(false); setFocus({ id: 'add-platform' });
                }}>{t.cancel}</button>
              </div>
            </fieldset>
          </form>
        </section>}
      </section>}
    </main>
  </div>;
}

createRoot(document.getElementById('root')).render(<App />);
