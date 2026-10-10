import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Controller } from '../src/controller.mjs';
import { AgentRuntime, OfflineBackend, sanitizeExecutorEnv, ActionsBackend, DedicatedMachineBackend, LocalEndpointBackend, capabilityAcceptance } from '../src/adapters.mjs';
import { FileStateStore } from '../src/store.mjs';
import { validateJobSpec, validateResult, digest, taskKey } from '../src/contracts.mjs';

const taskId='standalone:task-8';
const baseSha='b'.repeat(40),headSha='c'.repeat(40),policySha='a'.repeat(40);
function spec(overrides={}) {
  return {
    version:'ddf.jobspec/v1',mode:'offline',contextBytes:1800,
    task:{id:taskId,projectId:'example-project',source:{kind:'manual',ref:'task-8'},branch:'work/task-8',integrationBranch:'trunk',baseSha},
    policy:{contractVersion:'ddf.policy/v1',approvalRef:'human:approved',sha:policySha,allowedPaths:['src/a.mjs'],publication:'allowed',humanMerge:true,maxCorrections:2,maxFallbacks:1},
    budget:{capMicros:1000,maxCalls:3},
    ...overrides
  };
}
const scopedKey=taskKey(spec().task);
const admission=(task)=>({contractVersion:'ddf.admission/v1',status:'ADMITTED',taskId:task.id,taskKey:taskKey(task),projectId:task.projectId,revision:'d'.repeat(64),checkedAt:new Date().toISOString(),blockers:[]});
function rig({admissionReader=admission,invoke,backend,ciReader,reviewer,publisher,paidGrantReader,capabilityReader,approvalReader,correctionGrantReader}={}) {
  const models = new AgentRuntime({mock:{accepted:true,roles:['Executor'],offlineOnly:true,invoke:async x=>({
    taskKey:x.taskKey,taskId:x.taskId,principalId:'executor-1',
    ...await (invoke||((v)=>({version:'ddf.result/v1',invocationId:v.invocationId,role:v.role,status:'completed',usageMicros:null})))(x)
  })}});
  const results = new Map();
  const reviewerImpl=reviewer||{request:async()=>({status:'pass'})};
  const mockReviewer={...reviewerImpl,mock:true,
    principalId:reviewerImpl.principalId||'independent-reviewer',
    async request(args) {
      return {taskKey:args.taskKey,invocationId:args.invocationId,sha:args.sha,
        status:'pass',readOnly:true,principalId:this.principalId,usageMicros:null,
        ...await reviewerImpl.request(args)};
    }};
  const trustedPublisher = publisher || {
    mock:true,
    async publish({spec,verified,key}) {
      const receipt={key,verifiedDigest:digest(verified),headSha,branch:spec.task.branch,base:spec.task.integrationBranch,changeRef:{kind:'pull_request',id:'offline-pr-8'},adapter:{github:{number:8}},
        commitCount:1,pushCount:1,forcePush:false,historyRewrite:false,humanMerge:true};
      results.set(key,receipt);
      return receipt;
    },
    async lookup(key) {return results.get(key);},
    async markReady(intent) {return {headSha:intent.headSha,changeRef:intent.changeRef,draft:false,humanMerge:true};},
    async lookupReady(intent) {return {headSha:intent.headSha,changeRef:intent.changeRef,draft:false,humanMerge:true};}
  };
  return {
    store:null,
    options:{admissionReader,approvalReader,correctionGrantReader,paidGrantReader,capabilityReader,backend:backend||new OfflineBackend(models),
      verifier:async s=>({status:'passed',baseSha:s.task.baseSha,changedPaths:['src/a.mjs'],diffDigest:'e'.repeat(64)}),
      publisher:trustedPublisher,
      ciReader:ciReader|| (async pub=>({status:'green',headSha:pub.headSha,requiredChecksPassed:true})),
      reviewer:mockReviewer}
  };
}
async function setup(t,options={}) {
  const dir=await mkdtemp(path.join(os.tmpdir(),'ddf-controller-'));
  t.after(()=>rm(dir,{recursive:true,force:true}));
  const store=new FileStateStore(dir), f=rig(options);
  return {store,controller:new Controller({store,...f.options}),...f.options};
}
async function execute(c,options={}) {return c.invoke({taskKey:scopedKey,kind:'implementation',provider:'mock',maxReserveMicros:250,...options});}
async function published(c) {
  await c.initialize(spec());
  await execute(c);
  await c.verify(scopedKey);
  return c.publish(scopedKey);
}

test('offline only: exact mock lifecycle stops at ready PR, never merges',async t=>{
  const {controller:c,store}=await setup(t);
  const pub=await published(c);
  assert.equal(pub.headSha,headSha);
  await c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100});
  await c.markReady(scopedKey);
  const state=await store.read(scopedKey);
  assert.equal(state.phase,'READY_CHANGE');
  assert.equal(state.ledger.chargedMicros,350);
  assert.equal(state.ledger.calls,2);
  assert.equal(state.ledger.unknownCalls,2);
  assert.equal(state.publication.receipt.headSha,headSha);
  assert.equal(state.spec.mode,'offline');
});

test('rejects malformed or over-context standalone JobSpec and absent paid grant',async()=>{
  assert.throws(()=>validateJobSpec(spec({contextBytes:32769})),/context/);
  assert.throws(()=>validateJobSpec(spec({budget:{capMicros:-1,maxCalls:1}})),/numeric/);
  assert.throws(()=>validateJobSpec(spec({policy:{...spec().policy,maxCorrections:3}})),/corrections/);
  const live=spec({mode:'live',budget:{capMicros:1000,maxCalls:1,paidAuthorization:true,authorizationRef:'grant',maxCanaries:1}});
  assert.equal(validateJobSpec(live).mode,'live');
});

test('admission queued, stale and mismatched exact task all fail closed',async t=>{
  for(const bad of [
    {status:'QUEUED'},
    {checkedAt:'2020-01-01T00:00:00Z'},
    {taskId:'another:task-1'},
    {blockers:['invalid manual task']}
  ]) {
    const {controller:c}=await setup(t,{admissionReader:task=>({...admission(task),...bad})});
    await assert.rejects(c.initialize(spec()),/admission|stale|mismatch|blockers/);
  }
});

test('any task source works without GitHub Issue, Projects or org fields',async t=>{
  const s=spec({task:{...spec().task,id:'gitlab:ticket-100',source:{kind:'gitlab',ref:'group/project/-/issues/100'}}});
  const {controller:c,store}=await setup(t);
  await c.initialize(s);
  assert.equal((await store.read(taskKey(s.task))).phase,'ADMITTED');
});

test('built-in ManualTaskSource requires independent operator-admitted manifest',async t=>{
  const {ManualTaskSource}=await import('../src/task-source.mjs');
  const job=spec();
  const manifest={id:job.task.id,projectId:job.task.projectId,source:job.task.source,
    baseSha:job.task.baseSha,policySha:job.policy.sha,admitted:true};
  const source=new ManualTaskSource([manifest]);
  const {controller:c}=await setup(t,{admissionReader:task=>source.readAdmission(task,job.policy.sha)});
  assert.equal((await c.initialize(job)).phase,'ADMITTED');
  const wrong=new ManualTaskSource([{...manifest,admitted:false}]);
  const {controller:blocked}=await setup(t,{admissionReader:task=>wrong.readAdmission(task,job.policy.sha)});
  await assert.rejects(blocked.initialize(job),/admission blocked/);
  const drift=new ManualTaskSource([{...manifest,baseSha:'e'.repeat(40)}]);
  const {controller:stale}=await setup(t,{admissionReader:task=>drift.readAdmission(task,job.policy.sha)});
  await assert.rejects(stale.initialize(job),/admission blocked/);
});

test('unknown usage debits full reserved cost and enforces shared cap',async t=>{
  const {controller:c,store}=await setup(t);
  await c.initialize(spec({budget:{capMicros:300,maxCalls:3}}));
  await execute(c);
  const state=await store.read(scopedKey);
  assert.equal(state.ledger.chargedMicros,250);
  assert.equal(state.ledger.unknownCalls,1);
  await assert.rejects(c.invoke({taskKey:scopedKey,role:'Executor',provider:'mock',kind:'fallback',maxReserveMicros:100}),/fallback|cap/);
});

test('reserve persisted before backend invocation and crash blocks auto-replay',async t=>{
  let before;
  const dir=await mkdtemp(path.join(os.tmpdir(),'ddf-crash-'));
  t.after(()=>rm(dir,{recursive:true,force:true}));
  const store=new FileStateStore(dir);
  const f=rig({backend:{offlineSafe:true,run:async()=>{
    before=await store.read(scopedKey);
    throw new Error('ambiguous dispatch');
  }}});
  const c=new Controller({store,...f.options});
  await c.initialize(spec());
  await assert.rejects(execute(c),/uncertain/);
  assert.equal(before.invocations[0].status,'RESERVED');
  assert.equal(before.ledger.reservedMicros,250);
  assert.equal((await store.read(scopedKey)).phase,'BLOCKED_UNCERTAIN');
  await assert.rejects(execute(c),/blocked/);
});

test('atomic store lock rejects concurrent tasks and requires operator recovery',async t=>{
  const {store}=await setup(t);
  let release;
  const hold=new Promise(resolve=>{release=resolve;});
  let entered;
  const enteredP=new Promise(resolve=>{entered=resolve;});
  const first=store.withTask(scopedKey,async()=>{entered();await hold;return true;});
  await enteredP;
  await assert.rejects(store.withTask(scopedKey,async()=>true),/locked/);
  await assert.rejects(store.recoverOrphanedLock(scopedKey),/manual/);
  release();
  assert.equal(await first,true);
});

test('failed invocation may use exactly one fallback with same ledger',async t=>{
  let calls=0;
  const {controller:c,store}=await setup(t,{invoke: x=>({
    version:'ddf.result/v1',taskId,role:x.role,invocationId:x.invocationId,
    status:++calls===1?'failed':'completed',usageMicros:null
  })});
  await c.initialize(spec());
  assert.equal((await execute(c)).phase,'BLOCKED');
  assert.equal((await c.invoke({taskKey:scopedKey,role:'Executor',provider:'mock',kind:'fallback',maxReserveMicros:250})).phase,'EXECUTED');
  assert.equal((await store.read(scopedKey)).ledger.chargedMicros,500);
  await assert.rejects(c.invoke({taskKey:scopedKey,role:'Executor',provider:'mock',kind:'fallback',maxReserveMicros:250}),/fallback/);
});

test('pending callback is settled once; conflicting repeats rejected',async t=>{
  const {controller:c}=await setup(t,{backend:{offlineSafe:true,run:async()=>({kind:'pending',reference:'job-1'})}});
  await c.initialize(spec());
  const first=await execute(c);
  const result={version:'ddf.result/v1',taskKey:scopedKey,taskId,principalId:'executor-1',role:'Executor',invocationId:first.invocationId,status:'completed',usageMicros:null};
  assert.equal((await c.receiveResult(scopedKey,first.invocationId,result)).phase,'EXECUTED');
  assert.equal((await c.receiveResult(scopedKey,first.invocationId,result)).kind,'already-settled');
  await assert.rejects(c.receiveResult(scopedKey,first.invocationId,{...result,usageMicros:0}),/conflicting/);
});

test('publication holds durable intent, only lookup can resolve ambiguous push',async t=>{
  let receipt;
  const mock={
    mock:true,
    publish:async ({key,verified,spec})=>{
      receipt={key,verifiedDigest:digest(verified),headSha,branch:spec.task.branch,base:spec.task.integrationBranch,
        changeRef:{kind:'merge_request',id:'sample-mr-10'},commitCount:1,pushCount:1,forcePush:false,historyRewrite:false,humanMerge:true};
      throw new Error('lost response');
    },
    lookup:async()=>receipt
  };
  const {controller:c,store}=await setup(t,{publisher:mock});
  await c.initialize(spec());await execute(c);await c.verify(scopedKey);
  await assert.rejects(c.publish(scopedKey),/uncertain/);
  assert.equal((await store.read(scopedKey)).publication.status,'PENDING');
  const recovered=await c.reconcilePublication(taskId);
  assert.equal(recovered.changeRef.id,'sample-mr-10');
  await assert.rejects(c.publish(scopedKey),/un-published/);
});

test('review deduplicated on actually published SHA; CI must be green',async t=>{
  const {controller:c}=await setup(t);
  await published(c);
  await c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100});
  await assert.rejects(c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100}),/published PR required|already requested/);
  const {controller:other}=await setup(t,{ciReader:async()=>({status:'green',headSha:'f'.repeat(40),requiredChecksPassed:true})});
  await published(other);
  await assert.rejects(other.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100}),/current published SHA/);
});

test('third correction denied, even if task attempts repeated findings',async t=>{
  const {controller:c,store}=await setup(t,{reviewer:{request:async({sha})=>({sha,status:'findings',readOnly:true,reviewerId:'independent-reviewer'})}});
  await published(c);
  await c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100});
  await store.withTask(scopedKey,async(get,save)=>{
    const s=get();s.corrections=2;await save(s);
  });
  await assert.rejects(c.invoke({taskKey:scopedKey,role:'Executor',provider:'mock',kind:'correction',maxReserveMicros:250,
    authorization:{headSha,reference:'human:correction',findingsPackageCount:1}}),/correction cap/);
});

test('unapproved correction findings package fails closed',async t=>{
  const {controller:c}=await setup(t,{reviewer:{request:async({sha})=>({sha,status:'findings',readOnly:true,reviewerId:'independent-reviewer'})}});
  await published(c);await c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100});
  await assert.rejects(c.invoke({taskKey:scopedKey,role:'Executor',provider:'mock',kind:'correction',maxReserveMicros:250,
    authorization:{headSha,reference:'human:correction',findingsPackageCount:2}}),/consolidated/);
});

test('provider/backend ports stay separate, and executor env excludes tokens',async()=>{
  const env=sanitizeExecutorEnv({PATH:'/bin',HOME:'/tmp',GITHUB_TOKEN:'secret',PUBLISHER_TOKEN:'another'});
  assert.deepEqual(env,{PATH:'/bin',HOME:'/tmp'});
  for(const x of [new ActionsBackend(async()=>42),new DedicatedMachineBackend(async()=>42),new LocalEndpointBackend(async()=>42)]) {
    assert.equal((await x.run({invocationId:'id',jobSpecDigest:'sha',role:'Executor',provider:'mock'})).kind,'pending');
  }
  assert.equal(capabilityAcceptance.codex.status,'PENDING');
  assert.equal(capabilityAcceptance.claude.status,'PENDING');
  assert.equal(capabilityAcceptance.local.status,'PENDING');
  assert.throws(()=>validateResult({version:'ddf.result/v1',taskKey:scopedKey,taskId,principalId:'executor-1',role:'Executor',invocationId:'a',status:'completed'},
    {taskKey:scopedKey,taskId,role:'Executor',invocationId:'a'}),/usage/);
});

test('Codex, Claude and local endpoint require independent capability acceptances',async t=>{
  const live=spec({mode:'live',budget:{capMicros:400,maxCalls:1,maxCanaries:1,
    paidAuthorization:true,authorizationRef:'human:exact-numeric-grant'}});
  for(const provider of ['codex','claude','local']) {
    const {controller:c,store}=await setup(t,{
      approvalReader:async task=>({status:'APPROVED',taskId:task.id,taskKey:taskKey(task),
        approvalRef:'human:approved',policySha,baseSha,publication:'allowed',humanMerge:true,
        maxCorrections:2,maxFallbacks:1,allowedPaths:['src/a.mjs'],checkedAt:new Date().toISOString()}),
      paidGrantReader:async task=>({status:'APPROVED',taskId:task.id,taskKey:taskKey(task),
        authorizationRef:'human:exact-numeric-grant',capMicros:400,maxCalls:1,maxCanaries:1,
        checkedAt:new Date().toISOString(),allowedProviders:[provider]}),
      capabilityReader:async name=>({provider:name,status:'PENDING',secretIsolation:false,
        usageAccounting:false,idempotency:false,sandbox:false})
    });
    const name=live.task.id;
    await c.initialize(live);
    await assert.rejects(c.invoke({taskKey:taskKey(live.task),role:'Executor',provider,maxReserveMicros:150}),/capability acceptance pending/);
    assert.equal((await store.read(taskKey(live.task))).ledger.calls,0);
  }
});

test('known usage above reserved ceiling blocks without allowing replay',async t=>{
  const {controller:c,store}=await setup(t,{invoke:x=>({
    version:'ddf.result/v1',taskId,role:x.role,invocationId:x.invocationId,status:'completed',usageMicros:300
  })});
  await c.initialize(spec());
  const call=await execute(c);
  assert.equal(call.phase,'BLOCKED_UNCERTAIN');
  assert.equal((await store.read(scopedKey)).ledger.chargedMicros,300);
  await assert.rejects(execute(c),/blocked/);
});

test('ModelProvider is a distinct executable protocol with no inherited publisher authority',async()=>{
  const {ModelProvider}=await import('../src/adapters.mjs');
  const model=new ModelProvider({id:'example',roles:['Executor'],accepted:false});
  await assert.rejects(model.invoke({}),/must be implemented/);
  assert.equal('publisher' in model,false);
  assert.throws(()=>new ModelProvider({id:'bad',roles:['Publisher']}),/provider roles/);
});

test('fresh DDF admission revision drift prevents invocation and resume',async t=>{
  let revision='d'.repeat(64);
  const {controller:c,store}=await setup(t,{admissionReader:task=>({...admission(task),revision})});
  await c.initialize(spec());
  revision='e'.repeat(64);
  await assert.rejects(execute(c),/admission revision drift/);
  await assert.rejects(c.initialize(spec()),/admission revision drift/);
  assert.equal((await store.read(scopedKey)).ledger.calls,0);
});

test('a JobSpec approvalRef never substitutes a trusted human grant',async t=>{
  const live=spec({mode:'live',budget:{capMicros:400,maxCalls:1,maxCanaries:1,
    paidAuthorization:true,authorizationRef:'human:exact-numeric-grant'}});
  const {controller:c}=await setup(t,{paidGrantReader:async()=>({status:'APPROVED'})});
  await assert.rejects(c.initialize(live),/trusted human task approval reader required/);
});

test('stale trusted task authority blocks live initialization',async t=>{
  const live=spec({mode:'live',budget:{capMicros:400,maxCalls:1,maxCanaries:1,
    paidAuthorization:true,authorizationRef:'human:exact-numeric-grant'}});
  const {controller:c}=await setup(t,{approvalReader:async task=>({
    status:'APPROVED',taskId:task.id,taskKey:taskKey(task),approvalRef:'human:approved',policySha,baseSha,
    publication:'allowed',humanMerge:true,maxCorrections:2,maxFallbacks:1,
    allowedPaths:['src/a.mjs'],checkedAt:'2020-01-01T00:00:00Z'
  })});
  await assert.rejects(c.initialize(live),/DDF task-policy grant missing or stale/);
});

test('no user-specific repository or personal role schema is accepted as core policy',()=>{
  const s=spec();
  assert.equal(validateJobSpec(s).task.source.kind,'manual');
  assert.equal(validateJobSpec(spec({task:{...s.task,id:'portable-789',projectId:'unknown-vcs',
    source:{kind:'forgejo',ref:'my-host/path/issue/789'},branch:'feature/789'}})).task.id,'portable-789');
  assert.throws(()=>validateJobSpec(spec({policy:{...s.policy,contractVersion:2}})),/DDF-native policy/);
  assert.throws(()=>validateJobSpec(spec({task:{...s.task,branch:s.task.integrationBranch}})),/unsafe or conflicting/);
});

test('admission is bound to project identity, not forge Issue labels or statuses',async t=>{
  const {controller:c}=await setup(t,{admissionReader:task=>({...admission(task),projectId:'other-project'})});
  await assert.rejects(c.initialize(spec()),/admission task\/project/);
});

test('standalone core never imports maintainer-specific repository contracts', async()=>{
  const {readFile,readdir}=await import('node:fs/promises');
  const {join}=await import('node:path');
  const paths=(await readdir(join(process.cwd(),'src'))).filter(name=>name.endsWith('.mjs'));
  for(const name of paths){
    const source=await readFile(join(process.cwd(),'src',name),'utf8');
    assert.doesNotMatch(source,/ChipIn-one|syllik\/ai-workflow|codex-local-runner|chipin-issue-admission|Project #5|org\/repo#issue/,
      'vendor-specific runtime dependency found: '+name);
  }
});


test('AUDIT: reviewer cannot bypass exhausted single-call ledger',async t=>{
  let reviewerCalls=0;
  const {controller:c,store}=await setup(t,{reviewer:{request:async()=>{reviewerCalls++;return {status:'pass'};}}});
  await c.initialize(spec({budget:{capMicros:250,maxCalls:1}}));
  await execute(c);await c.verify(scopedKey);await c.publish(scopedKey);
  await assert.rejects(c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100}),/paid-role|cap exceeded/);
  assert.equal(reviewerCalls,0);
  assert.equal((await store.read(scopedKey)).ledger.calls,1);
  assert.equal((await store.read(scopedKey)).reviewBySha[headSha],undefined);
});

test('AUDIT: identical local ID across project/source isolates store, ledger and callback',async t=>{
  const {controller:c,store}=await setup(t),a=spec();
  const b=spec({task:{...a.task,projectId:'other-project',source:{kind:'gitlab',ref:'group/ticket-8'}}});
  const bKey=taskKey(b.task);
  assert.notEqual(bKey,scopedKey);
  await c.initialize(a);await c.initialize(b);await execute(c);
  assert.equal((await store.read(scopedKey)).ledger.calls,1);
  assert.equal((await store.read(bKey)).ledger.calls,0);
  const first=(await store.read(scopedKey)).invocations[0];
  await assert.rejects(c.receiveResult(bKey,first.invocationId,{
    version:'ddf.result/v1',taskKey:scopedKey,taskId,role:'Executor',
    principalId:'executor-1',invocationId:first.invocationId,status:'completed',usageMicros:null
  }),/no pending invocation/);
});

test('AUDIT: different reviewer principal can share same model provider',async t=>{
  const {controller:c,store}=await setup(t,{reviewer:{principalId:'reviewer-session',request:async()=>({status:'pass'})}});
  await published(c);
  await c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100});
  const s=await store.read(scopedKey);
  assert.equal(s.invocations[0].provider,s.invocations[1].provider);
  assert.notEqual(s.invocations[0].principalId,s.invocations[1].principalId);
});

test('AUDIT: same executor/reviewer principal rejected independently of provider',async t=>{
  let called=false;
  const {controller:c,store}=await setup(t,{reviewer:{principalId:'executor-1',request:async()=>{called=true;return {status:'pass'};}}});
  await published(c);
  await assert.rejects(c.requestReview(scopedKey,{provider:'other-vendor',maxReserveMicros:100}),/review principal/);
  assert.equal(called,false);assert.equal((await store.read(scopedKey)).ledger.calls,1);
});

test('AUDIT: lost review response blocks automatic replay; reservation retained',async t=>{
  let calls=0;
  const {controller:c,store}=await setup(t,{reviewer:{request:async()=>{calls++;throw new Error('lost response');}}});
  await published(c);
  await assert.rejects(c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100}),/uncertain/);
  const s=await store.read(scopedKey);
  assert.equal(s.ledger.calls,2);assert.equal(s.ledger.reservedMicros,100);
  assert.equal(s.reviewBySha[headSha].status,'REQUESTED');
  assert.equal((await c.recover(scopedKey)).phase,'BLOCKED_UNCERTAIN');
  await assert.rejects(c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100}),/published change required/);
  assert.equal(calls,1);
});

test('AUDIT: pending review callback settles once, conflicting callback rejected',async t=>{
  const {controller:c,store}=await setup(t,{reviewer:{request:async()=>({status:'pending',reference:'async-review'})}});
  await published(c);
  const pending=await c.requestReview(scopedKey,{provider:'mock',maxReserveMicros:100});
  assert.equal((await store.read(scopedKey)).ledger.reservedMicros,100);
  const final={...pending,status:'pass',usageMicros:null};
  await c.receiveReview(scopedKey,final);
  assert.equal((await store.read(scopedKey)).ledger.chargedMicros,350);
  assert.equal((await c.receiveReview(scopedKey,final)).kind,'already-reviewed');
  await assert.rejects(c.receiveReview(scopedKey,{...final,usageMicros:0}),/conflicting duplicate/);
});

test('AUDIT: GitHub PR number is adapter metadata, not publication contract',async t=>{
  const {controller:c,store}=await setup(t);
  await published(c);
  const s=await store.read(scopedKey);
  assert.equal(s.publication.receipt.changeRef.id,'offline-pr-8');
  assert.equal(s.publication.receipt.adapter.github.number,8);
});

test('AUDIT: live review provider must pass trusted paid grant and own acceptance',async t=>{
  const live=spec({mode:'live',budget:{capMicros:400,maxCalls:2,maxCanaries:1,paidAuthorization:true,authorizationRef:'grant'}});
  const {controller:c}=await setup(t,{
    approvalReader:async task=>({status:'APPROVED',taskId:task.id,taskKey:taskKey(task),
      approvalRef:'human:approved',policySha,baseSha,publication:'allowed',humanMerge:true,
      maxCorrections:2,maxFallbacks:1,allowedPaths:['src/a.mjs'],checkedAt:new Date().toISOString()}),
    paidGrantReader:async task=>({status:'APPROVED',taskId:task.id,taskKey:taskKey(task),
      authorizationRef:'grant',capMicros:400,maxCalls:2,maxCanaries:1,
      allowedProviders:['executor-model'],checkedAt:new Date().toISOString()}),
    capabilityReader:async provider=>({provider,status:'PENDING',secretIsolation:false,
      usageAccounting:false,idempotency:false,sandbox:false})
  });
  await c.initialize(live);
  const s=await c.store.read(scopedKey);
  await assert.rejects(c._reservePaid(s,{role:'Reviewer',kind:'review',provider:'review-model',maxReserveMicros:100}),/provider not allowed/);
  assert.equal(s.ledger.calls,0);
});
