// Fails if the npm tarball would ship test files. Run after `npm run build`.
import { execFileSync } from 'node:child_process';

const [pack] = JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], { encoding: 'utf8' }));
const tests = pack.files.map((f) => f.path).filter((p) => /\.test\./.test(p));
if (tests.length) {
  console.error(`npm tarball ships ${tests.length} test files:\n  ${tests.join('\n  ')}`);
  process.exit(1);
}
console.log(`npm tarball: ${pack.files.length} files, no tests`);
