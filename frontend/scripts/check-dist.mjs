import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';

const names = ['TOKEN_SECRET', 'DB_PASSWORD', 'SEED_TEACHER_A_PASSWORD', 'SEED_STUDENT_A1_PASSWORD', 'SEED_STUDENT_B1_PASSWORD'];
async function inspect(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) { await inspect(file); continue; }
    const text = await readFile(file, 'utf8');
    if (names.some(name => text.includes(name) || (process.env[name] && text.includes(process.env[name])))) {
      throw new Error('Build contains a server credential marker; details suppressed');
    }
  }
}
await inspect('dist');
console.log('PASS build contains no server credential names or supplied values');
