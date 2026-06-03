// Typed fetch wrappers over the Fabrika REST API (SPECS.md §11).
import type { Agent, Task, ReviewItem, Metrics } from "./types.js";

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
  createTask: (t: Partial<Task>) => req<Task>("POST", "/api/tasks", t),

  // BigTask (define)
  createBigTask: (b: { title: string; intent: string }) =>
    req<unknown>("POST", "/api/bigtasks", b),

  // Accept queue (live loop)
  listReviews: () => req<ReviewItem[]>("GET", "/api/reviews"),
  acceptTask: (id: string) => req<{ status: string }>("POST", `/api/tasks/${id}/accept`),
  rejectTask: (id: string, reason: string) =>
    req<{ status: string }>("POST", `/api/tasks/${id}/reject`, { reason }),

  // Scheduling / steering (Phase 1)
  metrics: () => req<Metrics>("GET", "/api/metrics"),
  assignTask: (id: string, agentId: string) =>
    req<Task>("POST", `/api/tasks/${id}/assign`, { agentId }),
  cancelTask: (taskId: string) =>
    req<{ status: string }>("POST", "/api/steer", { action: "cancel", taskId }),
  getSettings: () => req<Record<string, string>>("GET", "/api/settings"),
  putSettings: (s: Record<string, string>) =>
    req<Record<string, string>>("PUT", "/api/settings", s),
};
