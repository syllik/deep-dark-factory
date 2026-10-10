import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Controller } from '../src/controller.mjs';
import { FileStateStore } from '../src/store.mjs';
import { AgentRuntime, OfflineBackend } from '../src/adapters.mjs';
import { digest } from '../src/contracts.mjs';

const id='syllik/deep-dark-factory#8';
const spec={
  version:'ddf.jobspec/v1',mode:'offline',contextBytes:1024,
  task:{id,repository:'syllik/deep-dark-factory',issueNumber:8,
    branch:'slave/issue-8-controller-v0',integrationBranch:'master',baseSha:'b'.repeat(40)},
  policy:{contractVersion:2,approvalRef:'DEMO_ONLY',sha:'a'.repeat(40),
    allowedPaths:['example.txt'],publication:'allowed',humanMerge:true,maxCorrections:2,maxFallbacks:1},
  budget:{capMicros:1000,maxCalls:3}
};
const dir=await mkdtemp(path.join(os.tmpdir(),'ddf-offline-demo-'));
try {
  const runtime=new AgentRuntime({fake:{accepted:true,roles:['Executor'],offlineOnly:true,invoke:async x=>({
    version:'ddf.result/v1',taskId:id,invocationId:x.invocationId,role:x.role,status:'completed',usageMicros:null
  })}});
  const pub={
    mock:true,
    publish:async({verified,key})=>({key,verifiedDigest:digest(verified),headSha:'c'.repeat(40),
      branch:spec.task.branch,base:'master',pr:999,commitCount:1,pushCount:1,
      forcePush:false,historyRewrite:false,humanMerge:true}),
    markReady:async i=>({headSha:i.headSha,draft:false,humanMerge:true})
  };
  const controller=new Controller({
    store:new FileStateStore(dir),
    admissionReader:async()=>({contractVersion:'task-admission/v1',status:'ADMITTED',
      issue:id,revision:'d'.repeat(64),checkedAt:new Date().toISOString(),blockers:[]}),
    backend:new OfflineBackend(runtime),
    verifier:async()=>({status:'passed',baseSha:spec.task.baseSha,
      diffDigest:'e'.repeat(64),changedPaths:['example.txt']}),
    publisher:pub,
    ciReader:async receipt=>({headSha:receipt.headSha,status:'green',requiredChecksPassed:true}),
    reviewer:{request:async({sha})=>({sha,status:'pass',readOnly:true,reviewerId:'separate-reviewer'})}
  });
  await controller.initialize(spec);
  await controller.invoke({taskId:id,provider:'fake',maxReserveMicros:250});
  await controller.verify(id);
  await controller.publish(id);
  await controller.requestReview(id);
  await controller.markReady(id);
  const state=await controller.store.read(id);
  console.log(JSON.stringify({status:'READY_PR_SIMULATED',phase:state.phase,chargedMicros:state.ledger.chargedMicros,
    unknownCalls:state.ledger.unknownCalls,realModelCalls:0,realGitHubPublication:false,humanMerge:true},null,2));
} finally {await rm(dir,{recursive:true,force:true});}
