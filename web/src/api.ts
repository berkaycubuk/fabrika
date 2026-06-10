// Typed fetch wrappers over the Fabrika REST API (SPECS.md §11).
import type { Agent, Task, Attempt, ReviewItem, Metrics, Plan, Decision, BigTask, Comment, ConfigManifest, Convention, Release } from "./types.js";

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = await res.json();
      if (j && j.error) msg = j.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return text ? (JSON.parse(text) as T) : (undefined as T);
}

export const api = {
  // Agents (global store)
  listAgents: () => req<Agent[]>("GET", "/api/agents"),
  createAgent: (a: Partial<Agent>) => req<Agent>("POST", "/api/agents", a),
  updateAgent: (id: string, a: Partial<Agent>) => req<Agent>("PUT", `/api/agents/${id}`, a),
  deleteAgent: (id: string) => req<void>("DELETE", `/api/agents/${id}`),
  enableAgent: (id: string) => req<Agent>("POST", `/api/agents/${id}/enable`),
  disableAgent: (id: string) => req<Agent>("POST", `/api/agents/${id}/disable`),

  // Tasks (per-project store)
  listTasks: () => req<Task[]>("GET", "/api/tasks"),
  getTask: (id: string) => req<{ task: Task; attempts: Attempt[] }>("GET", `/api/tasks/${id}`),
  createTask: (t: Partial<Task>) => req<Task>("POST", "/api/tasks", t),
  listComments: (id: string) => req<Comment[]>("GET", `/api/tasks/${id}/comments`),
  addComment: (id: string, body: string, attachments: string[] = []) =>
    req<Comment>("POST", `/api/tasks/${id}/comments`, { body, attachments }),
  uploadImage: async (file: File): Promise<string> => {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch("/api/uploads", { method: "POST", body: form });
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const j = await res.json();
        if (j && j.error) msg = j.error;
      } catch {
        /* ignore */
      }
      throw new Error(msg);
    }
    const { url } = (await res.json()) as { url: string };
    return url;
  },

  // BigTask comments
  listBigTaskComments: (id: string) => req<Comment[]>("GET", `/api/bigtasks/${id}/comments`),
  addBigTaskComment: (id: string, body: string, attachments: string[] = []) =>
    req<Comment>("POST", `/api/bigtasks/${id}/comments`, { body, attachments }),

  // BigTask (define)
  listBigTasks: () => req<BigTask[]>("GET", "/api/bigtasks"),
  createBigTask: (b: { title: string; intent: string; constraints?: string[]; attachments?: string[]; status?: string }) =>
    req<BigTask>("POST", "/api/bigtasks", b),
  deleteBigTask: (id: string) => req<void>("DELETE", `/api/bigtasks/${id}`),
  promoteBigTask: (id: string) => req<{ status: string }>("POST", `/api/bigtasks/${id}/plan`),
  replanBigTask: (id: string) => req<{ status: string }>("POST", `/api/bigtasks/${id}/replan`),
  stopPlanning: (id: string, reason?: string) => req<{ status: string }>("POST", `/api/bigtasks/${id}/stop`, { reason }),

  // Plans (approve flow, Phase 2)
  listPlans: () => req<Plan[]>("GET", "/api/plans"),
  approvePlan: (id: string) => req<{ status: string }>("POST", `/api/plans/${id}/approve`),
  rejectPlan: (id: string) => req<{ status: string }>("POST", `/api/plans/${id}/reject`),
  revisePlan: (id: string, feedback: string) =>
    req<{ status: string }>("POST", `/api/plans/${id}/revise`, { feedback }),

  // Decisions (decide queue, Phase 2)
  listDecisions: () => req<Decision[]>("GET", "/api/decisions"),
  answerDecision: (id: string, answer: string, promote: boolean) =>
    req<{ status: string }>("POST", `/api/decisions/${id}/answer`, { answer, promote }),

  // Conventions (proposed steering rules)
  listConventions: (status?: string) => req<Convention[]>("GET", status ? `/api/conventions?status=${status}` : "/api/conventions"),
  approveConvention: (id: string) => req<{ status: string }>("POST", `/api/conventions/${id}/approve`),
  rejectConvention: (id: string) => req<{ status: string }>("POST", `/api/conventions/${id}/reject`),

  // Accept queue (live loop)
  listReviews: () => req<ReviewItem[]>("GET", "/api/reviews"),
  acceptTask: (id: string, force = false) =>
    req<{ status: string }>("POST", `/api/tasks/${id}/accept`, force ? { force } : undefined),
  rejectTask: (id: string, reason: string) =>
    req<{ status: string }>("POST", `/api/tasks/${id}/reject`, { reason }),
  retryTask: (id: string) => req<{ status: string }>("POST", `/api/tasks/${id}/retry`),
  requestChangesTask: (id: string, guidance: string) =>
    req<{ status: string }>("POST", `/api/tasks/${id}/request-changes`, { guidance }),
  deleteTask: (id: string) => req<void>("DELETE", `/api/tasks/${id}`),

  // Audit queue (Phase 3: post-merge sampling of auto-merged work)
  listAudits: () => req<ReviewItem[]>("GET", "/api/audits"),
  ackAudit: (id: string) => req<{ status: string }>("POST", `/api/tasks/${id}/audit-ok`),
  revertTask: (id: string) => req<{ status: string }>("POST", `/api/tasks/${id}/revert`),

  // Scheduling / steering (Phase 1)
  metrics: () => req<Metrics>("GET", "/api/metrics"),
  assignTask: (id: string, agentId: string) =>
    req<Task>("POST", `/api/tasks/${id}/assign`, { agentId }),
  cancelTask: (taskId: string) =>
    req<{ status: string }>("POST", "/api/steer", { action: "cancel", taskId }),
  // Ship: push the integration branch to its remote
  push: () => req<{ status: string; detail: string }>("POST", "/api/push"),
  pushStatus: () =>
    req<{ canPush: boolean; ahead: number; branch: string; remote: string }>("GET", "/api/push/status"),

  getSettings: () => req<Record<string, string>>("GET", "/api/settings"),
  putSettings: (s: Record<string, string>) =>
    req<Record<string, string>>("PUT", "/api/settings", s),

  getConfig: () => req<ConfigManifest>("GET", "/api/config"),
  putConfig: (m: ConfigManifest) => req<ConfigManifest>("PUT", "/api/config", m),

  // Releases (Phase 4)
  listReleases: () => req<Release[]>("GET", "/api/releases"),
  ship: () => req<Release>("POST", "/api/releases/ship"),
  getRelease: (id: string) => req<Release>("GET", `/api/releases/${id}`),
  rollbackRelease: (id: string) => req<Release>("POST", `/api/releases/${id}/rollback`),
  unshipped: () => req<Task[]>("GET", "/api/releases/unshipped"),
};
