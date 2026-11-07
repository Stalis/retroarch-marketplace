import { createHash } from 'node:crypto';

export const initialMockManifest = {
  platforms: [
    { id: 'playstation', name: 'PlayStation', libretro_name: 'Sony - PlayStation' },
    { id: 'snes', name: 'Super Nintendo Entertainment System', libretro_name: 'Nintendo - Super Nintendo Entertainment System' },
    { id: 'genesis', name: 'Sega Mega Drive / Genesis', libretro_name: 'Sega - Mega Drive - Genesis' }
  ],
  contents: [
    {
      id: 'kings-field',
      canonical_name: "King's Field",
      display_name: "King's Field [SLPS-00017]",
      platform_id: 'playstation',
      discs: [
        {
          entrypoint: "King's Field.cue",
          files: [
            { source: "PSX/King's Field/King's Field.cue", path: "King's Field.cue" },
            { source: "PSX/King's Field/KING'S FIELD.BIN", path: "KING'S FIELD.BIN" }
          ]
        }
      ]
    },
    {
      id: 'parasite-eve-ii',
      canonical_name: 'Parasite Eve II',
      display_name: 'Parasite Eve II (Europe) [RUS]',
      platform_id: 'playstation',
      playlist: 'Parasite Eve II.m3u',
      discs: [
        {
          entrypoint: 'Disc 1/disc1.cue',
          files: [
            { source: 'PSX/PE2/Disc 1/disc1.cue', path: 'Disc 1/disc1.cue' },
            { source: 'PSX/PE2/Disc 1/disc1.bin', path: 'Disc 1/disc1.bin' }
          ]
        },
        {
          entrypoint: 'Disc 2/disc2.cue',
          files: [
            { source: 'PSX/PE2/Disc 2/disc2.cue', path: 'Disc 2/disc2.cue' },
            { source: 'PSX/PE2/Disc 2/disc2.bin', path: 'Disc 2/disc2.bin' }
          ]
        }
      ]
    },
    {
      id: 'super-mario-world',
      canonical_name: 'Super Mario World',
      platform_id: 'snes',
      discs: [
        {
          entrypoint: 'smw.sfc',
          files: [
            { source: 'SNES/smw.sfc', path: 'smw.sfc' }
          ]
        }
      ]
    },
    {
      id: 'unreleased-prototype',
      canonical_name: 'Unreleased Prototype',
      platform_id: 'genesis',
      defer_missing: true,
      discs: [
        {
          entrypoint: 'proto.bin',
          files: [
            { source: 'Genesis/missing/proto.bin', path: 'proto.bin' }
          ]
        }
      ]
    }
  ]
};

export class MockBackend {
  constructor(initialData = initialMockManifest) {
    this.initialData = initialData;
    this.reset();
  }

  reset() {
    this.text = JSON.stringify(this.initialData, null, 2) + '\n';
    this.etag = this.computeEtag(this.text);
  }

  computeEtag(content) {
    return `"${createHash('sha256').update(content).digest('hex')}"`;
  }

  findMissingFiles(manifest, includeDeferred = false) {
    const missing = [];
    if (!manifest || !Array.isArray(manifest.contents)) return missing;
    for (const item of manifest.contents) {
      if (!item || (!includeDeferred && item.defer_missing === true) || !Array.isArray(item.discs)) continue;
      for (const disc of item.discs) {
        if (!disc || !Array.isArray(disc.files)) continue;
        for (const file of disc.files) {
          if (typeof file?.source === 'string' && (file.source.includes('missing') || file.source.includes('absent'))) {
            missing.push({ id: item.id || '', path: file.source });
          }
        }
      }
    }
    return missing;
  }

  // Mirrors the public /api/games cards (minimal fields): available games with a known platform.
  publicGames() {
    const manifest = JSON.parse(this.text);
    const platforms = new Map(manifest.platforms.map(platform => [platform.id, platform]));
    const missingIds = new Set(this.findMissingFiles(manifest, true).map(file => file.id));
    return manifest.contents
      .filter(item => !missingIds.has(item.id) && platforms.has(item.platform_id))
      .map(item => ({ id: item.id, canonical_name: item.canonical_name, platform: platforms.get(item.platform_id) }));
  }

  handle(req, res) {
    const url = new URL(req.url, 'http://127.0.0.1');
    const pathname = url.pathname;

    // Optional mock reset endpoint
    if (pathname === '/api/admin/mock/reset' && req.method === 'POST') {
      this.reset();
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: true, etag: this.etag }));
      return;
    }

    if ((pathname === '/api/games' || pathname === '/api/platforms') && req.method === 'GET') {
      const games = this.publicGames();
      const body = pathname === '/api/games' ? games : JSON.parse(this.text).platforms;
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(body));
      return;
    }

    if (pathname !== '/api/admin/manifest') {
      res.writeHead(404, { 'Content-Type': 'text/plain' });
      res.end('Not Found');
      return;
    }

    if (req.method === 'GET') {
      res.writeHead(200, {
        'Content-Type': 'application/json',
        'ETag': this.etag,
        'X-Catalog-Version': '1.0.0-mock',
        'Cache-Control': 'no-store'
      });
      res.end(this.text);
      return;
    }

    if (req.method === 'PUT') {
      const ifMatch = req.headers['if-match'];
      if (!ifMatch || ifMatch !== this.etag) {
        res.writeHead(412, { 'Content-Type': 'text/plain' });
        res.end('manifest revision does not match');
        return;
      }

      let body = '';
      req.on('data', chunk => { body += chunk; });
      req.on('end', () => {
        let parsed;
        try {
          parsed = JSON.parse(body);
        } catch (err) {
          res.writeHead(400, { 'Content-Type': 'text/plain' });
          res.end(`invalid JSON: ${err.message}`);
          return;
        }

        if (!parsed || typeof parsed !== 'object' || !Array.isArray(parsed.platforms) || !Array.isArray(parsed.contents)) {
          res.writeHead(400, { 'Content-Type': 'text/plain' });
          res.end('invalid manifest: platforms and contents must be arrays');
          return;
        }

        const allowMissing = url.searchParams.get('allow_missing') === '1';
        const missing = this.findMissingFiles(parsed);

        if (missing.length > 0 && !allowMissing) {
          res.writeHead(422, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ missing }));
          return;
        }

        if (missing.length > 0 && allowMissing) {
          const missingIds = new Set(missing.map(m => m.id));
          for (const item of parsed.contents) {
            if (missingIds.has(item.id)) {
              item.defer_missing = true;
            }
          }
          this.text = JSON.stringify(parsed, null, 2) + '\n';
          this.etag = this.computeEtag(this.text);
          res.writeHead(200, {
            'Content-Type': 'application/json',
            'ETag': this.etag
          });
          res.end(this.text);
          return;
        }

        this.text = body;
        this.etag = this.computeEtag(this.text);
        res.writeHead(204, { 'ETag': this.etag });
        res.end();
      });
      return;
    }

    res.writeHead(405, { 'Allow': 'GET, PUT', 'Content-Type': 'text/plain' });
    res.end('Method Not Allowed');
  }
}

// Standalone runner when executed directly via node
if (import.meta.url === `file://${process.argv[1]}`) {
  const { createServer } = await import('node:http');
  const port = Number(process.env.PORT) || 5050;
  const backend = new MockBackend();
  const server = createServer((req, res) => backend.handle(req, res));
  server.listen(port, '127.0.0.1', () => {
    console.log(`Mock admin backend listening at http://127.0.0.1:${port}/api/admin/manifest`);
  });
}
