import { describe, expect, test } from 'bun:test';
import { markdownToHtml } from 'satteri';
import { docsCleanup } from '../src/plugins/docs-cleanup';

describe('docs cleanup plugin', () => {
  test('removes the source H1 and manual table of contents only', () => {
    const { html } = markdownToHtml(
      [
        '# Page title',
        '',
        'Introduction.',
        '',
        '## Table of contents',
        '',
        '- [First](#first)',
        '- [Second](#second)',
        '',
        '## First',
        '',
        'Body.',
      ].join('\n'),
      { mdastPlugins: [docsCleanup] },
    );

    expect(html).not.toContain('<h1>');
    expect(html).not.toContain('Table of contents');
    expect(html).not.toContain('href="#first"');
    expect(html).toContain('<p>Introduction.</p>');
    expect(html).toContain('<h2>First</h2>');
  });

  test('does not remove a contents heading without a following list', () => {
    const { html } = markdownToHtml('# Title\n\n## Contents\n\nA prose section.', {
      mdastPlugins: [docsCleanup],
    });

    expect(html).toContain('<h2>Contents</h2>');
    expect(html).toContain('<p>A prose section.</p>');
  });

  test('preserves an H1 that is not the first content node', () => {
    const { html } = markdownToHtml('Introduction.\n\n# Deliberate section heading', {
      mdastPlugins: [docsCleanup],
    });

    expect(html).toContain('<p>Introduction.</p>');
    expect(html).toContain('<h1>Deliberate section heading</h1>');
  });

  test('preserves a later contents section with a list', () => {
    const { html } = markdownToHtml(
      '# Title\n\n## Overview\n\nBody.\n\n## Contents\n\n- Item one\n- Item two',
      { mdastPlugins: [docsCleanup] },
    );

    expect(html).toContain('<h2>Contents</h2>');
    expect(html).toContain('<li>Item one</li>');
  });

  test('does not leak leading-heading state between documents', () => {
    const plugins = [docsCleanup];
    const first = markdownToHtml('# First\n\nBody.', { mdastPlugins: plugins });
    const second = markdownToHtml('# Second\n\nBody.', { mdastPlugins: plugins });

    expect(first.html).not.toContain('<h1>');
    expect(second.html).not.toContain('<h1>');
  });
});
