import React, { useState } from 'react';

function boxArtURL(platform, name, mock) {
  const segments = [platform, name].map(encodeURIComponent);
  return mock
    ? `/api/admin/mock/boxart?platform=${segments[0]}&name=${segments[1]}`
    : `https://thumbnails.libretro.com/${segments[0]}/Named_Boxarts/${segments[1]}.png`;
}

function Cover({ url, alt, fallback, preview }) {
  const [failed, setFailed] = useState(false);
  return <div className={`cover ${preview ? 'cover-preview' : ''}`}>
    {failed ? <span title={fallback}>{preview ? fallback : <><span aria-hidden="true">—</span><span className="sr-only">{fallback}</span></>}</span> :
      <img src={url} alt={alt} loading={preview ? 'eager' : 'lazy'} referrerPolicy="no-referrer" onError={() => setFailed(true)} />}
  </div>;
}

export function BoxArt({ lookup, platform, name, mock, onSelect, t }) {
  if (!lookup) return null;
  if (lookup.status === 'loading') return <p className="hint" role="status">{t.lookupLoading}</p>;
  if (lookup.status === 'error') return <p className="hint" role="status">{t.lookupError}</p>;

  const exact = lookup.candidates.includes(name);
  const alternatives = lookup.candidates.filter(candidate => candidate !== name);
  const url = candidate => boxArtURL(platform, candidate, mock);
  return <section className="boxart" aria-label="BoxArt">
    {exact ? <div className="boxart-match">
      <Cover key={url(name)} url={url(name)} alt={t.coverAlt(name)} fallback={t.coverFailed} preview />
      <div><strong>{t.exactCover}</strong><p className="hint">{name}</p></div>
    </div> : <p className="hint" role="status">{t.noExactCover}</p>}
    {!!alternatives.length && <details className="boxart-alternatives" open={!exact}>
      <summary>{t.similarCovers(alternatives.length)}</summary>
      <ul>{alternatives.map(candidate => <li key={candidate}>
        <Cover key={url(candidate)} url={url(candidate)} alt="" fallback={t.coverFailed} />
        <span>{candidate}</span>
        <button className="secondary btn-small" type="button" aria-label={t.useTitle(candidate)}
          onClick={() => onSelect(candidate)}>{t.useTitleButton}</button>
      </li>)}</ul>
    </details>}
  </section>;
}
