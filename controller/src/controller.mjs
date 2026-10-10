import { assert, assertFreshAdmission, clone, digest, taskKey as keyForTask, validateJobSpec, validateResult, ROLES } from './contracts.mjs';

const validMoney = n => Number.isSafeInteger(n) && n > 0;
const SHA = /^[0-9a-f]{40}$/;

/** Core orchestration only: accepts trusted adapter ports, never holds publication credentials. */
export class Controller {
  constructor({store,admissionReader,approvalReader,correctionGrantReader,paidGrantReader,capabilityReader,backend,verifier,publisher,ciReader,reviewer}) {
    Object.assign(this,{store,admissionReader,approvalReader,correctionGrantReader,paidGrantReader,capabilityReader,backend,verifier,publisher,ciReader,reviewer});
  }
  async gate(spec, provider=null) {
    assert(typeof this.admissionReader === 'function','trusted fresh admission reader required');
    const revision = assertFreshAdmission(await this.admissionReader(spec.task),spec);
    if (spec.mode === 'live') {
      assert(typeof this.approvalReader === 'function','trusted human task approval reader required');
      const authority = await this.approvalReader(spec.task);
      const grantedAt = Date.parse(authority?.checkedAt);
      assert(authority?.status === 'APPROVED' && authority.taskId === spec.task.id &&
        authority.taskKey === keyForTask(spec.task) &&
        authority.approvalRef === spec.policy.approvalRef &&
        authority.policySha === spec.policy.sha && authority.baseSha === spec.task.baseSha &&
        authority.publication === 'allowed' && authority.humanMerge === true &&
        authority.maxCorrections === spec.policy.maxCorrections &&
        authority.maxFallbacks === spec.policy.maxFallbacks &&
        Array.isArray(authority.allowedPaths) &&
        authority.allowedPaths.length === spec.policy.allowedPaths.length &&
        authority.allowedPaths.every(p => spec.policy.allowedPaths.includes(p)) &&
        Number.isFinite(grantedAt) && grantedAt <= Date.now() && Date.now()-grantedAt <= 120000,
        'trusted exact DDF task-policy grant missing or stale');
      assert(typeof this.paidGrantReader === 'function','trusted external paid-grant reader required');
      const grant = await this.paidGrantReader(spec.task);
      assert(grant?.status === 'APPROVED' && grant.taskId === spec.task.id &&
        grant.taskKey === keyForTask(spec.task) &&
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
    const key=keyForTask(spec.task);
    return this.store.withTask(key,async (get,save) => {
      const current = get();
      if (current) {
        assert(current.specDigest === digest(spec),'immutable JobSpec drift');
        assert(current.admissionRevision === admissionRevision,'admission revision drift on resume');
        return current;
      }
      const state = {version:'ddf.state/v1',taskKey:key,taskId:spec.task.id,spec,specDigest:digest(spec),admissionRevision,
        phase:'ADMITTED',ledger:{reservedMicros:0,chargedMicros:0,calls:0,unknownCalls:0},
        invocations:[],corrections:0,reviewBySha:{},publication:null,publicationHistory:[],
        verified:null,ci:null,ready:false,readyIntent:null};
      await save(state); return state;
    });
  }
  // Shared paid-invocation accounting for all roles, including independent review.
  // Callers persist the reserved state before dispatch; no role bypass is allowed.
  async _reservePaid(s,{role,provider,kind,maxReserveMicros}) {
    assert(ROLES.includes(role) && typeof provider==='string' && provider.length>0,'valid paid role/provider required');
    assert((await this.gate(s.spec,provider))===s.admissionRevision,'admission revision drift');
    assert(s.invocations.every(i=>!['RESERVED','PENDING'].includes(i.status)),'uncertain invocation requires recovery');
    assert(validMoney(maxReserveMicros),'nonzero pre-invocation reservation required');
    assert(s.ledger.calls < s.spec.budget.maxCalls &&
      s.ledger.chargedMicros+s.ledger.reservedMicros+maxReserveMicros <= s.spec.budget.capMicros,
      'single paid-role task+fallback+review+corrections cap exceeded');
    if(s.spec.mode==='live') {
      assert(typeof this.capabilityReader==='function','per-provider capability acceptance required');
      const accepted=await this.capabilityReader(provider);
      assert(accepted?.provider===provider && accepted.status==='ACCEPTED' &&
        accepted.secretIsolation===true && accepted.usageAccounting===true &&
        accepted.idempotency===true && accepted.sandbox===true,'provider capability acceptance pending');
    } else {
      assert(kind==='review'?this.reviewer?.mock===true:this.backend?.offlineSafe===true,
        'offline mode refuses non-mock execution/review');
    }
    const invocationId=digest({taskKey:s.taskKey,call:s.ledger.calls+1,provider,role,kind});
    const entry={invocationId,provider,role,kind,maximumMicros:maxReserveMicros,status:'RESERVED',usageMicros:null};
    s.invocations.push(entry);s.ledger.calls++;s.ledger.reservedMicros+=maxReserveMicros;
    return entry;
  }
  async invoke({taskKey,role='Executor',provider,maxReserveMicros,kind='implementation',authorization=null,payload={}}) {
    return this.store.withTask(taskKey,async(get,save) => {
      const state = get();
      assert(state && !['BLOCKED_UNCERTAIN','READY_CHANGE'].includes(state.phase),'task absent or blocked');
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
        if (state.spec.mode === 'live') {
          assert(typeof this.correctionGrantReader === 'function','trusted correction grant reader required');
          const grant = await this.correctionGrantReader(state.spec.task);
          const at = Date.parse(grant?.checkedAt);
          assert(grant?.status === 'APPROVED' && grant.taskId === state.taskId && grant.taskKey === state.taskKey &&
            grant.reference === authorization.reference && grant.headSha === publishedSha &&
            grant.findingsPackageCount === 1 && grant.batch === state.corrections+1 &&
            Number.isFinite(at) && at <= Date.now() && Date.now()-at <= 120000,
            'current-head authorized correction batch not confirmed');
        }
      }
      const entry=await this._reservePaid(state,{role,provider,kind,maxReserveMicros});
      const invocationId=entry.invocationId;
      if (kind === 'correction') {
        state.corrections++;
        if (state.publication) state.publicationHistory.push(state.publication);
        state.publication=null;state.verified=null;state.ci=null;state.readyIntent=null;
      }
      state.phase='INVOCATION_PENDING';
      await save(state); // atomic persisted reservation before dispatch/model invocation
      let response;
      try { response = await this.backend.run({invocationId,taskKey:state.taskKey,taskId:state.taskId,jobSpecDigest:state.specDigest,role,provider,payload,mode:state.spec.mode}); }
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
    const result = validateResult(input,{taskKey:state.taskKey,taskId:state.taskId,invocationId,role:i.role});
    const usage = result.usageMicros === null ? i.maximumMicros : result.usageMicros;
    state.ledger.reservedMicros -= i.maximumMicros;
    state.ledger.chargedMicros += Math.max(i.maximumMicros,usage);
    if (result.usageMicros === null) state.ledger.unknownCalls++;
    i.status=result.status;i.usageMicros=result.usageMicros;i.principalId=result.principalId;
    i.debitedMicros=Math.max(i.maximumMicros,usage);i.resultDigest=digest(result);
    state.phase=usage > i.maximumMicros || result.status==='unknown' ? 'BLOCKED_UNCERTAIN' : result.status==='completed' ? 'EXECUTED' : 'BLOCKED';
    await save(state);
    return {kind:'settled',result,chargedMicros:i.debitedMicros,phase:state.phase};
  }
  async receiveResult(taskKey,invocationId,result) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get(), i=s?.invocations.find(x=>x.invocationId===invocationId);
      if(i && ['completed','failed','unknown'].includes(i.status)) {
        assert(i.resultDigest===digest(result),'conflicting duplicate callback');
        return {kind:'already-settled',phase:s.phase};
      }
      assert(i?.status==='PENDING','no pending invocation');
      return this._settle(s,save,result,invocationId);
    });
  }
  async recover(taskKey) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get();assert(s,'task absent');
      if(s.invocations.some(i=>['RESERVED','PENDING'].includes(i.status)) ||
         ['PUBLISH_PENDING','READY_PENDING','REVIEW_PENDING'].includes(s.phase) ||
         Object.values(s.reviewBySha).some(r=>r.status==='REQUESTED')) {
        s.phase='BLOCKED_UNCERTAIN'; await save(s);
      }
      return s;
    });
  }
  async verify(taskKey) {
    return this.store.withTask(taskKey,async(get,save)=>{
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
      ['pull_request','merge_request','change_proposal'].includes(receipt.changeRef?.kind) &&
      typeof receipt.changeRef?.id==='string' && receipt.changeRef.id.length>0 && receipt.changeRef.id.length<=512 && receipt.commitCount===1 && receipt.pushCount===1 &&
      receipt.forcePush===false && receipt.historyRewrite===false && receipt.humanMerge===true;
  }
  async publish(taskKey) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get();assert(s?.phase==='VERIFIED' && !s.publication,'verified un-published revision required');
      assert((await this.gate(s.spec))===s.admissionRevision,'admission revision drift');
      assert(typeof this.publisher?.publish==='function','trusted publisher required');
      if(s.spec.mode==='offline') assert(this.publisher.mock===true,'offline forbids real publication');
      const key=digest({taskKey:s.taskKey,verifiedDigest:digest(s.verified),branch:s.spec.task.branch});
      s.phase='PUBLISH_PENDING';s.publication={key,status:'PENDING'};await save(s);
      let receipt;
      try {receipt=await this.publisher.publish({spec:s.spec,verified:s.verified,key});}
      catch {s.phase='BLOCKED_UNCERTAIN';await save(s);throw new Error('publisher outcome uncertain; read-only reconcile by key');}
      assert(this.validPublication(receipt,s,key),'trusted publisher receipt invalid');
      s.publication={key,status:'PUBLISHED',receipt};s.phase='PUBLISHED';await save(s);return receipt;
    });
  }
  async reconcilePublication(taskKey) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get();assert(s?.publication?.status==='PENDING','no pending publisher intent');
      assert(typeof this.publisher?.lookup==='function','read-only publisher lookup required');
      const receipt=await this.publisher.lookup(s.publication.key);
      assert(this.validPublication(receipt,s,s.publication.key),'outcome unknown; manual reconciliation needed');
      s.publication={key:receipt.key,status:'PUBLISHED',receipt};s.phase='PUBLISHED';await save(s);return receipt;
    });
  }
  async requestReview(taskKey,{provider,maxReserveMicros}={}) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get();assert(s?.phase==='PUBLISHED','published change required for review');
      assert(typeof this.ciReader==='function' && typeof this.reviewer?.request==='function',
        'trusted CI and reviewer adapters required');
      const sha=s.publication.receipt.headSha;
      assert(!s.reviewBySha[sha],'review already requested for exact SHA');
      const ci=await this.ciReader(s.publication.receipt);
      assert(ci?.headSha===sha && ci.status==='green' && ci.requiredChecksPassed===true,
        'required CI not green on current published SHA');
      const principalId=this.reviewer.principalId;
      assert(typeof principalId==='string' && principalId.length>0,'reviewer principal required');
      assert(!s.invocations.some(i=>i.role==='Executor' && i.principalId===principalId),
        'review principal must differ from executor principal');
      const entry=await this._reservePaid(s,{role:'Reviewer',provider,kind:'review',maxReserveMicros});
      s.ci=ci;s.reviewBySha[sha]={status:'REQUESTED',invocationId:entry.invocationId,principalId};
      s.phase='REVIEW_PENDING';
      await save(s); // persisted reservation and SHA dedup before any reviewer request
      let review;
      try {
        review=await this.reviewer.request({taskKey:s.taskKey,taskId:s.taskId,sha,
          changeRef:s.publication.receipt.changeRef,provider,invocationId:entry.invocationId,readOnly:true});
        if(review?.status==='pending') {
          this._assertReview(s,review,entry);
          entry.status='PENDING';
          s.reviewBySha[sha]={status:'pending',invocationId:entry.invocationId,principalId,reference:review.reference||null};
          await save(s);return review;
        }
        return await this._settleReview(s,save,review);
      } catch(err) {
        s.phase='BLOCKED_UNCERTAIN';await save(s);
        throw new Error('review outcome uncertain; paid reservation retained: '+err.message);
      }
    });
  }
  _assertReview(s,review,entry) {
    assert(review?.taskKey===s.taskKey && review.invocationId===entry.invocationId &&
      review.sha===s.publication?.receipt?.headSha &&
      ['pass','findings','pending'].includes(review.status) && review.readOnly===true &&
      review.principalId===this.reviewer?.principalId &&
      !s.invocations.some(i=>i.role==='Executor' && i.principalId===review.principalId),
      'review principal or result identity mismatch');
    assert(review.usageMicros===null || (Number.isSafeInteger(review.usageMicros) && review.usageMicros>=0),
      'review usage must be numeric or explicitly unknown');
  }
  async _settleReview(s,save,review) {
    const prior=s.reviewBySha[review?.sha],i=s.invocations.find(x=>x.invocationId===prior?.invocationId);
    assert(i?.role==='Reviewer' && ['PENDING','RESERVED'].includes(i.status),'no unsettled paid review');
    this._assertReview(s,review,i);
    assert(['pass','findings'].includes(review.status),'terminal review result required');
    const usage=review.usageMicros===null?i.maximumMicros:review.usageMicros;
    s.ledger.reservedMicros-=i.maximumMicros;s.ledger.chargedMicros+=Math.max(i.maximumMicros,usage);
    if(review.usageMicros===null)s.ledger.unknownCalls++;
    i.status='completed';i.usageMicros=review.usageMicros;i.principalId=review.principalId;
    i.debitedMicros=Math.max(i.maximumMicros,usage);i.resultDigest=digest(review);
    s.reviewBySha[review.sha]={status:review.status,principalId:review.principalId,
      invocationId:i.invocationId,resultDigest:i.resultDigest,reference:review.reference||null};
    s.phase=usage>i.maximumMicros?'BLOCKED_UNCERTAIN':review.status==='pass'?'REVIEWED':'REVIEW_FINDINGS';
    await save(s);return review;
  }
  async receiveReview(taskKey,review) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get(),prior=s?.reviewBySha[review?.sha];
      if(prior && ['pass','findings'].includes(prior.status)) {
        assert(prior.resultDigest===digest(review),'conflicting duplicate review callback');
        return {kind:'already-reviewed',phase:s.phase};
      }
      assert(prior && ['REQUESTED','pending'].includes(prior.status),'no requested SHA review');
      return this._settleReview(s,save,review);
    });
  }
  async markReady(taskKey) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get();assert(s?.phase==='REVIEWED','independent review pass required');
      assert((await this.gate(s.spec))===s.admissionRevision,'admission revision drift');
      const head=s.publication.receipt.headSha;
      assert(s.reviewBySha[head]?.status==='pass' && s.ci?.headSha===head,'review/CI SHA mismatch');
      const ci=await this.ciReader(s.publication.receipt);
      assert(ci?.status==='green' && ci.headSha===head && ci.requiredChecksPassed===true,'required CI drift');
      assert(typeof this.publisher?.markReady==='function','trusted mark-ready required');
      if(s.spec.mode==='offline') assert(this.publisher.mock===true,'offline forbids real readiness mutation');
      s.readyIntent={key:s.publication.key,headSha:head,changeRef:s.publication.receipt.changeRef};
      s.phase='READY_PENDING';await save(s);
      let receipt;
      try {receipt=await this.publisher.markReady(s.readyIntent);}
      catch {s.phase='BLOCKED_UNCERTAIN';await save(s);throw new Error('ready outcome uncertain; read-only reconcile');}
      assert(receipt?.headSha===head && receipt.changeRef?.kind===s.readyIntent.changeRef.kind &&
        receipt.changeRef?.id===s.readyIntent.changeRef.id && receipt.draft===false && receipt.humanMerge===true,'ready change read-back invalid');
      s.ready=true;s.phase='READY_CHANGE';await save(s);return receipt;
    });
  }
  async reconcileReady(taskKey) {
    return this.store.withTask(taskKey,async(get,save)=>{
      const s=get();assert(s?.readyIntent && !s.ready,'no pending ready intent');
      assert(typeof this.publisher?.lookupReady==='function','read-only PR lookup required');
      const receipt=await this.publisher.lookupReady(s.readyIntent);
      assert(receipt?.headSha===s.readyIntent.headSha && receipt.changeRef?.kind===s.readyIntent.changeRef.kind &&
        receipt.changeRef?.id===s.readyIntent.changeRef.id && receipt.draft===false && receipt.humanMerge===true,'ready state unconfirmed');
      s.ready=true;s.phase='READY_CHANGE';await save(s);return receipt;
    });
  }
}
