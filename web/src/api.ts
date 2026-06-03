// Typed fetch wrappers over the Fabrika REST API (SPECS.md §11).
import type { Agent, Task } from "./types.js";

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
};
