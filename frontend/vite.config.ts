import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { belayPWA } from './vite-pwa.js'

const pwa = belayPWA({ name: 'Belay', shortName: 'Belay', themeColor: '#0f0f0f' })

export default defineConfig({
  define: { ...pwa.define },
  plugins: [react(), ...pwa.plugins],
  server: {
    port: 33411,
    strictPort: true,
    host: true,
    allowedHosts: true,
    proxy: {
      '/api': {
        target: 'http://localhost:33412',
        changeOrigin: true,
      },
    },
  },
})
