import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The Go server serves the built app at /ui/ and embeds ../internal/webui/dist.
// In dev, API calls and the event stream go to a running `birdie serve`.
const target = process.env.BIRDIE_URL || 'http://127.0.0.1:7777';

export default defineConfig(({ command }) => ({
  base: '/ui/',
  plugins: [react()],
  define: {
    // Where the dev proxy points, so the "not reachable" banner names the right address.
    __BIRDIE_DEV_ADDRESS__: JSON.stringify(command === 'serve' ? new URL(target).host : ''),
  },
  server: {
    fs: { allow: ['.', '../design'] },
    proxy: {
      '/ui/api': {
        target,
        changeOrigin: true,
        // Keep the event stream flowing instead of buffering it.
        configure: (proxy) => {
          proxy.on('proxyRes', (res) => {
            if (String(res.headers['content-type'] || '').startsWith('text/event-stream')) {
              res.headers['cache-control'] = 'no-cache';
              res.headers['x-accel-buffering'] = 'no';
            }
          });
        },
      },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: false,
    assetsDir: 'assets',
  },
}));
