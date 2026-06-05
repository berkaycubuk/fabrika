// Wire types mirroring internal/model (SPECS.md §5). Only the fields the Phase 0
// UI reads/writes are typed richly; the rest are kept loose.

export interface Agent {
  id: string;
  name: string;
  command: string;
  model: string;
  roles: string[];
  tags: string[];
  concurrency: number;
  timeout: string;
  maxAttempts: number;
  // Higher number = higher routing priority; 0 = normal (default).
  priority: number;
  enabled: boolean;
  photo: string;
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
  attachments: string[];
  riskTier: string;
  priority: string;
  status: string;
  branch: string;
  agentId: string;
  preferredAgentId: string;
  autoMerged: boolean;
  auditFlagged: boolean;
  reverted: boolean;
  reporter: string;
}

export interface StageResult {
  pass: boolean;
  output: string;
  skipped: boolean;
  exitCode: number;
}

export interface Evidence {
  stages: Record<string, StageResult>;
  diff: string;
  artifacts: string[];
}

// Usage mirrors internal/model.Usage: token usage an agent self-reports for a run.
export interface Usage {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

export interface Attempt {
  id: string;
  taskId: string;
  agentId: string;
  result: string; // pass|fail|escalated
  evidence: Evidence;
  usage?: Usage;
  log: string;
}

export interface ReviewItem {
  task: Task;
  attempt: Attempt | null;
}

export interface BigTask {
  id: string;
  title: string;
  intent: string;
  constraints: string[];
  attachments: string[];
  repoPath: string;
  status: string; // draft|planning|planned|running|done|error
  error: string; // failure reason when status === "error"
  plannerAgentId: string; // which registered planner agent is decomposing this
  planFeedback?: string;
}

export interface Decision {
  id: string;
  planId: string;
  taskId: string;
  question: string;
  options: string[];
  context: string;
  answer: string;
  promote: boolean;
  status: string; // open|answered
}

export interface Plan {
  id: string;
  bigTaskId: string;
  status: string; // proposed|approved|rejected
  tasks: Task[];
  openDecisions: Decision[];
  bigTask: BigTask | null;
}

export interface AgentMetrics {
  agentId: string;
  name: string;
  enabled: boolean;
  concurrency: number;
  running: number;
  planning: number;
  merged: number;
  planned: number;
  kickedBack: number;
  kickbackRate: number;
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
}

export interface Metrics {
  agents: AgentMetrics[];
  wip: number;
  planning: number;
  wipCap: number;
  ready: number;
  inReview: number;
  blocked: number;
  merged: number;
  throughput: number;
  // Trust + autonomy (Phase 3).
  autoMerged: number;
  manualMerged: number;
  reverted: number;
  auditQueue: number;
  autoMergeShare: number;
  touchesPerUnit: number;
  changeFailRate: number;
  auditRate: number;
  mutationTesting: boolean;
  totalTokens?: number; // board-wide sum of agent token usage
}

export interface Comment {
  id: string;
  taskId: string;
  authorType: string;
  authorId: string;
  body: string;
  attachments: string[];
  createdAt: string;
}

export interface FabrikaEvent {
  type: string;
  payload: unknown;
}

// Fixed gate stage order (mirrors internal/gate stageOrder) for stable display,
// followed by the Phase 3 advisory stages (reviewer verdict + mutation testing).
export const STAGE_ORDER = ["setup", "typecheck", "lint", "build", "test", "verify", "e2e", "review", "mutation"];

export const ROLES = ["implementer", "planner", "reviewer"];
