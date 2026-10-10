import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Controller } from '../src/controller.mjs';
import { AgentRuntime, OfflineBackend, sanitizeExecutorEnv, ActionsBackend, DedicatedMachineBackend, LocalEndpointBackend, capabilityAcceptance } from '../src/adapters.mjs';
import { FileStateStore } from '../src/store.mjs';
import { validateJobSpec, validateResult, digest } from '../src/contracts.mjs';

const taskId='syllik/deep-dark-factory#8';
const baseSha='b'.repeat(40),headSha='c'.repeat(40),policySha='a'.repeat(40);
function spec(overrides={}) {
  return {
    version:'ddf.jobspec/v1',mode:'offline',contextBytes:1800,
    task:{id:taskId,repository:'syllik/deep-dark-factory',issueNumber:8,branch:'slave/issue-8-controller-v0',integrationBranch:'master',baseSha},
    policy:{contractVersion:2,approvalRef:'human:approved',sha:policySha,allowedPaths:['src/a.mjs'],publication:'allowed',humanMerge:true,maxCorrections:2,maxFallbacks:1},
    budget:{capMicros:1000,maxCalls:3},
    ...overrides
  };
}
const admission=(task)=>({contractVersion:'task-admission/v1',status:'ADMITTED',issue:task.id,revision:'d'.repeat(64),checkedAt:new Date().toISOString(),blockers:[]});
function rig({admissionReader=admission,invoke,backend,ciReader,reviewer,publisher,paidGrantReader,capabilityReader,approvalReader,correctionGrantReader}={}) {
  const models = new AgentRuntime({mock:{accepted:true,roles:['Executor'],offlineOnly:true,invoke:invoke||((x)=>({
    version:'ddf.result/v1',taskId,invocationId:x.invocationId,role:x.role,status:'completed',usageMicros:null
  }))}});
  const results = new Map();
  const trustedPublisher = publisher || {
    mock:true,
    async publish({spec,verified,key}) {
      const receipt={key,verifiedDigest:digest(verified),headSha,branch:spec.task.branch,base:spec.task.integrationBranch,pr:8,
        commitCount:1,pushCount:1,forcePush:false,historyRewrite:false,humanMerge:true};
      results.set(key,receipt);
      return receipt;
    },
    async lookup(key) {return results.get(key);},
    async markReady(intent) {return {headSha:intent.headSha,draft:false,humanMerge:true};},
    async lookupReady(intent) {return {headSha:intent.headSha,draft:false,humanMerge:true};}
  };
  return {
    store:null,
    options:{admissionReader,approvalReader,correctionGrantReader,paidGrantReader,capabilityReader,backend:backend||new OfflineBackend(models),
      verifier:async s=>({status:'passed',baseSha:s.task.baseSha,changedPaths:['src/a.mjs'],diffDigest:'e'.repeat(64)}),
      publisher:trustedPublisher,
      ciReader:ciReader|| (async pub=>({status:'green',headSha:pub.headSha,requiredChecksPassed:true})),
      reviewer:reviewer||{request:async ({sha})=>({sha,status:'pass',readOnly:true,reviewerId:'independent-reviewer',reference:'offline-review'})}}
  };
}
async function setup(t,options={}) {
  const dir=await mkdtemp(path.join(os.tmpdir(),'ddf-controller-'));
  t.after(()=>rm(dir,{recursive:true,force:true}));
  const store=new FileStateStore(dir), f=rig(options);
  return {store,controller:new Controller({store,...f.options}),...f.options};
}
async function execute(c,options={}) {return c.invoke({taskId,kind:'implementation',provider:'mock',maxReserveMicros:250,...options});}
async function published(c) {
  await c.initialize(spec());
  await execute(c);
  await c.verify(taskId);
  return c.publish(taskId);
}

test('offline only: exact mock lifecycle stops at ready PR, never merges',async t=>{
  const {controller:c,store}=await setup(t);
  const pub=await published(c);
  assert.equal(pub.headSha,headSha);
  await c.requestReview(taskId);
  await c.markReady(taskId);
  const state=await store.read(taskId);
  assert.equal(state.phase,'READY_PR');
  assert.equal(state.ledger.chargedMicros,250);
  assert.equal(state.ledger.unknownCalls,1);
  assert.equal(state.publication.receipt.headSha,headSha);
  assert.equal(state.spec.mode,'offline');
});

test('rejects malformed or over-context JobSpec and absent paid grant',async()=>{
  assert.throws(()=>validateJobSpec(spec({contextBytes:32769})),/context/);
  assert.throws(()=>validateJobSpec(spec({budget:{capMicros:-1,maxCalls:1}})),/numeric/);
  assert.throws(()=>validateJobSpec(spec({policy:{...spec().policy,maxCorrections:3}})),/corrections/);
  const live=spec({mode:'live',budget:{capMicros:1000,maxCalls:1,paidAuthorization:true,authorizationRef:'grant',maxCanaries:1}});
  assert.equal(validateJobSpec(live).mode,'live');
});

test('admission queued, stale and mismatched exact issue all fail closed',async t=>{
  for(const bad of [
    {status:'QUEUED'},
    {checkedAt:'2020-01-01T00:00:00Z'},
    {issue:'other/repo#1'},
    {blockers:['Missing Issue Type']}
  ]) {
    const {controller:c}=await setup(t,{admissionReader:task=>({...admission(task),...bad})});
    await assert.rejects(c.initialize(spec()),/admission|stale|mismatch|blockers/);
  }
});

test('FE requires native org #53 owner and Project #5, never label-only',async t=>{
  const s=spec({task:{...spec().task,id:'ChipIn-one/chipin-frontend#100',repository:'ChipIn-one/chipin-frontend',issueNumber:100}});
  const reader=task=>({...admission(task),contractVersion:'chipin-issue-admission/v1',status:'INTAKE_COMPLETE',
    project:{number:5,membershipCount:1,status:'Todo'},assignees:['somebody']});
  const {controller:c}=await setup(t,{admissionReader:reader});
  await assert.rejects(c.initialize(s),/native assignee/);
});

test('unknown usage debits full reserved cost and enforces shared cap',async t=>{
  const {controller:c,store}=await setup(t);
  await c.initialize(spec({budget:{capMicros:300,maxCalls:3}}));
  await execute(c);
  const state=await store.read(taskId);
  assert.equal(state.ledger.chargedMicros,250);
  assert.equal(state.ledger.unknownCalls,1);
  await assert.rejects(c.invoke({taskId,role:'Executor',provider:'mock',kind:'fallback',maxReserveMicros:100}),/fallback|cap/);
});

test('reserve persisted before backend invocation and crash blocks auto-replay',async t=>{
  let before;
  const dir=await mkdtemp(path.join(os.tmpdir(),'ddf-crash-'));
  t.after(()=>rm(dir,{recursive:true,force:true}));
  const store=new FileStateStore(dir);
  const f=rig({backend:{offlineSafe:true,run:async()=>{
    before=await store.read(taskId);
    throw new Error('ambiguous dispatch');
  }}});
  const c=new Controller({store,...f.options});
  await c.initialize(spec());
  await assert.rejects(execute(c),/uncertain/);
  assert.equal(before.invocations[0].status,'RESERVED');
  assert.equal(before.ledger.reservedMicros,250);
  assert.equal((await store.read(taskId)).phase,'BLOCKED_UNCERTAIN');
  await assert.rejects(execute(c),/blocked/);
});

test('atomic store lock rejects concurrent tasks and requires operator recovery',async t=>{
  const {store}=await setup(t);
  let release;
  const hold=new Promise(resolve=>{release=resolve;});
  let entered;
  const enteredP=new Promise(resolve=>{entered=resolve;});
  const first=store.withTask(taskId,async()=>{entered();await hold;return true;});
  await enteredP;
  await assert.rejects(store.withTask(taskId,async()=>true),/locked/);
  await assert.rejects(store.recoverOrphanedLock(taskId),/manual/);
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
  assert.equal((await c.invoke({taskId,role:'Executor',provider:'mock',kind:'fallback',maxReserveMicros:250})).phase,'EXECUTED');
  assert.equal((await store.read(taskId)).ledger.chargedMicros,500);
  await assert.rejects(c.invoke({taskId,role:'Executor',provider:'mock',kind:'fallback',maxReserveMicros:250}),/fallback/);
});

test('pending callback is settled once; conflicting repeats rejected',async t=>{
  const {controller:c}=await setup(t,{backend:{offlineSafe:true,run:async()=>({kind:'pending',reference:'job-1'})}});
  await c.initialize(spec());
  const first=await execute(c);
  const result={version:'ddf.result/v1',taskId,role:'Executor',invocationId:first.invocationId,status:'completed',usageMicros:null};
  assert.equal((await c.receiveResult(taskId,first.invocationId,result)).phase,'EXECUTED');
  assert.equal((await c.receiveResult(taskId,first.invocationId,result)).kind,'already-settled');
  await assert.rejects(c.receiveResult(taskId,first.invocationId,{...result,usageMicros:0}),/conflicting/);
});

test('publication holds durable intent, only lookup can resolve ambiguous push',async t=>{
  let receipt;
  const mock={
    mock:true,
    publish:async ({key,verified,spec})=>{
      receipt={key,verifiedDigest:digest(verified),headSha,branch:spec.task.branch,base:spec.task.integrationBranch,
        pr:10,commitCount:1,pushCount:1,forcePush:false,historyRewrite:false,humanMerge:true};
      throw new Error('lost response');
    },
    lookup:async()=>receipt
  };
  const {controller:c,store}=await setup(t,{publisher:mock});
  await c.initialize(spec());await execute(c);await c.verify(taskId);
  await assert.rejects(c.publish(taskId),/uncertain/);
  assert.equal((await store.read(taskId)).publication.status,'PENDING');
  const recovered=await c.reconcilePublication(taskId);
  assert.equal(recovered.pr,10);
  await assert.rejects(c.publish(taskId),/un-published/);
});

test('review deduplicated on actually published SHA; CI must be green',async t=>{
  const {controller:c}=await setup(t);
  await published(c);
  await c.requestReview(taskId);
  await assert.rejects(c.requestReview(taskId),/published PR required|already requested/);
  const {controller:other}=await setup(t,{ciReader:async()=>({status:'green',headSha:'f'.repeat(40),requiredChecksPassed:true})});
  await published(other);
  await assert.rejects(other.requestReview(taskId),/current published SHA/);
});

test('third correction denied, even if task attempts repeated findings',async t=>{
  const {controller:c,store}=await setup(t,{reviewer:{request:async({sha})=>({sha,status:'findings',readOnly:true,reviewerId:'independent-reviewer'})}});
  await published(c);
  await c.requestReview(taskId);
  await store.withTask(taskId,async(get,save)=>{
    const s=get();s.corrections=2;await save(s);
  });
  await assert.rejects(c.invoke({taskId,role:'Executor',provider:'mock',kind:'correction',maxReserveMicros:250,
    authorization:{headSha,reference:'human:correction',findingsPackageCount:1}}),/correction cap/);
});

test('unapproved correction findings package fails closed',async t=>{
  const {controller:c}=await setup(t,{reviewer:{request:async({sha})=>({sha,status:'findings',readOnly:true,reviewerId:'independent-reviewer'})}});
  await published(c);await c.requestReview(taskId);
  await assert.rejects(c.invoke({taskId,role:'Executor',provider:'mock',kind:'correction',maxReserveMicros:250,
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
  assert.throws(()=>validateResult({version:'ddf.result/v1',taskId,role:'Executor',invocationId:'a',status:'completed'},
    {taskId,role:'Executor',invocationId:'a'}),/usage/);
});

test('Codex, Claude and local endpoint require three independent capability acceptances',async t=>{
  const live=spec({mode:'live',budget:{capMicros:400,maxCalls:1,maxCanaries:1,
    paidAuthorization:true,authorizationRef:'human:exact-numeric-grant'}});
  for(const provider of ['codex','claude','local']) {
    const {controller:c,store}=await setup(t,{
      approvalReader:async task=>({status:'APPROVED',taskId:task.id,
        approvalRef:'human:approved',policySha,baseSha,publication:'allowed',humanMerge:true,
        maxCorrections:2,maxFallbacks:1,allowedPaths:['src/a.mjs'],checkedAt:new Date().toISOString()}),
      paidGrantReader:async task=>({status:'APPROVED',taskId:task.id,
        authorizationRef:'human:exact-numeric-grant',capMicros:400,maxCalls:1,maxCanaries:1,
        checkedAt:new Date().toISOString(),allowedProviders:[provider]}),
      capabilityReader:async name=>({provider:name,status:'PENDING',secretIsolation:false,
        usageAccounting:false,idempotency:false,sandbox:false})
    });
    const name=live.task.id;
    await c.initialize(live);
    await assert.rejects(c.invoke({taskId:name,role:'Executor',provider,maxReserveMicros:150}),/capability acceptance pending/);
    assert.equal((await store.read(name)).ledger.calls,0);
  }
});

test('known usage above reserved ceiling blocks without allowing replay',async t=>{
  const {controller:c,store}=await setup(t,{invoke:x=>({
    version:'ddf.result/v1',taskId,role:x.role,invocationId:x.invocationId,status:'completed',usageMicros:300
  })});
  await c.initialize(spec());
  const call=await execute(c);
  assert.equal(call.phase,'BLOCKED_UNCERTAIN');
  assert.equal((await store.read(taskId)).ledger.chargedMicros,300);
  await assert.rejects(execute(c),/blocked/);
});

test('ModelProvider is a distinct executable protocol with no inherited publisher authority',async()=>{
  const {ModelProvider}=await import('../src/adapters.mjs');
  const model=new ModelProvider({id:'example',roles:['Executor'],accepted:false});
  await assert.rejects(model.invoke({}),/must be implemented/);
  assert.equal('publisher' in model,false);
  assert.throws(()=>new ModelProvider({id:'bad',roles:['Publisher']}),/provider roles/);
});

test('fresh canonical admission revision drift prevents invocation and resume',async t=>{
  let revision='d'.repeat(64);
  const {controller:c,store}=await setup(t,{admissionReader:task=>({...admission(task),revision})});
  await c.initialize(spec());
  revision='e'.repeat(64);
  await assert.rejects(execute(c),/admission revision drift/);
  await assert.rejects(c.initialize(spec()),/admission revision drift/);
  assert.equal((await store.read(taskId)).ledger.calls,0);
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
    status:'APPROVED',taskId:task.id,approvalRef:'human:approved',policySha,baseSha,
    publication:'allowed',humanMerge:true,maxCorrections:2,maxFallbacks:1,
    allowedPaths:['src/a.mjs'],checkedAt:'2020-01-01T00:00:00Z'
  })});
  await assert.rejects(c.initialize(live),/human authority missing or stale/);
});
