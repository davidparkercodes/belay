import { VitePWA } from 'vite-plugin-pwa'
import { readFileSync } from 'fs'

export function belayPWA(opts) {
  const pkg = JSON.parse(readFileSync('./package.json', 'utf-8'))
  const themeColor = opts.themeColor || '#0f0f0f'
  const backgroundColor = opts.backgroundColor || themeColor
  const iconType = opts.iconType || 'svg'

  const icons = iconType === 'png'
    ? [
        { src: 'pwa-192x192.png', sizes: '192x192', type: 'image/png' },
        { src: 'pwa-512x512.png', sizes: '512x512', type: 'image/png' },
        { src: 'pwa-512x512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
      ]
    : [
        { src: 'favicon.svg', sizes: '192x192', type: 'image/svg+xml', purpose: 'any maskable' },
      ]

  return {
    define: {
      __APP_VERSION__: JSON.stringify(pkg.version),
      __BUILD_TIME__: JSON.stringify(new Date().toISOString()),
    },
    plugins: [
      VitePWA({
        registerType: 'autoUpdate',
        devOptions: { enabled: true, type: 'module' },
        includeAssets: iconType === 'png'
          ? ['favicon.svg', 'pwa-192x192.png', 'pwa-512x512.png']
          : ['favicon.svg'],
        workbox: {
          cacheId: `${opts.shortName.toLowerCase().replace(/\s+/g, '-')}-v${pkg.version}`,
          cleanupOutdatedCaches: true,
          skipWaiting: true,
          clientsClaim: true,
          navigateFallback: 'index.html',
          navigateFallbackAllowlist: [/^(?!\/__).*/],
          runtimeCaching: [
            {
              urlPattern: ({ request }) => request.mode === 'navigate',
              handler: 'NetworkFirst',
              options: {
                cacheName: 'html-cache',
                expiration: { maxEntries: 10, maxAgeSeconds: 60 * 60 * 24 },
                networkTimeoutSeconds: 3,
              },
            },
            {
              urlPattern: /\.(?:js|css)$/,
              handler: 'CacheFirst',
              options: {
                cacheName: 'static-assets',
                expiration: { maxEntries: 100, maxAgeSeconds: 60 * 60 * 24 * 30 },
              },
            },
          ],
        },
        manifest: {
          name: opts.name,
          short_name: opts.shortName,
          description: opts.description || opts.name,
          theme_color: themeColor,
          background_color: backgroundColor,
          display: 'standalone',
          scope: '/',
          start_url: '/',
          icons,
        },
      }),
    ],
  }
}
