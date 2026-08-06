import { defineMdastPlugin } from 'satteri';

const tableOfContentsHeading = /^(?:contents|table of contents)$/i;

/**
 * Removes source-only navigation that is useful on GitHub but duplicated by
 * Starlight. This runs only while building the site; source Markdown is kept
 * unchanged.
 */
export function docsCleanup() {
  return defineMdastPlugin({
    name: 'gh-qw-docs-cleanup',
    heading(node, context) {
      const parent = context.parent(node);
      if (parent?.type !== 'root') return;

      const index = context.indexOf(node);
      if (index === 0 && node.depth === 1) {
        context.removeNode(node);
        return;
      }

      if (
        node.depth !== 2 ||
        !tableOfContentsHeading.test(context.textContent(node).trim())
      ) {
        return;
      }

      if (index === undefined) return;

      const isFirstSection = !parent.children
        .slice(0, index)
        .some((sibling) => sibling.type === 'heading' && sibling.depth >= 2);
      const nextNode = parent.children[index + 1];
      if (!isFirstSection || nextNode?.type !== 'list') return;

      context.removeNode(nextNode);
      context.removeNode(node);
    },
  });
}
