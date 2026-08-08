import { describe, expect, test } from 'vite-plus/test';
import { mdxToJs } from 'satteri';

import { mdxAutoImport } from '../src/plugins/mdx-auto-import';

const HOMEPAGE = `
<Hero>

# gh-qw

</Hero>

<CardGrid stagger>
<Card>

<CardTitle><CardIcon>🧭</CardIcon> **[Concept](concept/)**</CardTitle>

</Card>
</CardGrid>
`;

function compileHomepage(): string {
  const result = mdxToJs(HOMEPAGE, {
    mdastPlugins: [mdxAutoImport],
  });

  if (result instanceof Promise) {
    throw new TypeError('The MDX auto-import plugin must remain synchronous.');
  }

  return result.code;
}

describe('mdxAutoImport', () => {
  test('injects every homepage component import once', () => {
    const code = compileHomepage();
    const componentNames = [
      'Card',
      'CardGrid',
      'CardIcon',
      'CardTitle',
      'Hero',
      'Install',
    ];

    for (const name of componentNames) {
      expect(code).toContain(`import ${name} from`);
    }

    expect(
      code.match(/import (?:Card|CardGrid|CardIcon|CardTitle|Hero|Install) from/g),
    ).toHaveLength(componentNames.length);
  });

  test('creates a fresh injection state for every MDX document', () => {
    const first = compileHomepage();
    const second = compileHomepage();

    const imports =
      /import (?:Card|CardGrid|CardIcon|CardTitle|Hero|Install) from/g;

    expect(first.match(imports)).toHaveLength(6);
    expect(second.match(imports)).toHaveLength(6);
  });
});
