import { assert, ROLES } from './contracts.mjs';

// ModelProvider owns protocol/usage. AgentRuntime owns role routing. Neither owns publication.

// Explicit provider protocol; vendors remain adapter implementations, never roles.
export class ModelProvider {
  constructor({id,roles,accepted=false,offlineOnly=false}) {
    assert(typeof id==='string' && id.length>0,'provider id required');
    assert(Array.isArray(roles) && roles.every(role=>ROLES.includes(role)),'provider roles invalid');
    Object.assign(this,{id,roles,accepted,offlineOnly});
  }
  async invoke(_request) { throw new Error('ModelProvider.invoke must be implemented by an adapter'); }
}

export class AgentRuntime {
  constructor(providers) { this.providers = providers; }
  async invoke({provider, role, payload, invocationId, mode}) {
    assert(ROLES.includes(role), 'unsupported role');
    const adapter = this.providers[provider];
    assert(adapter && adapter.accepted === true && adapter.roles.includes(role), 'provider capability not accepted: ' + provider);
    if (mode === 'offline') assert(adapter.offlineOnly === true,'offline mode refuses real models');
    return adapter.invoke({role,payload,invocationId});
  }
}

export function sanitizeExecutorEnv(env) {
  const out = {};
  for(const k of ['PATH','HOME','LANG','TZ','TMPDIR']) if(typeof env[k] === 'string') out[k] = env[k];
  return out;
}

// Transport backends do not assume that dispatch has completed model execution.
export class ActionsBackend {
  constructor(dispatch) { this.dispatch = dispatch; }
  async run(request) {
    assert(typeof this.dispatch === 'function','Actions dispatch not configured');
    const reference = await this.dispatch({
      invocationId:request.invocationId,
      jobSpecDigest:request.jobSpecDigest,
      role:request.role,
      provider:request.provider
    });
    return {kind:'pending',reference};
  }
}
export class DedicatedMachineBackend {
  constructor(submit) { this.submit = submit; }
  async run(request) {
    assert(typeof this.submit === 'function','machine backend not configured');
    return {kind:'pending',reference:await this.submit(request)};
  }
}
export class LocalEndpointBackend {
  constructor(submit) { this.submit = submit; }
  async run(request) {
    assert(typeof this.submit === 'function','local endpoint backend not configured');
    return {kind:'pending',reference:await this.submit(request)};
  }
}
export class OfflineBackend {
  constructor(runtime) { this.runtime = runtime; this.offlineSafe = true; }
  async run(request) { return {kind:'result',result:await this.runtime.invoke(request)}; }
}
export const capabilityAcceptance = Object.freeze({
  codex: {status:'PENDING',requires:['protocol','usage ceiling','idempotency','secret isolation','sandbox']},
  claude: {status:'PENDING',requires:['protocol','usage ceiling','idempotency','secret isolation','sandbox']},
  local: {status:'PENDING',requires:['model pin','endpoint allowlist','usage ceiling','idempotency','sandbox']}
});
