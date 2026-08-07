import { defineConfig } from 'vite-plus';

export default defineConfig({
  run: {
    tasks: {
      dev: {
        command: 'astro dev',
        cache: false,
      },
      preview: {
        command: 'astro preview',
        cache: false,
      },
      build: {
        command: 'astro build',
        input: [{ auto: true }, '!dist/**', '!node_modules/**'],
        output: ['dist/**'],
      },
      check: {
        command: 'astro check && vp check --no-fmt',
      },
      'check:links': {
        command: 'node ./scripts/check-links.ts',
        dependsOn: ['build'],
      },
      test: {
        command: 'vp test',
      },
      validate: {
        command: 'vp run test && vp run check && vp run check:links',
        cache: false,
      },
      format: {
        command: 'vp fmt --write src scripts test astro.config.mjs vite.config.ts',
        cache: false,
      },
    },
  },
});
