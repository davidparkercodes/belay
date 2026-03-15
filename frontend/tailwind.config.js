import belayPreset from './src/styles/tailwind.preset.js'

/** @type {import('tailwindcss').Config} */
export default {
  presets: [belayPreset],
  content: [
    './index.html',
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {},
  },
  plugins: [],
}
