import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Сборка кладётся в api/internal/web/dist и встраивается в бинарник Go (ТЗ §10.2).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../api/internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8090',
    },
  },
})
