import { readFileSync } from 'node:fs';
import { validateGame } from './src/game.js';

const knownPlatforms = JSON.parse(readFileSync(new URL('../internal/store/known-platforms.json', import.meta.url)));

const disc = (entrypoint, ...sources) => ({ entrypoint, files: sources.map(([source, path]) => ({ source, path })) });

export const initialMockData = {
  platforms: [
    { id: 'playstation', name: 'PlayStation', libretro_name: 'Sony - PlayStation' },
    { id: 'snes', name: 'Super Nintendo Entertainment System', libretro_name: 'Nintendo - Super Nintendo Entertainment System' },
    { id: 'genesis', name: 'Sega Mega Drive / Genesis', libretro_name: 'Sega - Mega Drive - Genesis' },
  ],
  games: [
    { id: 'kings-field', canonical_name: "King's Field", display_name: "King's Field [SLPS-00017]", platform_id: 'playstation',
      discs: [disc("King's Field.cue", ["PSX/King's Field/King's Field.cue", "King's Field.cue"], ["PSX/King's Field/KING'S FIELD.BIN", "KING'S FIELD.BIN"])] },
    { id: 'parasite-eve-ii', canonical_name: 'Parasite Eve II', display_name: 'Parasite Eve II (Europe) [RUS]', platform_id: 'playstation', playlist: 'Parasite Eve II.m3u',
      discs: [
        disc('Disc 1/disc1.cue', ['PSX/PE2/Disc 1/disc1.cue', 'Disc 1/disc1.cue'], ['PSX/PE2/Disc 1/disc1.bin', 'Disc 1/disc1.bin']),
        disc('Disc 2/disc2.cue', ['PSX/PE2/Disc 2/disc2.cue', 'Disc 2/disc2.cue'], ['PSX/PE2/Disc 2/disc2.bin', 'Disc 2/disc2.bin']),
      ] },
    { id: 'super-mario-world', canonical_name: 'Super Mario World', platform_id: 'snes', discs: [disc('smw.sfc', ['SNES/smw.sfc', 'smw.sfc'])] },
    { id: 'unreleased-prototype', canonical_name: 'Unreleased Prototype', platform_id: 'genesis', discs: [disc('proto.bin', ['Genesis/missing/proto.bin', 'proto.bin'])] },
  ],
};

// Sources containing "missing" or "absent" simulate files absent on disk.
const missingSources = game => game.discs.flatMap(d => d.files)
  .filter(file => file.source.includes('missing') || file.source.includes('absent'))
  .map(file => ({ id: game.id, path: file.source }));

const fold = value => (value || '').toLowerCase();

export class MockBackend {
  constructor(initialData = initialMockData) {
    this.initialData = initialData;
    this.reset();
  }

  reset() {
    const copy = structuredClone(this.initialData);
    this.platforms = copy.platforms;
    this.games = copy.games.map(game => ({ game, revision: 1 }));
  }

  summary({ game, revision }) {
    return { id: game.id, canonical_name: game.canonical_name, ...(game.display_name ? { display_name: game.display_name } : {}), platform_id: game.platform_id, available: missingSources(game).length === 0, revision };
  }

  // Mirrors the public /api/games cards (minimal fields): available games only.
  publicGames() {
    return this.games.filter(({ game }) => missingSources(game).length === 0).map(({ game }) => ({
      id: game.id, canonical_name: game.canonical_name, platform: this.platforms.find(p => p.id === game.platform_id),
    }));
  }

  async handle(req, res) {
    const url = new URL(req.url, 'http://127.0.0.1');
    const path = url.pathname;
    const send = (status, body, headers = {}) => {
      const json = body !== undefined && typeof body !== 'string';
      res.writeHead(status, { 'Content-Type': json ? 'application/json' : 'text/plain; charset=utf-8', 'X-Catalog-Version': '2.0.0-mock', 'Cache-Control': 'no-store', ...headers });
      res.end(json ? JSON.stringify(body) : body);
    };
    const readBody = async () => {
      let text = '';
      for await (const chunk of req) text += chunk;
      try { return JSON.parse(text); } catch { return null; }
    };
    const method = req.method;

    if (path === '/api/admin/mock/reset' && method === 'POST') { this.reset(); return send(200, { ok: true }); }
    if (path === '/api/games' && method === 'GET') return send(200, this.publicGames());
    if (path === '/api/platforms' && method === 'GET') return send(200, this.platforms);
    if (path === '/api/admin/known-platforms' && method === 'GET') return send(200, knownPlatforms);

    if (path === '/api/admin/platforms') {
      if (method === 'GET') return send(200, this.platforms.map(p => ({ ...p, games: this.games.filter(({ game }) => game.platform_id === p.id && missingSources(game).length === 0).length })));
      if (method !== 'POST') return send(405, 'method not allowed', { Allow: 'GET, POST' });
      const platform = await readBody();
      if (!platform?.id || !platform.name || !platform.libretro_name) return send(400, 'invalid platform');
      if (this.platforms.some(p => p.id === platform.id)) return send(409, 'platform exists');
      this.platforms.push({ id: platform.id, name: platform.name, libretro_name: platform.libretro_name });
      return send(201, '');
    }

    if (path.startsWith('/api/admin/platforms/')) {
      if (method !== 'DELETE') return send(405, 'method not allowed', { Allow: 'DELETE' });
      const id = decodeURIComponent(path.slice('/api/admin/platforms/'.length));
      const index = this.platforms.findIndex(p => p.id === id);
      if (index < 0) return send(404, 'not found');
      if (this.games.some(({ game }) => game.platform_id === id)) return send(409, 'platform has games');
      this.platforms.splice(index, 1);
      return send(204, '');
    }

    const allowMissing = url.searchParams.get('allow_missing') === '1';
    const check = game => {
      const error = game && validateGame(game, 'en');
      if (!game || error) return [400, error || 'invalid game'];
      if (!this.platforms.some(p => p.id === game.platform_id)) return [400, 'unknown platform'];
      const missing = missingSources(game);
      if (missing.length && !allowMissing) return [422, { missing }];
      return null;
    };

    if (path === '/api/admin/games') {
      if (method === 'GET') {
        const q = fold(url.searchParams.get('q'));
        const platform = url.searchParams.get('platform_id');
        const skip = Number(url.searchParams.get('skip')) || 0;
        const count = Number(url.searchParams.get('count') || 50);
        const found = this.games.filter(({ game }) => (!q || fold(game.canonical_name).includes(q) || fold(game.display_name).includes(q)) && (!platform || game.platform_id === platform));
        return send(200, found.slice(skip, count < 0 ? undefined : skip + count).map(entry => this.summary(entry)));
      }
      if (method !== 'POST') return send(405, 'method not allowed', { Allow: 'GET, POST' });
      const game = await readBody();
      const failure = check(game);
      if (failure) return send(...failure);
      if (this.games.some(entry => entry.game.id === game.id)) return send(409, 'game exists');
      this.games.push({ game, revision: 1 });
      return send(201, '', { ETag: '"1"', Location: `/api/admin/games/${encodeURIComponent(game.id)}` });
    }

    if (path.startsWith('/api/admin/games/')) {
      const id = decodeURIComponent(path.slice('/api/admin/games/'.length));
      const entry = this.games.find(item => item.game.id === id);
      if (!['GET', 'PUT', 'DELETE'].includes(method)) return send(405, 'method not allowed', { Allow: 'GET, PUT, DELETE' });
      if (!entry) return send(404, 'not found');
      if (method === 'GET') {
        const missing = missingSources(entry.game);
        return send(200, { game: entry.game, available: missing.length === 0, missing }, { ETag: `"${entry.revision}"` });
      }
      const ifMatch = req.headers['if-match'];
      if (ifMatch !== `"${entry.revision}"`) return send(412, 'game revision does not match');
      if (method === 'DELETE') {
        this.games.splice(this.games.indexOf(entry), 1);
        return send(204, '');
      }
      const game = await readBody();
      if (game && game.id !== id) return send(400, 'game id does not match path');
      const failure = check(game);
      if (failure) return send(...failure);
      entry.game = game;
      entry.revision++;
      return send(204, '', { ETag: `"${entry.revision}"` });
    }

    send(404, 'Not Found');
  }
}

// Standalone runner when executed directly via node
if (import.meta.url === `file://${process.argv[1]}`) {
  const { createServer } = await import('node:http');
  const port = Number(process.env.PORT) || 5050;
  const backend = new MockBackend();
  const server = createServer((req, res) => backend.handle(req, res));
  server.listen(port, '127.0.0.1', () => {
    console.log(`Mock admin backend listening at http://127.0.0.1:${port}/api/admin/games`);
  });
}
