import { createHash } from 'node:crypto';

export const JOBSPEC_VERSION = 'ddf.jobspec/v1';
export const RESULT_VERSION = 'ddf.result/v1';
export const POLICY_VERSION = 'ddf.policy/v1';
export const ADMISSION_VERSION = 'ddf.admission/v1';
export const ROLES = Object.freeze(['Planner','Architect','Executor','Reviewer','Auditor']);
export const MAX_CONTEXT_BYTES = 32768;
export const MAX_CORRECTIONS = 2;
const sha = /^[0-9a-f]{40}$/;
const identity = /^[a-zA-Z0-9][a-zA-Z0-9._:/#-]{0,199}$/;
const branch = /^[a-zA-Z0-9][a-zA-Z0-9._/-]+$/;

export function assert(ok,message){ if(!ok) throw new Error(message); }
export function digest(value){ return createHash('sha256').update(JSON.stringify(value)).digest('hex'); }
export function clone(value){ return structuredClone(value); }
export function taskKey(task) {
  assert(identity.test(task?.id||'') && identity.test(task?.projectId||'') &&
    identity.test(task?.source?.kind||'') && typeof task?.source?.ref==='string' &&
    task.source.ref.length>0 && task.source.ref.length<=512,'invalid scoped task identity');
  return 'ddf-task:' + digest([task.projectId,task.source.kind,task.source.ref,task.id]);
}

export function validateJobSpec(spec){
  assert(spec?.version===JOBSPEC_VERSION,'unsupported JobSpec version');
  const task=spec.task;
  assert(identity.test(task?.id||''),'stable opaque task ID required');
  assert(identity.test(task?.projectId||''),'explicit project ID required');
  assert(task.source && identity.test(task.source.kind||'') &&
    typeof task.source.ref==='string' && task.source.ref.length>0 &&
    task.source.ref.length<=512,'TaskSource adapter/ref required');
  assert(!task.source.revision || typeof task.source.revision==='string','invalid TaskSource revision');
  assert(sha.test(task.baseSha||'') && sha.test(spec.policy?.sha||''),'pinned git base and DDF policy SHA required');
  assert(typeof task.integrationBranch==='string' && branch.test(task.integrationBranch) &&
    !task.integrationBranch.split('/').some(s=>!s||s==='.'||s==='..'),'invalid integration branch');
  assert(branch.test(task.branch||'') && task.branch!==task.integrationBranch &&
    !task.branch.split('/').some(s=>!s||s==='.'||s==='..'),'unsafe or conflicting task branch');
  assert(spec.policy?.contractVersion===POLICY_VERSION && !!spec.policy.approvalRef,'DDF-native policy grant required');
  assert(Array.isArray(spec.policy.allowedPaths)&&spec.policy.allowedPaths.length>0,'explicit allowed paths required');
  assert(spec.policy.publication==='allowed' && spec.policy.humanMerge===true,'publication approval and human merge required');
  assert(Number.isInteger(spec.policy.maxCorrections)&&spec.policy.maxCorrections>=0 &&
    spec.policy.maxCorrections<=MAX_CORRECTIONS,'corrections max two');
  assert(spec.policy.maxFallbacks===0||spec.policy.maxFallbacks===1,'fallback max one');
  assert(Number.isInteger(spec.contextBytes)&&spec.contextBytes>=0 &&
    spec.contextBytes<=MAX_CONTEXT_BYTES,'context exceeds 32768 bytes');
  assert(spec.mode==='offline'||spec.mode==='live','mode required');
  assert(Number.isSafeInteger(spec.budget?.capMicros)&&spec.budget.capMicros>=0,
    'numeric task + fallback cap required');
  assert(Number.isSafeInteger(spec.budget?.maxCalls)&&spec.budget.maxCalls>=0,'numeric max calls required');
  if(spec.mode==='live'){
    assert(spec.budget.capMicros>0 && spec.budget.maxCalls>0 &&
      spec.budget.paidAuthorization===true && !!spec.budget.authorizationRef,'paid grant required');
    assert(spec.budget.maxCanaries===1,'single canary only');
  }
  return clone(spec);
}

// AdmissionReader is trusted and DDF-owned. TaskSource/forge-specific metadata is
// validated only by its adapter; none of it is a universal core prerequisite.
export function assertFreshAdmission(receipt,spec,now=Date.now()){
  assert(receipt?.contractVersion===ADMISSION_VERSION && receipt.status==='ADMITTED',
    'DDF admission blocked, queued or from unsupported contract');
  assert(receipt.taskId===spec.task.id && receipt.projectId===spec.task.projectId &&
    receipt.taskKey===taskKey(spec.task) &&
    /^[a-f0-9]{64}$/.test(receipt.revision||''),'admission task/project/revision mismatch');
  const checkedAt=Date.parse(receipt.checkedAt);
  assert(Number.isFinite(checkedAt)&&checkedAt<=now&&now-checkedAt<=120000,'stale admission');
  assert(Array.isArray(receipt.blockers)&&receipt.blockers.length===0,'admission blockers or unreadable');
  return receipt.revision;
}

export function validateResult(result,expected){
  assert(result?.version===RESULT_VERSION,'unsupported Result version');
  assert(result.taskKey===expected.taskKey && result.taskId===expected.taskId &&
    result.invocationId===expected.invocationId &&
    result.role===expected.role,'Result identity mismatch');
  assert(['completed','failed','unknown'].includes(result.status),'Result status invalid');
  assert(identity.test(result.principalId||''),'authenticated invocation principal required');
  assert(result.usageMicros===null ||
    (Number.isSafeInteger(result.usageMicros)&&result.usageMicros>=0),
    'usage must be a nonnegative integer or explicit null');
  return clone(result);
}
