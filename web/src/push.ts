import type { Task } from "./types.js";

export function pushStatusLabel(t: Task): string | null {
  if (t.status !== "merged") return null;
  return t.pushed ? "pushed" : "unpushed";
}
