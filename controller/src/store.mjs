import { mkdir, open, readFile, rename, rm } from 'node:fs/promises';
import path from 'node:path';
import { digest } from './contracts.mjs';

// Offline, single-host store only. Unsafe for ephemeral cross-Action production!
export class FileStateStore {
  constructor(root) { this.root = root; }
  location(id) { return path.join(this.root, digest(id)); }
  async withTask(id, fn) {
    const dir = this.location(id), lock = path.join(dir, '.lock');
    await mkdir(dir, {recursive:true});
    try { await mkdir(lock); }
    catch (e) { if (e.code === 'EEXIST') throw new Error('task locked; explicit recovery required'); throw e; }
    try {
      const file = path.join(dir,'state.json');
      let state;
      try { state = JSON.parse(await readFile(file,'utf8')); }
      catch (e) { if (e.code !== 'ENOENT') throw e; state = null; }
      const save = async next => {
        const tmp = path.join(dir,'state.' + process.pid + '.' + digest(next).slice(0,12) + '.tmp');
        const h = await open(tmp,'wx',0o600);
        try { await h.writeFile(JSON.stringify(next,null,2) + '\n'); await h.sync(); } finally { await h.close(); }
        await rename(tmp,file);
        const d = await open(dir,'r');
        try { await d.sync(); } finally { await d.close(); }
        state = structuredClone(next);
      };
      return await fn(() => structuredClone(state),save);
    } finally { await rm(lock,{recursive:true,force:true}); }
  }
  async read(id) {
    try { return JSON.parse(await readFile(path.join(this.location(id),'state.json'),'utf8')); }
    catch (e) { if (e.code === 'ENOENT') return null; throw e; }
  }
  async recoverOrphanedLock(id, {operatorConfirmedNoWorker=false}={}) {
    if (!operatorConfirmedNoWorker) throw new Error('manual worker-death confirmation required');
    await rm(path.join(this.location(id),'.lock'),{recursive:true});
  }
}
