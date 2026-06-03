// Tasks screen: manually create a task (paste spec + verify commands) and see
// the list (SPECS.md §13 Phase 0). No planner yet — one task at a time.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import type { Task } from "../types.js";

export function renderTasks(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Tasks"]),
      el("p", { class: "muted" }, [
        "Define a unit of work. Verify commands are the machine-checkable acceptance contract.",
      ]),
    ]),
    taskForm(),
    el("div", { id: "task-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

function taskForm(): HTMLElement {
  const title = el("input", { placeholder: "Title (e.g. Add /healthz endpoint)" }) as HTMLInputElement;
  const spec = el("textarea", {
    placeholder: "What to build, where, and any constraints…",
    rows: "4",
  }) as HTMLTextAreaElement;
  const verify = el("textarea", {
    placeholder: "Verify commands, one per line (e.g. go test ./...)",
    rows: "3",
  }) as HTMLTextAreaElement;
  const tags = el("input", { placeholder: "Tags, comma-separated (go, api)" }) as HTMLInputElement;
  const err = el("div", { class: "form-error" });

  return el("form", {
    class: "task-form card",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      const payload: Partial<Task> = {
        title: title.value.trim(),
        spec: spec.value.trim(),
        acceptance: {
          verifyCmds: verify.value.split("\n").map((s) => s.trim()).filter(Boolean),
          heldOut: [],
          properties: [],
          lockedGlobs: [],
        },
        tags: tags.value.split(",").map((s) => s.trim()).filter(Boolean),
      };
      try {
        await api.createTask(payload);
        (e.target as HTMLFormElement).reset();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    el("div", { class: "field" }, [el("label", {}, ["Title"]), title]),
    el("div", { class: "field" }, [el("label", {}, ["Spec"]), spec]),
    el("div", { class: "field" }, [el("label", {}, ["Verify commands"]), verify]),
    el("div", { class: "field" }, [el("label", {}, ["Tags"]), tags]),
    err,
    el("div", { class: "form-actions" }, [
      el("button", { class: "primary", type: "submit" }, ["Create task"]),
    ]),
  ]);
}

async function refresh(): Promise<void> {
  const list = document.getElementById("task-list");
  if (!list) return;
  try {
    const tasks = await api.listTasks();
    clear(list);
    if (tasks.length === 0) {
      list.append(el("p", { class: "muted" }, ["No tasks yet."]));
      return;
    }
    for (const t of tasks) list.append(taskCard(t));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function taskCard(t: Task): HTMLElement {
  return el("div", { class: "card task-card" }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        t.title,
        el("span", { class: `pill status-${t.status}` }, [t.status]),
        el("span", { class: `tag risk-${t.riskTier}` }, [t.riskTier]),
      ]),
      t.spec ? el("p", { class: "card-spec" }, [t.spec]) : el("span", {}),
      el("div", { class: "card-meta" }, [
        ...(t.acceptance?.verifyCmds ?? []).map((c) => el("code", { class: "verify-cmd" }, [c])),
        ...(t.tags ?? []).map((tag) => el("span", { class: "tag" }, [tag])),
      ]),
    ]),
  ]);
}

export function onTaskEvent(): void {
  if (document.getElementById("task-list")) refresh();
}
