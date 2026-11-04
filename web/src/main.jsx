import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { addGame, addPlatform, formatManifest, parseManifest, updateCanonicalName } from './manifest.js';
import { knownPlatforms } from './platforms.js';
import { initialLocale, messages, persistLocale } from './i18n.js';
import './style.css';

const endpoint = '/api/admin/manifest';
const display = (value, fallback) => typeof value === 'string' && value ? value : fallback;
const blankDisc = () => ({ entrypoint: '', files: [{ source: '', path: '' }] });
const blankGame = () => ({ id: '', canonical_name: '', display_name: '', platform_id: '', playlist: '', discs: [blankDisc()] });

async function responseError(response) {
  const body = await response.text();
  let detail = body.trim();
  try {
    const json = JSON.parse(body);
    detail = typeof json === 'string' ? json : json.error || json.message || body;
  } catch { /* Plain text is also a valid error response. */ }
  return { status: response.status, detail };
}

function message(value, t) {
  if (typeof value === 'string') return t[value] || value;
  if (!value) return '';
  const { status, detail } = value;
  if (status === 409 || status === 412) return t.conflict + (detail ? ` ${detail}` : '');
  if (status === 403) return t.forbidden + (detail ? ` ${detail}` : '');
  return t.serverError(status) + (detail ? `: ${detail}` : `. ${t.retry}`);
}

function App() {
  const [locale, setLocale] = useState(initialLocale);
  const t = messages[locale];
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
  const [newGame, setNewGame] = useState(blankGame);
  const [gameReplacement, setGameReplacement] = useState(null);
  const [gameError, setGameError] = useState('');
  const [missing, setMissing] = useState(null);
  const dirty = loaded && text !== savedText;
  const parsed = parseManifest(text);
  const manifest = parsed.value;
  const games = Array.isArray(manifest?.contents) ? manifest.contents : [];
  const platforms = Array.isArray(manifest?.platforms) ? manifest.platforms : [];
  const game = games[selected];
  const platformName = (id) => display(platforms.find(platform => platform?.id === id)?.name, display(id, t.notSpecified));

  useEffect(() => { document.documentElement.lang = locale; document.title = t.title; persistLocale(locale); }, [locale]);

  useEffect(() => {
    function warn(event) {
      if (dirty) { event.preventDefault(); event.returnValue = ''; }
    }
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  useEffect(() => { setMissing(null); }, [text, etag, newGame]);

  async function load(confirmDiscard = true) {
    if (confirmDiscard && dirty && !window.confirm(t.discard)) return;
    setBusy(true); setError(''); setNotice(''); setMissing(null);
    try {
      const response = await fetch(endpoint, { cache: 'no-store' });
      if (!response.ok) throw await responseError(response);
      const body = await response.text();
      const revision = response.headers.get('ETag');
      if (!revision) throw new Error('noEtag');
      const formatted = formatManifest(body);
      setText(formatted); setSavedText(formatted); setEtag(revision); setLoaded(true); setSelected(0);
      setGameReplacement(null); setNewGame(blankGame()); setGameError('');
      setNotice('loaded'); setAddedPlatform(''); setPlatformError('');
    } catch (err) {
      setError(err instanceof TypeError ? 'loadNetwork' : err instanceof Error ? err.message : err);
    } finally { setBusy(false); }
  }

  useEffect(() => { load(false); }, []);

  async function save(approval = null) {
    if (parsed.error || !etag) return;
    if (approval && (approval.text !== text || approval.etag !== etag)) {
      setMissing(null); setError('staleApproval'); return;
    }
    if (gameReplacement && gameReplacement.draft !== JSON.stringify(newGame)) {
      setMissing(null); setError('changedDraft'); return;
    }
    const submitted = text;
    const revisionAtSubmit = etag;
    setBusy(true); setError(''); setNotice(''); setMissing(null);
    try {
      const response = await fetch(endpoint + (approval ? '?allow_missing=1' : ''), {
        method: 'PUT', headers: { 'Content-Type': 'application/json', 'If-Match': revisionAtSubmit }, body: submitted,
      });
      if (response.status === 422 && !approval) {
        const body = await response.clone().json().catch(() => null);
        if (Array.isArray(body?.missing) && body.missing.length && body.missing.every(item => typeof item?.id === 'string' && typeof item?.path === 'string')) {
          setMissing({ text: submitted, etag: revisionAtSubmit, files: body.missing }); return;
        }
      }
      if (!response.ok) throw await responseError(response);
      const revision = response.headers.get('ETag');
      if (!revision) {
        setEtag('');
        throw new Error('savedNoEtag');
      }
      const saved = response.status === 200 ? formatManifest(await response.text()) : submitted;
      setText(saved); setEtag(revision); setSavedText(saved); setNotice('savedNotice'); setAddedPlatform('');
      if (gameReplacement && gameReplacement.draft === JSON.stringify(newGame)) setNewGame(blankGame());
      setGameReplacement(null);
    } catch (err) {
      setError(err instanceof TypeError ? 'saveNetwork' : err instanceof Error ? err.message : err);
    } finally { setBusy(false); }
  }

  function changeName(value) {
    const updated = updateCanonicalName(text, selected, value);
    if (updated === text && value !== game?.canonical_name) {
      setError('nameError');
    } else { setText(updated); setError(''); setNotice(''); }
  }

  function submitPlatform(event) {
    event.preventDefault();
    const platform = platformMode === 'preset'
      ? knownPlatforms.find(item => item.libretro_name === preset)
      : customPlatform;
    if (!platform) { setPlatformError('choosePlatformError'); return; }
    const result = addPlatform(text, platform, locale);
    if (result.error) { setPlatformError(result.error); return; }
    setText(result.text); setPlatformError(''); setError(''); setNotice('');
    setAddedPlatform(`${platform.name} (${platform.id}; ${platform.libretro_name})`);
  }

  function submitGame(event) {
    event.preventDefault();
    const result = addGame(text, newGame, gameReplacement, locale);
    if (result.error) { setGameError(result.error); return; }
    setText(result.text); setSelected(result.replacement.index); setGameReplacement({ ...result.replacement, draft: JSON.stringify(newGame) }); setGameError(''); setError(''); setNotice('gameAdded');
  }

  function updateDisc(index, change) {
    setNewGame(current => ({ ...current, discs: current.discs.map((disc, i) => i === index ? { ...disc, ...change } : disc) }));
  }

  return <div className="app">
    <header className="header">
      <div><p className="eyebrow">RETROARCH MARKETPLACE</p><h1>{t.title}</h1><p className="subtitle">{t.subtitle}</p></div>
      <div className="header-actions">
        <label className="language-selector">{t.language} <select value={locale} onChange={event => { setLocale(event.target.value); setPlatformError(''); setGameError(''); }}><option value="ru">Русский</option><option value="en">English</option></select></label>
        <span className={`status ${dirty ? 'unsaved' : ''}`} role="status">{!loaded ? t.noData : dirty ? t.unsaved : t.saved}</span>
        <button className="secondary" onClick={() => load()} disabled={busy}>{t.reload}</button>
        <button className="primary" onClick={() => save()} disabled={busy || !dirty || !!parsed.error || !etag}>{t.save}</button>
      </div>
    </header>
    <main>
      {error && <div className="alert error" role="alert">{message(error, t)}</div>}
      {notice && <div className="alert success" role="status">{message(notice, t)}</div>}
      {missing && <section className="alert error" aria-labelledby="missing-heading">
        <h2 id="missing-heading">{t.missingTitle}</h2>
        <p>{t.missingWarning}</p>
        <ul>{missing.files.map((file, index) => <li key={index}>{file.id}: {file.path}</li>)}</ul>
        <button className="secondary" disabled={busy} onClick={() => save(missing)}>{t.saveAnyway}</button>{' '}
        <button className="secondary" disabled={busy} onClick={() => { setMissing(null); if (gameReplacement) document.getElementById('game-id')?.focus(); }}>{t.backEdit}</button>
      </section>}
      {busy && <p role="status" className="working">{t.working}</p>}
      {loaded && <>
        <section className="workspace" aria-label={t.catalogGames}>
          <aside className="panel games-panel">
            <div className="panel-heading"><div><p className="eyebrow">{t.catalog}</p><h2>{t.games}</h2></div><span className="count">{games.length}</span></div>
            {parsed.error ? <p className="muted">{t.fixGames}</p> : games.length === 0 ? <p className="muted">{t.noGames}</p> :
              <div className="game-list">{games.map((item, index) => <button key={index} className={`game-item ${selected === index ? 'selected' : ''}`} onClick={() => setSelected(index)} aria-pressed={selected === index}>
                <span className="game-title">{display(item?.canonical_name, display(item?.id, t.gameNumber(index + 1)))}</span><span className="game-platform">{platformName(item?.platform_id)}{item?.defer_missing === true && t.missingAllowedShort}</span>
              </button>)}</div>}
          </aside>
          <section className="panel details">
            <p className="eyebrow">{t.selectedGame}</p><h2>{t.details}</h2>
            {game && typeof game === 'object' && !Array.isArray(game) ? <>
              <label htmlFor="canonical">Canonical Name <span>{t.canonicalHint}</span></label>
              <input id="canonical" value={typeof game.canonical_name === 'string' ? game.canonical_name : ''} onChange={event => changeName(event.target.value)} disabled={busy} />
              <dl><div><dt>{t.identifier}</dt><dd>{display(game.id, t.notSpecifiedId)}</dd></div><div><dt>{t.platform}</dt><dd>{platformName(game.platform_id)}</dd></div><div><dt>{t.displayName}</dt><dd>{display(game.display_name, t.notSpecifiedName)}</dd></div><div><dt>{t.discs}</dt><dd>{Array.isArray(game.discs) ? game.discs.length : t.notSpecifiedDiscs}</dd></div></dl>
              {game.defer_missing === true && <p className="hint">{t.missingAllowed}</p>}
              <p className="hint">{t.otherFields}</p>
            </> : <p className="muted">{t.selectGame}</p>}
          </section>
        </section>
        <section className="panel game-form-panel" aria-labelledby="add-game-heading">
          <h2 id="add-game-heading">{t.addGame}</h2>
          <p className="hint">{t.gameHint}</p>
          <form onSubmit={submitGame} noValidate>
            <fieldset disabled={busy}>
              <legend>{t.gameData}</legend>
              {[['id', 'ID'], ['canonical_name', t.canonicalName], ['display_name', t.displayOptional]].map(([key, label]) => <div key={key}>
                <label htmlFor={`game-${key}`}>{label}</label>
                <input id={`game-${key}`} value={newGame[key]} onChange={event => setNewGame({ ...newGame, [key]: event.target.value })} />
              </div>)}
              <label htmlFor="game-platform">{t.platform}</label>
              <select id="game-platform" value={newGame.platform_id} onChange={event => setNewGame({ ...newGame, platform_id: event.target.value })}>
                <option value="">{t.choosePlatform}</option>
                {platforms.filter(item => typeof item?.id === 'string').map((item, index) => <option key={index} value={item.id}>{display(item.name, item.id)} ({item.id})</option>)}
              </select>
              {newGame.discs.map((disc, index) => <fieldset className="disc-form" key={index}>
                <legend>{t.disc(index + 1)}</legend>
                <label htmlFor={`disc-${index}-entrypoint`}>{t.entrypoint}</label>
                <input id={`disc-${index}-entrypoint`} value={disc.entrypoint} onChange={event => updateDisc(index, { entrypoint: event.target.value })} />
                {disc.files.map((file, fileIndex) => <div className="file-form" key={fileIndex}>
                  <label htmlFor={`disc-${index}-source-${fileIndex}`}>{t.sourceFile(fileIndex + 1)}</label>
                  <input id={`disc-${index}-source-${fileIndex}`} value={file.source} onChange={event => updateDisc(index, { files: disc.files.map((item, i) => i === fileIndex ? { ...item, source: event.target.value } : item) })} />
                  <label htmlFor={`disc-${index}-path-${fileIndex}`}>{t.filePath(fileIndex + 1)}</label>
                  <input id={`disc-${index}-path-${fileIndex}`} value={file.path} onChange={event => updateDisc(index, { files: disc.files.map((item, i) => i === fileIndex ? { ...item, path: event.target.value } : item) })} />
                  <button type="button" className="secondary" onClick={() => updateDisc(index, { files: disc.files.filter((_, i) => i !== fileIndex) })}>{t.removeFile(fileIndex + 1, index + 1)}</button>
                </div>)}
                <button type="button" className="secondary" onClick={() => updateDisc(index, { files: [...disc.files, { source: '', path: '' }] })}>{t.addFile(index + 1)}</button>{' '}
                <button type="button" className="secondary" onClick={() => setNewGame({ ...newGame, discs: newGame.discs.filter((_, i) => i !== index) })}>{t.removeDisc(index + 1)}</button>
              </fieldset>)}
              <button type="button" className="secondary" onClick={() => setNewGame({ ...newGame, discs: [...newGame.discs, blankDisc()] })}>{t.addDisc}</button>
              <label htmlFor="game-playlist">{newGame.discs.length > 1 ? t.playlistRequired : t.playlistOptional}</label>
              <input id="game-playlist" value={newGame.playlist} onChange={event => setNewGame({ ...newGame, playlist: event.target.value })} />
              {gameError && <p className="json-error" role="alert">{message(gameError, t)}</p>}
              <button className="secondary" type="submit">{gameReplacement ? t.applyGame : t.addGame}</button>
              {gameReplacement && <button className="secondary" type="button" onClick={() => { setGameReplacement(null); setNewGame(blankGame()); setGameError(''); }}>{t.anotherGame}</button>}
            </fieldset>
          </form>
        </section>
        <section className="panel platform-panel" aria-labelledby="platform-heading">
          <div className="panel-heading"><div><p className="eyebrow">{t.catalog}</p><h2 id="platform-heading">{t.platforms}</h2></div><span className="count">{platforms.length}</span></div>
          {parsed.error ? <p className="muted">{t.fixPlatforms}</p> : platforms.length === 0 ? <p className="muted">{t.noPlatforms}</p> :
            <ul className="platform-list">{platforms.map((item, index) => <li key={index}><strong>{display(item?.name, t.unnamed)}</strong> <span>{display(item?.id, t.noId)} · {display(item?.libretro_name, t.noLibretro)}</span></li>)}</ul>}
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
                  {knownPlatforms.map(item => <option key={item.libretro_name} value={item.libretro_name}>{item.libretro_name}</option>)}
                </select>
              </> : <>
                {[['id', 'ID'], ['name', t.name], ['libretro_name', 'Libretro name']].map(([key, label]) => <div key={key}>
                  <label htmlFor={`platform-${key}`}>{label}</label>
                  <input id={`platform-${key}`} value={customPlatform[key]} onChange={event => setCustomPlatform({ ...customPlatform, [key]: event.target.value })} />
                </div>)}
              </>}
              <button className="secondary" type="submit">{t.addPlatform}</button>
            </fieldset>
          </form>
          {platformError && <p className="json-error" role="alert">{message(platformError, t)}</p>}
          {addedPlatform && <p role="status">{t.platformAdded(addedPlatform)}</p>}
        </section>
        <section className="panel json-panel">
          <div className="json-heading"><div><p className="eyebrow">{t.advanced}</p><h2>{t.fullJson}</h2><p className="muted">{t.jsonHint}</p></div></div>
          <label htmlFor="manifest-json" className="sr-only">{t.fullJson}</label>
          <textarea id="manifest-json" value={text} onChange={event => { setText(event.target.value); setError(''); setNotice(''); setAddedPlatform(''); setPlatformError(''); }} spellCheck="false" disabled={busy} aria-invalid={!!parsed.error} aria-describedby={parsed.error ? 'json-error' : undefined} />
          {parsed.error && <p id="json-error" className="json-error" role="alert">{t.invalidJson(parsed.error)}</p>}
        </section>
      </>}
    </main>
  </div>;
}

createRoot(document.getElementById('root')).render(<App />);
