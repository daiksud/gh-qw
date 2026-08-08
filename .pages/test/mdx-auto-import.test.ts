import { describe, expect, test } from 'vite-plus/test';
import { mdxToJs } from 'satteri';

import { mdxAutoImport } from '../src/plugins/mdx-auto-import';

const HOMEPAGE = `
<Hero>

# gh-qw

</Hero>

<Cards>
<Card>

Card content.

</Card>
</Cards>
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

    expect(code).toContain('import Card from');
    expect(code).toContain('import Cards from');
    expect(code).toContain('import Hero from');
    expect(code).toContain('import Install from');
    expect(code.match(/import (?:Card|Cards|Hero|Install) from/g)).toHaveLength(
      4,
    );
  });

  test('creates a fresh injection state for every MDX document', () => {
    const first = compileHomepage();
    const second = compileHomepage();

    expect(first.match(/import (?:Card|Cards|Hero|Install) from/g)).toHaveLength(
      4,
    );
    expect(second.match(/import (?:Card|Cards|Hero|Install) from/g)).toHaveLength(
      4,
    );
  });
});
