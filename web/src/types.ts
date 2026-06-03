// Wire types mirroring internal/model (SPECS.md §5). Only the fields the Phase 0
// UI reads/writes are typed richly; the rest are kept loose.

export interface Agent {
  id: string;
  name: string;
  command: string;
  roles: string[];
  tags: string[];
  concurrency: number;
  timeout: string;
  maxAttempts: number;
  enabled: boolean;
}

export interface Contract {
  verifyCmds: string[];
  heldOut: string[];
  properties: string[];
  lockedGlobs: string[];
}

export interface Task {
  id: string;
  bigTaskId: string;
  title: string;
  spec: string;
  acceptance: Contract;
  dependsOn: string[];
  touchPaths: string[];
  tags: string[];
  riskTier: string;
  status: string;
  branch: string;
  agentId: string;
  preferredAgentId: string;
}

export interface FabrikaEvent {
  type: string;
  payload: unknown;
}

export const ROLES = ["implementer", "planner", "reviewer"];
