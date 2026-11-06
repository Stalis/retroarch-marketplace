import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { MockBackend, initialMockManifest } from '../mock-backend.js';

function startTestServer() {
  const backend = new MockBackend();
  const server = createServer((req, res) => backend.handle(req, res));
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      resolve({
        server,
        backend,
        baseUrl: `http://127.0.0.1:${port}`
      });
    });
  });
}

test('mock backend handles GET /api/admin/manifest with ETag', async () => {
  const { server, baseUrl } = await startTestServer();
  try {
    const res = await fetch(`${baseUrl}/api/admin/manifest`);
    assert.equal(res.status, 200);
    const etag = res.headers.get('ETag');
    assert.ok(etag && etag.startsWith('"') && etag.endsWith('"'));
    const data = await res.json();
    assert.deepEqual(data.platforms, initialMockManifest.platforms);
    assert.equal(data.contents.length, initialMockManifest.contents.length);
  } finally {
    server.close();
  }
});

test('mock backend updates manifest on valid PUT with matching ETag', async () => {
  const { server, baseUrl } = await startTestServer();
  try {
    const getRes = await fetch(`${baseUrl}/api/admin/manifest`);
    const etag = getRes.headers.get('ETag');
    const manifest = await getRes.json();

    manifest.contents[0].canonical_name = 'Updated Title';
    const putRes = await fetch(`${baseUrl}/api/admin/manifest`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'If-Match': etag
      },
      body: JSON.stringify(manifest)
    });
    assert.equal(putRes.status, 204);
    const newEtag = putRes.headers.get('ETag');
    assert.ok(newEtag && newEtag !== etag);

    const verifyRes = await fetch(`${baseUrl}/api/admin/manifest`);
    const verified = await verifyRes.json();
    assert.equal(verified.contents[0].canonical_name, 'Updated Title');
  } finally {
    server.close();
  }
});

test('mock backend rejects PUT with stale or missing ETag with 412', async () => {
  const { server, baseUrl } = await startTestServer();
  try {
    const resNoEtag = await fetch(`${baseUrl}/api/admin/manifest`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(initialMockManifest)
    });
    assert.equal(resNoEtag.status, 412);

    const resWrongEtag = await fetch(`${baseUrl}/api/admin/manifest`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'If-Match': '"wrong-etag"'
      },
      body: JSON.stringify(initialMockManifest)
    });
    assert.equal(resWrongEtag.status, 412);
  } finally {
    server.close();
  }
});

test('mock backend simulates 422 for missing files and accepts allow_missing=1', async () => {
  const { server, baseUrl } = await startTestServer();
  try {
    const getRes = await fetch(`${baseUrl}/api/admin/manifest`);
    const etag = getRes.headers.get('ETag');
    const manifest = await getRes.json();

    // Add a game with a simulated missing source
    manifest.contents.push({
      id: 'missing-test',
      canonical_name: 'Missing Test',
      platform_id: 'playstation',
      discs: [
        {
          entrypoint: 'game.bin',
          files: [{ source: 'missing/file.bin', path: 'game.bin' }]
        }
      ]
    });

    const putRes = await fetch(`${baseUrl}/api/admin/manifest`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'If-Match': etag
      },
      body: JSON.stringify(manifest)
    });
    assert.equal(putRes.status, 422);
    const errorBody = await putRes.json();
    assert.deepEqual(errorBody, {
      missing: [{ id: 'missing-test', path: 'missing/file.bin' }]
    });

    // Save with allow_missing=1
    const forceRes = await fetch(`${baseUrl}/api/admin/manifest?allow_missing=1`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'If-Match': etag
      },
      body: JSON.stringify(manifest)
    });
    assert.equal(forceRes.status, 200);
    const forceBody = await forceRes.json();
    const savedMissingItem = forceBody.contents.find(c => c.id === 'missing-test');
    assert.equal(savedMissingItem.defer_missing, true);
  } finally {
    server.close();
  }
});

test('mock public games include deferred games only when their files are available', async () => {
  const { server, baseUrl } = await startTestServer();
  try {
    const getRes = await fetch(`${baseUrl}/api/admin/manifest`);
    const manifest = await getRes.json();
    const deferred = manifest.contents.find(item => item.id === 'unreleased-prototype');
    deferred.discs.push({
      entrypoint: 'extra.bin',
      files: [{ source: 'Genesis/absent/extra.bin', path: 'extra.bin' }]
    });
    deferred.discs[0].files[0].source = 'Genesis/proto.bin';

    const save = async (etag) => fetch(`${baseUrl}/api/admin/manifest`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', 'If-Match': etag },
      body: JSON.stringify(manifest)
    });
    const gameIds = async () => (await (await fetch(`${baseUrl}/api/games`)).json()).map(game => game.id);

    let saved = await save(getRes.headers.get('ETag'));
    assert.equal(saved.status, 204);
    assert.ok(!(await gameIds()).includes(deferred.id));

    deferred.discs[1].files[0].source = 'Genesis/extra.bin';
    saved = await save(saved.headers.get('ETag'));
    assert.equal(saved.status, 204);
    assert.ok((await gameIds()).includes(deferred.id));

    deferred.discs[0].files[0].source = 'Genesis/missing/proto.bin';
    saved = await save(saved.headers.get('ETag'));
    assert.equal(saved.status, 204);
    const ids = await gameIds();
    assert.ok(!ids.includes(deferred.id));
    assert.ok(ids.includes('kings-field'));
  } finally {
    server.close();
  }
});
