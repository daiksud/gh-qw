import { fileURLToPath } from 'node:url';

import { defineMdastPlugin, type MdastNode } from 'satteri';

const componentsDirectory = new URL('../components/', import.meta.url);

const componentImports = {
  Card: 'Card.astro',
  Cards: 'Cards.astro',
  Hero: 'Hero.astro',
  Install: 'Install.astro',
} as const;

const importSource = Object.entries(componentImports)
  .map(([name, file]) => {
    const path = fileURLToPath(new URL(file, componentsDirectory));
    return `import ${name} from ${JSON.stringify(path)};`;
  })
  .join('\n');

export function mdxAutoImport() {
  let injected = false;

  return defineMdastPlugin({
    name: 'gh-qw-mdx-auto-import',
    mdxJsxFlowElement(node, context) {
      if (injected || context.sourceFormat !== 'mdx') return;

      let ancestor: MdastNode = node;
      while (true) {
        const parent: MdastNode | undefined = context.parent(ancestor);
        if (parent === undefined) return;
        if (parent.type === 'root') break;
        ancestor = parent;
      }

      context.insertBefore(ancestor, {
        type: 'mdxjsEsm',
        value: importSource,
      });
      injected = true;
    },
  });
}
