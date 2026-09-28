import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  envDir: false,
  server: {
    host: '127.0.0.1',
    proxy: Object.fromEntries(['/api', '/health'].map(path => [path, {
      target: process.env.CAMPUSCLAW_API_TARGET || 'http://127.0.0.1:8080',
      changeOrigin: false,
    }])),
  },
});
