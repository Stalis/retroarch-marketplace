import { defineConfig } from 'vite';

export default defineConfig({
  base: '/admin/',
  server: {
    host: '127.0.0.1',
    proxy: { '/api/admin': 'http://127.0.0.1:5050' },
  },
});
