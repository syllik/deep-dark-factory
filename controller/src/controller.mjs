import { assert, assertFreshAdmission, clone, digest, validateJobSpec, validateResult } from './contracts.mjs';

const validMoney = n => Number.isSafeInteger(n) && n > 0;
const SHA = /^[0-9a-f]{40}$/;

/** Core orchestration only: accepts trusted adapter ports, never holds publication credentials. */
export class Controller {
  constructor({store,admissionReader,paidGrantReader,capabilityReader,backend,verifier,publisher,ciReader,reviewer}) {
    Object.assign(this,{store,admissionReader,paidGrantReader,capabilityReader,backend,verifier,publisher,ciReader,reviewer});
  }
  async gate(spec, provider=null) {
    assert(typeof this.admissionReader === 'function','trusted fresh admission reader required');
    const revision = assertFreshAdmission(await this.admissionReader(spec.task),spec);
    if (spec.mode === 'live') {
      assert(typeof this.paidGrantReader === 'function','trusted external paid-grant reader required');
      const grant = await this.paidGrantReader(spec.task);
      assert(grant?.status === 'APPROVED' && grant.taskId === spec.task.id &&
        grant.authorizationRef === spec.budget.authorizationRef &&
        grant.capMicros === spec.budget.capMicros && grant.maxCalls === spec.budget.maxCalls &&
        grant.maxCanaries === 1, 'paid grant task/ceiling mismatch');
      const at = Date.parse(grant.checkedAt);
      assert(Number.isFinite(at) && at <= Date.now() && Date.now()-at <= 120000,'stale paid grant');
      if (provider) assert(grant.allowedProviders?.includes(provider),'provider not allowed by paid grant');
    }
    return revision;
  }
  async initialize(input) {
    const spec = validateJobSpec(input);
    const admissionRevision = await this.gate(spec);
    return this.store.withTask(spec.task.id,async (get,save) => {
      const current = get();
      if (current) {
        assert(current.specDigest === digest(spec),'immutable JobSpec drift');
        assert(current.admissionRevision === admissionRevision,'admission revision drift on resume');
        return current;
      }
      const state = {version:'ddf.state/v1',taskId:spec.task.id,spec,specDigest:digest(spec),admissionRevision,
        phase:'ADMITTED',ledger:{reservedMicros:0,chargedMicros:0,calls:0,unknownCalls:0},
        invocations:[],corrections:0,reviewBySha:{},publication:null,publicationHistory:[],
        verified:null,ci:null,ready:false,readyIntent:null};
      await save(state); return state;
    });
  }
  async invoke({taskId,role='Executor',provider,maxReserveMicros,kind='implementation',authorization=null,payload={}}) {
    return this.store.withTask(taskId,async(get,save) => {
      const state = get();
      assert(state && !['BLOCKED_UNCERTAIN','READY_PR'].includes(state.phase),'task absent or blocked');
      assert((await this.gate(state.spec,provider))===state.admissionRevision,'admission revision drift');
      assert(['implementation','fallback','correction'].includes(kind),'unsupported invocation kind');
      if (kind === 'implementation') {
        assert(role === 'Executor' && state.phase === 'ADMITTED' && !state.invocations.length,'one initial Executor only');
      } else if (kind === 'fallback') {
        assert(role === 'Executor' && state.spec.policy.maxFallbacks === 1 &&
          !state.invocations.some(i=>i.kind==='fallback') &&
          state.invocations.some(i=>i.kind==='implementation'&&i.status==='failed') &&
          state.invocations.every(i=>i.status!=='unknown'), 'fallback requires confirmed failed implementation and explicit allowance');
      } else {
        assert(role==='Executor' && state.phase==='REVIEW_FINDINGS','correction needs current findings');
        assert(state.corrections < Math.min(2,state.spec.policy.maxCorrections),'correction cap reached');
        const publishedSha = state.publication?.receipt?.headSha;
        assert(authorization?.headSha === publishedSha && !!authorization.reference &&
          authorization.findingsPackageCount === 1 &&
          state.reviewBySha[publishedSha]?.status === 'findings','current-head authorized consolidated correction required');
      }
      assert(state.invocations.every(i=>!['RESERVED','PENDING'].includes(i.status)),'uncertain invocation requires recovery');
      assert(validMoney(maxReserveMicros),'nonzero pre-invocation reservation required');
      assert(state.ledger.calls < state.spec.budget.maxCalls &&
        state.ledger.chargedMicros+state.ledger.reservedMicros+maxReserveMicros <= state.spec.budget.capMicros,
        'single task+fallback+corrections cap exceeded');
      if (state.spec.mode === 'live') {
        assert(typeof this.capabilityReader === 'function','per-provider capability acceptance required');
        const accepted = await this.capabilityReader(provider);
        assert(accepted?.provider === provider && accepted.status === 'ACCEPTED' &&
          accepted.secretIsolation === true && accepted.usageAccounting === true &&
          accepted.idempotency === true && accepted.sandbox === true,'provider capability acceptance pending');
      } else assert(this.backend?.offlineSafe === true,'offline mode cannot invoke real backend');
      const invocationId = digest({taskId,call:state.ledger.calls+1,provider,kind});
      state.invocations.push({invocationId,provider,role,kind,maximumMicros:maxReserveMicros,status:'RESERVED',usageMicros:null});
      state.ledger.calls++;state.ledger.reservedMicros+=maxReserveMicros;
      if (kind === 'correction') {
        state.corrections++;
        if (state.publication) state.publicationHistory.push(state.publication);
        state.publication=null;state.verified=null;state.ci=null;state.readyIntent=null;
      }
      state.phase='INVOCATION_PENDING';
      await save(state); // atomic persisted reservation before dispatch/model invocation
      let response;
      try { response = await this.backend.run({invocationId,jobSpecDigest:state.specDigest,role,provider,payload,mode:state.spec.mode}); }
      catch (err) {
        state.phase='BLOCKED_UNCERTAIN';await save(state);
        throw new Error('invocation outcome uncertain; reservation retained: '+err.message);
      }
      if (response?.kind === 'pending') {
        state.invocations.at(-1).status='PENDING';
        state.invocations.at(-1).reference=response.reference;
        await save(state);
        return {kind:'pending',invocationId,reference:response.reference};
      }
      assert(response?.kind === 'result','unverified backend outcome: reserved invocation retained');
      return this._settle(state,save,response.result,invocationId);
    });
  }
  async _settle(state,save,input,invocationId) {
    const i = state.invocations.find(x=>x.invocationId===invocationId);
    assert(i && ['PENDING','RESERVED'].includes(i.status),'invocation absent or settled');
    const result = validateResult(input,{taskId:state.taskId,invocationId,role:i.role});
    const usage = result.usageMicros === null ? i.maximumMicros : result.usageMicros;
    state.ledger.reservedMicros -= i.maximumMicros;
    state.ledger.chargedMicros += Math.max(i.maximumMicros,usage);
    if (result.usageMicros === null) state.ledger.unknownCalls++;
    i.status=result.status;i.usageMicros=result.usageMicros;
    i.debitedMicros=Math.max(i.maximumMicros,usage);i.resultDigest=digest(result);
    state.phase=usage > i.maximumMicros || result.status==='unknown' ? 'BLOCKED_UNCERTAIN' : result.status==='completed' ? 'EXECUTED' : 'BLOCKED';
    await save(state);
    return {kind:'settled',result,chargedMicros:i.debitedMicros,phase:state.phase};
  }
  async receiveResult(taskId,invocationId,result) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get(), i=s?.invocations.find(x=>x.invocationId===invocationId);
      if(i && ['completed','failed','unknown'].includes(i.status)) {
        assert(i.resultDigest===digest(result),'conflicting duplicate callback');
        return {kind:'already-settled',phase:s.phase};
      }
      assert(i?.status==='PENDING','no pending invocation');
      return this._settle(s,save,result,invocationId);
    });
  }
  async recover(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s,'task absent');
      if(s.invocations.some(i=>['RESERVED','PENDING'].includes(i.status)) ||
         ['PUBLISH_PENDING','READY_PENDING','REVIEW_PENDING'].includes(s.phase) ||
         Object.values(s.reviewBySha).some(r=>r.status==='REQUESTED')) {
        s.phase='BLOCKED_UNCERTAIN'; await save(s);
      }
      return s;
    });
  }
  async verify(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s?.phase==='EXECUTED','completed execution required');
      assert((await this.gate(s.spec))===s.admissionRevision,'admission revision drift');
      assert(typeof this.verifier==='function','trusted deterministic verifier required');
      const receipt=await this.verifier(s.spec);
      assert(receipt?.status==='passed' && SHA.test(receipt.baseSha||'') &&
        receipt.baseSha===s.spec.task.baseSha && /^[0-9a-f]{64}$/.test(receipt.diffDigest||'') &&
        Array.isArray(receipt.changedPaths) && receipt.changedPaths.length>0,'verified actual diff/base required');
      assert(receipt.changedPaths.every(p=>s.spec.policy.allowedPaths.includes(p)),'actual diff exceeds approved paths');
      s.verified=clone(receipt);s.ci=null;s.phase='VERIFIED';await save(s);return receipt;
    });
  }
  validPublication(receipt,s,key) {
    return receipt?.key===key && SHA.test(receipt.headSha||'') && receipt.verifiedDigest===digest(s.verified) &&
      receipt.branch===s.spec.task.branch && receipt.base===s.spec.task.integrationBranch &&
      Number.isInteger(receipt.pr) && receipt.pr>0 && receipt.commitCount===1 && receipt.pushCount===1 &&
      receipt.forcePush===false && receipt.historyRewrite===false && receipt.humanMerge===true;
  }
  async publish(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s?.phase==='VERIFIED' && !s.publication,'verified un-published revision required');
      assert((await this.gate(s.spec))===s.admissionRevision,'admission revision drift');
      assert(typeof this.publisher?.publish==='function','trusted publisher required');
      if(s.spec.mode==='offline') assert(this.publisher.mock===true,'offline forbids real publication');
      const key=digest({taskId,verifiedDigest:digest(s.verified),branch:s.spec.task.branch});
      s.phase='PUBLISH_PENDING';s.publication={key,status:'PENDING'};await save(s);
      let receipt;
      try {receipt=await this.publisher.publish({spec:s.spec,verified:s.verified,key});}
      catch {s.phase='BLOCKED_UNCERTAIN';await save(s);throw new Error('publisher outcome uncertain; read-only reconcile by key');}
      assert(this.validPublication(receipt,s,key),'trusted publisher receipt invalid');
      s.publication={key,status:'PUBLISHED',receipt};s.phase='PUBLISHED';await save(s);return receipt;
    });
  }
  async reconcilePublication(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s?.publication?.status==='PENDING','no pending publisher intent');
      assert(typeof this.publisher?.lookup==='function','read-only publisher lookup required');
      const receipt=await this.publisher.lookup(s.publication.key);
      assert(this.validPublication(receipt,s,s.publication.key),'outcome unknown; manual reconciliation needed');
      s.publication={key:receipt.key,status:'PUBLISHED',receipt};s.phase='PUBLISHED';await save(s);return receipt;
    });
  }
  async requestReview(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s?.phase==='PUBLISHED','published PR required for review');
      assert((await this.gate(s.spec))===s.admissionRevision,'admission revision drift');
      const sha=s.publication.receipt.headSha;
      assert(!s.reviewBySha[sha],'review already requested for exact SHA');
      const ci=await this.ciReader(s.publication.receipt);
      assert(ci?.headSha===sha && ci.status==='green' && ci.requiredChecksPassed===true,'required CI not green on current published SHA');
      s.ci=ci;s.reviewBySha[sha]={status:'REQUESTED'};await save(s);
      let review;
      try {review=await this.reviewer.request({taskId,sha,pr:s.publication.receipt.pr,readOnly:true});}
      catch {s.phase='BLOCKED_UNCERTAIN';await save(s);throw new Error('review request outcome uncertain; SHA reserved');}
      assert(review?.sha===sha && ['pass','findings','pending'].includes(review.status) &&
        review.readOnly===true && !!review.reviewerId &&
        !s.invocations.some(i=>i.role==='Executor'&&i.provider===review.reviewerId),'independent read-only review required');
      s.reviewBySha[sha]={status:review.status,reviewerId:review.reviewerId,reference:review.reference||null};
      s.phase=review.status==='pass'?'REVIEWED':review.status==='findings'?'REVIEW_FINDINGS':'REVIEW_PENDING';
      await save(s);return review;
    });
  }
  async receiveReview(taskId,review) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get(), prior=s?.reviewBySha[review?.sha];
      assert(prior && ['REQUESTED','pending'].includes(prior.status),'no requested SHA review');
      assert(review.reviewerId===prior.reviewerId || prior.reviewerId==null,'reviewer identity changed');
      assert(review.readOnly===true && ['pass','findings'].includes(review.status) &&
        !s.invocations.some(i=>i.role==='Executor'&&i.provider===review.reviewerId),'review independence failed');
      s.reviewBySha[review.sha]={status:review.status,reviewerId:review.reviewerId,reference:review.reference||null};
      s.phase=review.status==='pass'?'REVIEWED':'REVIEW_FINDINGS';await save(s);return review;
    });
  }
  async markReady(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s?.phase==='REVIEWED','independent review pass required');
      assert((await this.gate(s.spec))===s.admissionRevision,'admission revision drift');
      const head=s.publication.receipt.headSha;
      assert(s.reviewBySha[head]?.status==='pass' && s.ci?.headSha===head,'review/CI SHA mismatch');
      const ci=await this.ciReader(s.publication.receipt);
      assert(ci?.status==='green' && ci.headSha===head && ci.requiredChecksPassed===true,'required CI drift');
      assert(typeof this.publisher?.markReady==='function','trusted mark-ready required');
      if(s.spec.mode==='offline') assert(this.publisher.mock===true,'offline forbids real readiness mutation');
      s.readyIntent={key:s.publication.key,headSha:head,pr:s.publication.receipt.pr};
      s.phase='READY_PENDING';await save(s);
      let receipt;
      try {receipt=await this.publisher.markReady(s.readyIntent);}
      catch {s.phase='BLOCKED_UNCERTAIN';await save(s);throw new Error('ready outcome uncertain; read-only reconcile');}
      assert(receipt?.headSha===head && receipt.draft===false && receipt.humanMerge===true,'ready PR read-back invalid');
      s.ready=true;s.phase='READY_PR';await save(s);return receipt;
    });
  }
  async reconcileReady(taskId) {
    return this.store.withTask(taskId,async(get,save)=>{
      const s=get();assert(s?.readyIntent && !s.ready,'no pending ready intent');
      assert(typeof this.publisher?.lookupReady==='function','read-only PR lookup required');
      const receipt=await this.publisher.lookupReady(s.readyIntent);
      assert(receipt?.headSha===s.readyIntent.headSha && receipt.draft===false && receipt.humanMerge===true,'ready state unconfirmed');
      s.ready=true;s.phase='READY_PR';await save(s);return receipt;
    });
  }
}
