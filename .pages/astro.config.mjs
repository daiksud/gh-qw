// @ts-check
import { defineConfig } from 'astro/config';
import { satteri } from '@astrojs/markdown-satteri';
import starlight from '@astrojs/starlight';
import mermaid from 'astro-mermaid';
import { docsCleanup } from './src/plugins/docs-cleanup.ts';
import { githubAlerts } from './src/plugins/github-alerts.ts';

const adrPages = [
  '0001-record-architecture-decisions',
  '0002-distribute-as-gh-cli-extension',
  '0003-go-cobra-prebuilt-binaries',
  '0004-ghq-directory-convention',
  '0005-dedicated-worktree-root',
  '0006-command-set-v1',
  '0007-configuration-sources',
  '0008-conventional-commits-and-release-notes',
  '0009-interactive-selection-via-fzf',
  '0010-public-api-and-semantic-versioning',
  '0011-type-named-release-labels',
  '0012-github-settings-as-code',
  '0013-node-pnpm-viteplus-toolchain',
].map((name) => `development/adr/${name}/index`);

export default defineConfig({
  site: 'https://daiksud.github.io',
  base: '/gh-qw',
  trailingSlash: 'always',
  vite: {
    build: {
      // Mermaid is loaded only on diagram pages; its core chunk is about 650 kB.
      chunkSizeWarningLimit: 700,
    },
  },
  integrations: [
    mermaid(),
    starlight({
      title: 'gh-qw',
      description:
        'Documentation for gh qw, a GitHub CLI extension for ghq-compatible repository paths and dedicated Git worktrees.',
      logo: {
        src: './src/assets/logo.svg',
      },
      favicon: '/favicon.svg',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/daiksud/gh-qw',
        },
      ],
      editLink: {
        // The glob loader records external sources as `../docs/...`. Keeping
        // `.pages/` in the base makes URL normalization resolve to `main/docs/...`.
        baseUrl: 'https://github.com/daiksud/gh-qw/edit/main/.pages/',
      },
      customCss: ['./src/styles/custom.css', './src/styles/github-alerts.css'],
      markdown: {
        processedDirs: ['../docs/'],
      },
      tableOfContents: {
        minHeadingLevel: 2,
        maxHeadingLevel: 3,
      },
      sidebar: [
        { label: 'Home', link: '/' },
        { label: 'Concept', link: 'concept/index' },
        {
          label: 'References',
          items: [
            { label: 'CLI', link: 'reference/cli/index' },
            {
              label: 'Configuration',
              link: 'reference/configuration/index',
            },
            { label: 'Versioning', link: 'reference/versioning/index' },
            {
              label: 'Repository settings',
              link: 'reference/repository-settings/index',
            },
            { label: 'Compatibility', link: 'reference/compatibility/index' },
          ],
        },
        {
          label: 'Development',
          collapsed: true,
          items: [
            { label: 'Overview', link: 'development/index' },
            'development/contributing/index',
            {
              label: 'Architecture Decision Records',
              collapsed: true,
              items: [
                'development/adr/index',
                ...adrPages,
                'development/adr/template/index',
              ],
            },
          ],
        },
      ],
    }),
  ],
  markdown: {
    processor: satteri({
      mdastPlugins: [docsCleanup, githubAlerts()],
    }),
  },
});
