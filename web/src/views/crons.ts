// Schedules screen: create/edit/enable/disable/run cron schedules.
import { api } from "../api.js";
import { el, clear } from "../dom.js";
import { button, pill, field } from "../components.js";
import { openModal, closeModal } from "../ui.js";
import type { CronSchedule, Agent } from "../types.js";

export function renderCrons(root: HTMLElement): void {
  clear(root);
  root.append(
    el("div", { class: "view-header" }, [
      el("h1", {}, ["Schedules"]),
      el("p", { class: "muted" }, [
        "Recurring prompts fired on a cron expression. Each run creates a one-shot agent session.",
      ]),
    ]),
    el("div", { class: "form-actions" }, [
      button("Add schedule", { variant: "primary", onclick: () => openCronModal() }),
    ]),
    el("div", { id: "cron-list", class: "card-list" }, ["Loading…"]),
  );
  refresh();
}

function openCronModal(cron?: CronSchedule): void {
  const editingId: string | null = cron?.id ?? null;

  const titleInput = el("input", { placeholder: "e.g. Nightly cleanup" }) as HTMLInputElement;
  const promptInput = el("textarea", { rows: "4", placeholder: "Describe the task…" }) as HTMLTextAreaElement;
  const agentSelect = el("select", {}) as HTMLSelectElement;
  const exprInput = el("input", { placeholder: "0 3 * * *" }) as HTMLInputElement;
  const exprHint = el("span", { class: "muted sm" }, ["5-field cron: minute hour day month weekday"]);

  api.listAgents().then((agents: Agent[]) => {
    const implementers = agents.filter((a) => a.enabled && a.roles?.includes("implementer"));
    clear(agentSelect);
    if (implementers.length === 0) {
      agentSelect.append(el("option", { value: "" }, ["(no implementer agents)"]));
    } else {
      for (const a of implementers) {
        agentSelect.append(el("option", { value: a.id }, [a.name]));
      }
    }
    if (cron) agentSelect.value = cron.agentId;
  }).catch(() => {
    agentSelect.append(el("option", { value: "" }, ["(failed to load agents)"]));
  });

  if (cron) {
    titleInput.value = cron.title;
    promptInput.value = cron.prompt;
    exprInput.value = cron.expr;
  }

  const err = el("div", { class: "form-error" });
  const submitBtn = button(cron ? "Save changes" : "Add schedule", { variant: "primary", type: "submit" });

  const form = el("form", {
    class: "modal-form",
    onsubmit: async (e: Event) => {
      e.preventDefault();
      err.textContent = "";
      const payload: Partial<CronSchedule> = {
        title: titleInput.value.trim(),
        prompt: promptInput.value.trim(),
        agentId: agentSelect.value,
        expr: exprInput.value.trim(),
        enabled: cron?.enabled ?? true,
      };
      try {
        if (editingId) {
          await api.updateCron(editingId, payload);
        } else {
          await api.createCron(payload);
        }
        closeModal();
        refresh();
      } catch (e2) {
        err.textContent = (e2 as Error).message;
      }
    },
  }, [
    field("Title", titleInput),
    field("Prompt", promptInput),
    field("Agent", agentSelect),
    field("Expression", el("div", {}, [exprInput, exprHint])),
    err,
    el("div", { class: "form-actions" }, [submitBtn]),
  ]) as HTMLFormElement;

  openModal(cron ? "Edit schedule" : "Add schedule", form);
}

async function refresh(): Promise<void> {
  const list = document.getElementById("cron-list");
  if (!list) return;
  try {
    const [crons, agents] = await Promise.all([api.listCrons(), api.listAgents()]);
    const agentMap = new Map(agents.map((a) => [a.id, a.name]));
    clear(list);
    if (crons.length === 0) {
      list.append(el("p", { class: "muted" }, ["No schedules yet. Add one above."]));
      return;
    }
    for (const c of crons) list.append(cronCard(c, agentMap));
  } catch (e) {
    list.textContent = (e as Error).message;
  }
}

function formatTime(ts: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}

function cronCard(c: CronSchedule, agentMap: Map<string, string>): HTMLElement {
  const err = el("div", { class: "form-error" });

  const withErr = async (fn: () => Promise<void>): Promise<void> => {
    err.textContent = "";
    try {
      await fn();
    } catch (e) {
      err.textContent = (e as Error).message;
    }
  };

  return el("div", { class: "card" }, [
    el("div", { class: "card-main" }, [
      el("div", { class: "card-title" }, [
        c.title,
        pill(c.enabled ? "enabled" : "disabled", c.enabled ? "on" : "off"),
      ]),
      el("code", { class: "card-cmd" }, [c.expr]),
      el("div", { class: "card-meta" }, [
        el("span", {}, [agentMap.get(c.agentId) ?? c.agentId]),
        el("span", { class: "muted" }, [`Last: ${formatTime(c.lastRunAt)} · Next: ${formatTime(c.nextRunAt)}`]),
      ]),
    ]),
    err,
    el("div", { class: "card-actions" }, [
      button("Edit", { onclick: () => openCronModal(c) }),
      button(c.enabled ? "Disable" : "Enable", {
        onclick: () => withErr(async () => {
          c.enabled ? await api.disableCron(c.id) : await api.enableCron(c.id);
          refresh();
        }),
      }),
      button("Run now", {
        onclick: () => withErr(async () => {
          await api.runCron(c.id);
          refresh();
        }),
      }),
      button("Delete", {
        variant: "danger",
        onclick: () => withErr(async () => {
          if (confirm(`Delete schedule "${c.title}"?`)) {
            await api.deleteCron(c.id);
            refresh();
          }
        }),
      }),
    ]),
  ]);
}
