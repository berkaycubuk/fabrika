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
  pushed?: boolean;
  reverted: boolean;
  reporter: string;
  releaseId?: string;
  ciStatus?: string;
  ciRunUrl?: string;
}

export interface Release {
  id: string;
  sha: string;
  prevSha: string;
  status: string; // pending|deploying|baking|live|failed|rolled_back
  deployLog: string;
  healthLog: string;
  error: string;
  createdAt: string;
  deployedAt: string;
  liveAt: string;
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
  autoMode: boolean;
  totalTokens?: number; // board-wide sum of agent token usage
}

export interface Comment {
  id: string;
  taskId: string;
  bigTaskId?: string;
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

// Heartbeat is the payload of a "task.heartbeat" event: a liveness pulse for a
// running task's agent, used to show the card is making progress (or has fallen
// quiet). idleSeconds is the agent's silence; lastLine is its most recent output.
export interface Heartbeat {
  taskId: string;
  agentName: string;
  idleSeconds: number;
  lastLine: string;
  outputBytes: number;
  runningSeconds: number;
}

export interface ConfigManifest {
  project: { name: string };
  verbs: {
    setup?: string;
    build?: string;
    test?: string;
    lint?: string;
    typecheck?: string;
    verify?: string;
    e2e?: string;
    run?: string;
  };
  risk: {
    high?: string[];
    medium?: string[];
  };
  autonomy: {
    auto_merge?: string[];
    escalate?: string[];
  };
}

// Fixed gate stage order (mirrors internal/gate stageOrder) for stable display,
// followed by the Phase 3 advisory stages (reviewer verdict + mutation testing).
export const STAGE_ORDER = ["setup", "typecheck", "lint", "build", "test", "verify", "e2e", "review", "mutation"];

export interface Convention {
  id: string;
  rule: string;
  status: string;
}

// Session is an interactive chat with a coding agent in its own worktree; the
// in-UI replacement for ad-hoc terminal work. busy is engine state: a turn or
// finish is in flight.
export interface Session {
  id: string;
  title: string;
  agentId: string;
  model: string;
  baseBranch: string;
  branch: string;
  status: string; // active|gating|merged|closed
  evidence: string;
  createdAt: string;
  updatedAt: string;
  busy: boolean;
}

export interface SessionMessage {
  id: string;
  sessionId: string;
  role: string; // user|agent|system
  body: string;
  attachments: string[];
  createdAt: string;
}

// SessionStream is the payload of a "session.stream" event: the in-flight
// turn's reply-so-far (cleaned agent stdout, complete lines only). Each event
// carries the full text, so applying it is an idempotent replace.
export interface SessionStream {
  sessionId: string;
  agentName: string;
  text: string;
}

// SessionHeartbeat is the payload of a "session.heartbeat" event, mirroring
// Heartbeat for an in-flight chat turn.
export interface SessionHeartbeat {
  sessionId: string;
  agentName: string;
  idleSeconds: number;
  lastLine: string;
  outputBytes: number;
  runningSeconds: number;
}

export const ROLES = ["implementer", "planner", "reviewer"];

export interface CronSchedule {
  id: string;
  title: string;
  prompt: string;
  agentId: string;
  expr: string;
  enabled: boolean;
  lastRunAt: string;
  nextRunAt: string;
  createdAt: string;
}

// RelayInfo is the /api/relay status: portal connection + paired phones.
export interface RelayInfo {
  enabled: boolean;
  url: string;
  tokenSet: boolean;
  connected: boolean;
  daemonId: string;
  sessions: number;
  lastError: string;
  devices: RelayDevice[];
}

export interface RelayDevice {
  id: string;
  name: string;
  createdAt: string;
  lastSeen: string;
}

export interface Transition {
  id: string;
  taskId: string;
  fromStatus: string;
  toStatus: string;
  actor: string;
  reason: string;
  createdAt: string;
}
