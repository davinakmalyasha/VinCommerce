/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],

  // Only VITE_-prefixed vars are exposed to the client. Spelled out
  // explicitly so adding a VITE_SECRET_* by accident becomes a visible,
  // reviewable diff rather than an invisible leak.
  envPrefix: ['VITE_'],

  build: {
    // Baseline for a React 19 + modern-browser storefront. Avoids shipping
    // downlevel transforms nobody needs.
    target: 'es2022',
    // Never emit production source maps publicly: the bundle is served to
    // every visitor and a map would expose source structure.
    sourcemap: false,
    // Vite 8 / Rolldown minifies with Oxc. Naming 'esbuild' explicitly fails
    // the build because that path is deprecated and esbuild is not installed.
    minify: 'oxc',
    // Tailwind's single 53 KB stylesheet was being sent to every route
    // regardless of use; splitting lets a page load only what it needs.
    cssCodeSplit: true,
    assetsInlineLimit: 2048,
    // Print gzip sizes in the build output so a bundle-size regression is
    // visible in CI rather than discovered by users.
    reportCompressedSize: true,
    // The old default of 500 KB silently accepted a 371 KB entry chunk.
    chunkSizeWarningLimit: 300,
    modulePreload: { polyfill: false },
    rollupOptions: {
      output: {
        // Vite 8 ships Rolldown, which prefers advancedChunks. The grouping
        // matters more than the mechanism: without it, react-dom, the router,
        // react-query, zustand, axios AND the app code all lived in one
        // 371 KB index chunk, so any app change invalidated 110 KB gzip of
        // vendor code in every visitor's cache.
        advancedChunks: {
          groups: [
            {
              name: 'react',
              test: /node_modules[\\/](react|react-dom|scheduler|react-router|react-router-dom)[\\/]/,
            },
            {
              name: 'data',
              test: /node_modules[\\/](@tanstack|use-sync-external-store)[\\/]/,
            },
            {
              name: 'state',
              test: /node_modules[\\/]zustand[\\/]/,
            },
            {
              name: 'net',
              test: /node_modules[\\/](axios|follow-redirects|form-data)[\\/]/,
            },
          ],
        },
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },

  optimizeDeps: {
    include: ['react', 'react-dom/client', 'react-router-dom', '@tanstack/react-query'],
  },

  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/uploads': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },

  test: {
    environment: 'jsdom',
    globals: false,
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      include: ['src/lib/**', 'src/stores/**'],
    },
  },
})
