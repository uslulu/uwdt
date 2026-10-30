import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, writeFileSync, existsSync } from 'node:fs';
import { resolve, relative, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const front = resolve(root, 'frontend');
const stamp = resolve(front, 'dist/.uwdt-build.json');
function digest(dir, skip = new Set()) {
  const hash = createHash('sha256');
  function walk(path) {
    for (const item of readdirSync(path, { withFileTypes: true }).sort((a,b) => a.name.localeCompare(b.name))) {
      if (skip.has(item.name) || item.name.endsWith('.tsbuildinfo')) continue;
      const p = resolve(path,item.name);
      if (item.isDirectory()) walk(p);
      else { hash.update(relative(dir,p)); hash.update('\0'); hash.update(readFileSync(p)); hash.update('\0'); }
    }
  }
  walk(dir); return hash.digest('hex');
}
const source = () => createHash('sha256').update(digest(front, new Set(['node_modules','dist','dist-ssr','.uwdt-build.json']))).update(readFileSync(fileURLToPath(import.meta.url))).digest('hex');
const output = () => digest(resolve(front,'dist'),new Set(['.uwdt-build.json']));
const environment = `${process.platform}/${process.arch}/${process.version}`;
function run(...args) {
  const result = spawnSync('npm',args,{cwd:front,stdio:'inherit',shell:process.platform === 'win32'});
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
if (process.argv.includes('--stamp')) {
  writeFileSync(stamp,JSON.stringify({source:source(),output:output(),environment},null,2));
} else {
  let previous;
  try { previous = JSON.parse(readFileSync(stamp,'utf8')); } catch {}
  if (previous && previous.environment === environment && previous.source === source() && existsSync(resolve(front,'dist/index.html')) && previous.output === output()) {
    console.log('Frontend verified; reusing dist.');
  } else {
    console.log('Frontend missing, changed or unverified; rebuilding.');
    run('ci');
    run('run','build');
  }
}
