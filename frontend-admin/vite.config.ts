import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Build output goes directly into the Go binary's go:embed target so that
// `make build` produces a single self-contained server binary.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/internal/admin/spa/dist',
    // Keep false so the committed .keep sentinel (which lets //go:embed
    // compile on a fresh clone before any build) isn't wiped each build.
    // Stale hashed assets from a previous build are harmless — they're never
    // referenced by the new index.html and only bloat the embed marginally.
    emptyOutDir: false,
    // Mirror asset paths so they resolve cleanly under /admin/assets/*
    assetsDir: 'assets',
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    // Proxy admin API + uploads to the Go backend so the dev server can be
    // used without CORS setup. This is OPTIONAL — production never runs vite.
    proxy: {
      '/admin/api': 'http://localhost:8080',
      '/uploads': 'http://localhost:8080',
    },
  },
  // Base path must match how the SPA is served. We serve under /admin/ so
  // all asset URLs are prefixed accordingly.
  base: '/admin/',
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test-setup.ts',
  },
} as any);
