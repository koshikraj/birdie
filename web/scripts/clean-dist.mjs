// Empties ../internal/webui/dist before a build, keeping the .gitkeep file
// that holds the Go embed folder in place.
import { existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const dist = resolve(here, '../../internal/webui/dist');

if (!existsSync(dist)) {
  mkdirSync(dist, { recursive: true });
} else {
  for (const name of readdirSync(dist)) {
    if (name === '.gitkeep') continue;
    rmSync(join(dist, name), { recursive: true, force: true });
  }
}
