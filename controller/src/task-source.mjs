import { assert, clone, digest, ADMISSION_VERSION } from './contracts.mjs';

/**
 * Built-in TaskSource for self-contained installations and offline tests.
 * The manifest is operator-owned input, not model/task-text authority.
 * Live workflows additionally require independent trusted task/paid grants.
 */
export class ManualTaskSource {
  constructor(manifest) {
    assert(Array.isArray(manifest),'manual task manifest required');
    this.tasks=new Map();
    for (const row of manifest) {
      assert(row?.id && row.projectId && row.source?.kind==='manual' &&
        row.source.ref && row.baseSha && row.policySha,'invalid manual task manifest');
      assert(!this.tasks.has(row.id),'duplicate manual task');
      this.tasks.set(row.id,clone(row));
    }
  }
  async readTask(taskId) {
    const row=this.tasks.get(taskId);
    if(!row)throw new Error('manual task not found');
    return clone(row);
  }
  async readAdmission(task,policySha){
    const row=await this.readTask(task.id);
    const blockers=[];
    if (row.projectId!==task.projectId) blockers.push('project mismatch');
    if (task.source.kind!=='manual'||row.source.ref!==task.source.ref)
      blockers.push('manual source identity mismatch');
    if (row.baseSha!==task.baseSha) blockers.push('pinned base SHA mismatch');
    if (policySha && row.policySha!==policySha) blockers.push('DDF policy revision mismatch');
    if(row.admitted!==true) blockers.push('manual task admission not approved');
    return {
      contractVersion:ADMISSION_VERSION,
      status:blockers.length?'BLOCKED':'ADMITTED',
      taskId:task.id,
      projectId:task.projectId,
      revision:digest(row),
      checkedAt:new Date().toISOString(),
      blockers
    };
  }
}
