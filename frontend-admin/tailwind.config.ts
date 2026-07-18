import type { Config } from 'tailwindcss';

// Design tokens for the admin panel. Linear/Notion-influenced: neutral grays,
// a near-black primary (slate-900 in light, white in dark), small radii, and
// minimal shadows — the panel reads as a tool, not a brand showcase.
//
// Colors resolve to CSS variables (R G B triples) defined in index.css, so a
// single `dark` class on <html> swaps the whole palette. We keep the
// `<alpha-value>` channel so Tailwind's opacity modifiers (bg-primary/40)
// still work on top of the variable.
const config: Config = {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        bg: 'rgb(var(--color-bg) / <alpha-value>)',
        card: 'rgb(var(--color-card) / <alpha-value>)',
        'card-2': 'rgb(var(--color-card-2) / <alpha-value>)',
        border: 'rgb(var(--color-border) / <alpha-value>)',
        primary: 'rgb(var(--color-primary) / <alpha-value>)',
        good: '#10B981',
        warn: '#F59E0B',
        bad: '#EF4444',
        txt: 'rgb(var(--color-txt) / <alpha-value>)',
        muted: 'rgb(var(--color-muted) / <alpha-value>)',
      },
      // Smaller radii than the previous 16/18px — feels more precise / less
      // toy-like, matches Linear/Notion cards & inputs.
      borderRadius: {
        xl: '10px',
        '2xl': '12px',
      },
      fontFamily: {
        // Inter for high information density + tabular numerals; system-ui
        // stack as fallback so it feels native on every platform.
        sans: ['Inter', 'system-ui', '-apple-system', 'ui-sans-serif', 'sans-serif'],
      },
      boxShadow: {
        // Restrained focus ring (was 3px @ 40% alpha — too loud).
        focus: '0 0 0 2px rgb(var(--color-primary) / 0.35)',
      },
    },
  },
  plugins: [],
};

export default config;
