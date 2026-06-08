import { api } from "../api.js";

export type UndoableAction = "accept" | "reject";

export interface ToastSpec { message: string; label: string; onUndo: () => Promise<unknown>; }

export function undoToastSpec(action: UndoableAction, task: { id: string; title: string }): ToastSpec {
  switch (action) {
    case "accept":
      return {
        message: `Merged: ${task.title}`,
        label: "Undo",
        onUndo: () => api.revertTask(task.id),
      };
    case "reject":
      return {
        message: `Kicked back: ${task.title}`,
        label: "Undo",
        onUndo: () => api.retryTask(task.id),
      };
  }
}
