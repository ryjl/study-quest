import type { Config } from 'tailwindcss';

// Design tokens mirror design.md §3 and the Flutter client's theme.dart so
// the admin panel feels like part of the same product family.
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
        primary: {
          DEFAULT: 'rgb(var(--color-primary) / <alpha-value>)',
          dark: '#7c3aed',
        },
        good: '#10B981',
        warn: '#F59E0B',
        bad: '#EF4444',
        txt: 'rgb(var(--color-txt) / <alpha-value>)',
        muted: 'rgb(var(--color-muted) / <alpha-value>)',
      },
      borderRadius: {
        xl: '16px',
        '2xl': '18px',
      },
      fontFamily: {
        sans: ['Outfit', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        focus: '0 0 0 3px rgb(var(--color-primary) / 0.4)',
        'primary-glow': '0 4px 12px rgb(var(--color-primary) / 0.4)',
      },
    },
  },
  plugins: [],
};

export default config;
