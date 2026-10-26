import { realpathSync } from 'node:fs';
import { createRequire } from 'node:module';

// The preset lives with the commitlint CLI installed by mise, not in the checkout.
const require = createRequire(realpathSync(process.argv[1]));

export default {
  extends: [require.resolve('@commitlint/config-conventional')],
};
