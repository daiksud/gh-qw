import { realpathSync } from 'node:fs';
import { createRequire } from 'node:module';

// The preset lives with the commitlint CLI installed by mise, not in the checkout.
const require = createRequire(realpathSync(process.argv[1]));

export default {
  extends: [require.resolve('@commitlint/config-conventional')],
  // PR titles are never exempted as generated merge, revert, or fixup messages.
  defaultIgnores: false,
  rules: {
    // Conventional Commits 1.0.0 has no line-length restriction.
    'header-max-length': [0],
    'body-max-line-length': [0],
    'footer-max-line-length': [0],

    // Unlike the other selected style rules, scope-case is not set by the preset.
    'scope-case': [2, 'always', 'lower-case'],
  },
};
