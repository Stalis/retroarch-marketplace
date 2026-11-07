import { defineConfig } from 'vite';
import { MockBackend } from './mock-backend.js';

export default defineConfig(({ mode }) => {
  const isMock = process.env.MOCK === 'true' || mode === 'mock';
  const mockBackend = isMock ? new MockBackend() : null;

  return {
    base: '/admin/',
    server: {
      host: '127.0.0.1',
      ...(isMock
        ? {}
        : { proxy: { '/api': 'http://127.0.0.1:5050' } }),
    },
    plugins: [
      isMock && {
        name: 'mock-backend',
        configureServer(server) {
          server.middlewares.use((req, res, next) => {
            if (req.url && req.url.startsWith('/api/')) {
              mockBackend.handle(req, res);
            } else {
              next();
            }
          });
        },
      },
    ].filter(Boolean),
  };
});
