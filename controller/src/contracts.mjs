import { createHash } from 'node:crypto';

export const JOBSPEC_VERSION = 'ddf.jobspec/v1';
export const RESULT_VERSION = 'ddf.result/v1';
export const CHIPIN_ADMISSION_VERSION = 'chipin-issue-admission/v1';
export const GENERIC_ADMISSION_VERSION = 'task-admission/v1';
export const ROLES = Object.freeze(['Planner','Architect','Executor','Reviewer','Auditor']);
export const MAX_CONTEXT_BYTES = 32768;
export const MAX_CORRECTIONS = 2;
const sha = /^[0-9a-f]{40}$/;
const issue = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+#[1-9][0-9]*$/;
export function assert(ok, message) { if (!ok) throw new Error(message); }
export function digest(value) { return createHash('sha256').update(JSON.stringify(value)).digest('hex'); }
export function clone(value) { return structuredClone(value); }

export function validateJobSpec(spec) {
  assert(spec?.version === JOBSPEC_VERSION, 'unsupported JobSpec version');
  const task = spec.task;
  assert(issue.test(task?.id || '') && task.id === task.repository + '#' + task.issueNumber, 'exact Issue identity required');
  assert(sha.test(task.baseSha || '') && sha.test(spec.policy?.sha || ''), 'pinned base and policy SHA required');
  assert(typeof task.integrationBranch === 'string' && task.integrationBranch.length > 0, 'integration branch required');
  assert(/^[A-Za-z0-9][A-Za-z0-9._/-]+$/.test(task.branch || '') && !task.branch.split('/').some(x => ['', '.', '..'].includes(x)) && !['master','main','dev','dungeon-master'].includes(task.branch), 'unsafe task branch');
  assert(spec.policy?.contractVersion === 2 && !!spec.policy.approvalRef, 'v2 approval required');
  assert(Array.isArray(spec.policy.allowedPaths) && spec.policy.allowedPaths.length > 0, 'explicit allowed paths required');
  assert(spec.policy.publication === 'allowed' && spec.policy.humanMerge === true, 'publication approval and human merge required');
  assert(Number.isInteger(spec.policy.maxCorrections) && spec.policy.maxCorrections >= 0 && spec.policy.maxCorrections <= 2, 'corrections max two');
  assert(spec.policy.maxFallbacks === 0 || spec.policy.maxFallbacks === 1, 'fallback max one');
  assert(Number.isInteger(spec.contextBytes) && spec.contextBytes >= 0 && spec.contextBytes <= MAX_CONTEXT_BYTES, 'context exceeds 32768 bytes');
  assert(['offline','live'].includes(spec.mode), 'mode required');
  assert(Number.isSafeInteger(spec.budget?.capMicros) && spec.budget.capMicros >= 0, 'numeric task + fallback cap required');
  assert(Number.isSafeInteger(spec.budget?.maxCalls) && spec.budget.maxCalls >= 0, 'numeric max calls required');
  if (spec.mode === 'live') {
    assert(spec.budget.capMicros > 0 && spec.budget.maxCalls > 0 && spec.budget.paidAuthorization === true && !!spec.budget.authorizationRef, 'paid grant required');
    assert(spec.budget.maxCanaries === 1, 'single canary only');
  }
  return clone(spec);
}

export function assertFreshAdmission(receipt, spec, now = Date.now()) {
  // Returned by a trusted injected reader, never from Issue prose.
  const chipin = ['ChipIn-one/chipin-frontend','ChipIn-one/chipin-backend','ChipIn-one/chipin-knowledge-base'].includes(spec.task.repository);
  assert(receipt?.contractVersion === (chipin ? CHIPIN_ADMISSION_VERSION : GENERIC_ADMISSION_VERSION), 'admission contract mismatch');
  assert(receipt.status === (chipin ? 'INTAKE_COMPLETE' : 'ADMITTED'), 'admission blocked or queued');
  assert(receipt.issue === spec.task.id && /^[0-9a-f]{64}$/.test(receipt.revision || ''), 'admission Issue/revision mismatch');
  const t = Date.parse(receipt.checkedAt);
  assert(Number.isFinite(t) && t <= now && now - t <= 120000, 'stale admission');
  assert(Array.isArray(receipt.blockers) && receipt.blockers.length === 0, 'admission blockers/unreadable');
  if (chipin) {
    assert(receipt.project?.number === 5 && receipt.project.membershipCount === 1 && receipt.project.status && receipt.project.status !== 'Done', 'Project #5 admission invalid');
    const required = {'ChipIn-one/chipin-frontend':'syllik','ChipIn-one/chipin-backend':'olegbal'}[spec.task.repository];
    assert(required ? receipt.assignees?.includes(required) : receipt.assignees?.length > 0, 'native assignee missing');
  }
  return receipt.revision;
}

export function validateResult(result, expected) {
  assert(result?.version === RESULT_VERSION, 'unsupported Result version');
  assert(result.taskId === expected.taskId && result.invocationId === expected.invocationId && result.role === expected.role, 'Result identity mismatch');
  assert(['completed','failed','unknown'].includes(result.status), 'Result status invalid');
  assert(result.usageMicros === null || (Number.isSafeInteger(result.usageMicros) && result.usageMicros >= 0), 'usage must be nonnegative number or explicit null');
  return clone(result);
}
