import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Controller } from '../src/controller.mjs';
import { FileStateStore } from '../src/store.mjs';
import { AgentRuntime, OfflineBackend } from '../src/adapters.mjs';
import { digest, taskKey } from '../src/contracts.mjs';
import { ManualTaskSource } from '../src/task-source.mjs';

const id='demo:portable-task-8';
const spec={
  version:'ddf.jobspec/v1',mode:'offline',contextBytes:1024,
  task:{id,projectId:'my-project',source:{kind:'manual',ref:'first-task'},
    branch:'work/first-task',integrationBranch:'trunk',baseSha:'b'.repeat(40)},
  policy:{contractVersion:'ddf.policy/v1',approvalRef:'DEMO_ONLY',sha:'a'.repeat(40),
    allowedPaths:['example.txt'],publication:'allowed',humanMerge:true,maxCorrections:2,maxFallbacks:1},
  budget:{capMicros:1000,maxCalls:3}
};
const dir=await mkdtemp(path.join(os.tmpdir(),'ddf-offline-demo-'));
try {
  const runtime=new AgentRuntime({fake:{accepted:true,roles:['Executor'],offlineOnly:true,invoke:async x=>({
    version:'ddf.result/v1',taskKey:x.taskKey,taskId:x.taskId,principalId:'demo-executor',invocationId:x.invocationId,role:x.role,status:'completed',usageMicros:null
  })}});
  const pub={
    mock:true,
    publish:async({verified,key})=>({key,verifiedDigest:digest(verified),headSha:'c'.repeat(40),
      branch:spec.task.branch,base:'trunk',changeRef:{kind:'change_proposal',id:'demo-change-999'},commitCount:1,pushCount:1,
      forcePush:false,historyRewrite:false,humanMerge:true}),
    markReady:async i=>({headSha:i.headSha,changeRef:i.changeRef,draft:false,humanMerge:true})
  };
  const source=new ManualTaskSource([{
    id,projectId:spec.task.projectId,source:spec.task.source,
    baseSha:spec.task.baseSha,policySha:spec.policy.sha,admitted:true
  }]);
  const controller=new Controller({
    store:new FileStateStore(dir),
    admissionReader:task=>source.readAdmission(task,spec.policy.sha),
    backend:new OfflineBackend(runtime),
    verifier:async()=>({status:'passed',baseSha:spec.task.baseSha,
      diffDigest:'e'.repeat(64),changedPaths:['example.txt']}),
    publisher:pub,
    ciReader:async receipt=>({headSha:receipt.headSha,status:'green',requiredChecksPassed:true}),
    reviewer:{mock:true,principalId:'demo-reviewer',request:async({sha,taskKey,invocationId})=>({sha,taskKey,invocationId,status:'pass',readOnly:true,principalId:'demo-reviewer',usageMicros:null})}
  });
  await controller.initialize(spec);
  const key=taskKey(spec.task);
  await controller.invoke({taskKey:key,provider:'fake',maxReserveMicros:250});
  await controller.verify(key);
  await controller.publish(key);
  await controller.requestReview(key,{provider:'fake',maxReserveMicros:100});
  await controller.markReady(key);
  const state=await controller.store.read(key);
  console.log(JSON.stringify({status:'READY_CHANGE_SIMULATED',phase:state.phase,chargedMicros:state.ledger.chargedMicros,
    unknownCalls:state.ledger.unknownCalls,realModelCalls:0,realGitHubPublication:false,humanMerge:true},null,2));
} finally {await rm(dir,{recursive:true,force:true});}
