import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Relative base so the build works when served from the embedded Go FS.
export default defineConfig({
  base: './',
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
  },
})
