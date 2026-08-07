import { defineConfig } from 'vite-plus';

export default defineConfig({
  run: {
    tasks: {
      'site:build': {
        command: 'astro build',
        input: [{ auto: true }, '!dist/**', '!node_modules/**'],
        output: ['dist/**'],
      },
      'site:dev': {
        command: 'astro dev',
      },
      'site:preview': {
        command: 'astro preview',
      },
    },
  },
});
