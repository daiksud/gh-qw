import { describe, expect, test } from 'bun:test';

const css = await Bun.file(new URL('../src/styles/github-alerts.css', import.meta.url)).text();

const expectedColors = {
  light: {
    note: { title: '#0969da', border: '#0969da' },
    tip: { title: '#1a7f37', border: '#1a7f37' },
    important: { title: '#8250df', border: '#8250df' },
    warning: { title: '#9a6700', border: '#9a6700' },
    caution: { title: '#d1242f', border: '#cf222e' },
  },
  dark: {
    note: { title: '#4493f8', border: '#1f6feb' },
    tip: { title: '#3fb950', border: '#238636' },
    important: { title: '#ab7df8', border: '#8957e5' },
    warning: { title: '#d29922', border: '#9e6a03' },
    caution: { title: '#f85149', border: '#da3633' },
  },
} as const;

function declarations(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp(`${escaped}\\s*\\{([^}]*)\\}`).exec(css);
  if (!match) throw new Error(`Missing CSS selector: ${selector}`);
  return match[1]!;
}

function channel(value: number): number {
  const normalized = value / 255;
  return normalized <= 0.04045
    ? normalized / 12.92
    : ((normalized + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  const rgb = [1, 3, 5].map((index) => Number.parseInt(hex.slice(index, index + 2), 16));
  return 0.2126 * channel(rgb[0]!) + 0.7152 * channel(rgb[1]!) + 0.0722 * channel(rgb[2]!);
}

function contrast(foreground: string, background: string): number {
  const lighter = Math.max(luminance(foreground), luminance(background));
  const darker = Math.min(luminance(foreground), luminance(background));
  return (lighter + 0.05) / (darker + 0.05);
}

describe('GitHub Alerts styles', () => {
  test('pins GitHub title and border colors for both themes', () => {
    for (const [theme, variants] of Object.entries(expectedColors)) {
      for (const [variant, values] of Object.entries(variants)) {
        const selector =
          theme === 'dark'
            ? `:root[data-theme="dark"] .markdown-alert-${variant}`
            : `.markdown-alert-${variant}`;
        const block = declarations(selector);

        expect(block).toContain(`--markdown-alert-border: ${values.border}`);
        expect(block).toContain(`--markdown-alert-title: ${values.title}`);
      }
    }
  });

  test('keeps every visible title at WCAG AA contrast', () => {
    for (const values of Object.values(expectedColors.light)) {
      expect(contrast(values.title, '#ffffff')).toBeGreaterThanOrEqual(4.5);
    }
    for (const values of Object.values(expectedColors.dark)) {
      expect(contrast(values.title, '#17181c')).toBeGreaterThanOrEqual(4.5);
    }
  });

  test('falls back to system colors in forced-colors mode', () => {
    expect(css).toContain('@media (forced-colors: active)');
    expect(css).toContain('--markdown-alert-border: CanvasText !important');
    expect(css).toContain('--markdown-alert-title: CanvasText !important');
  });
});
