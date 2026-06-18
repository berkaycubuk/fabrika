export function mentionQuery(text: string, caret: number): string | null {
  const sub = text.slice(0, caret);
  const atIdx = sub.lastIndexOf("@");
  if (atIdx === -1) return null;
  if (atIdx > 0 && !/\s/.test(text[atIdx - 1])) return null;
  const between = sub.slice(atIdx + 1);
  if (/\s/.test(between)) return null;
  return between;
}

export function matchAgents<T extends { name: string; enabled: boolean }>(agents: T[], query: string): T[] {
  const q = query.toLowerCase();
  return agents.filter((a) => a.enabled && a.name.toLowerCase().includes(q));
}

export function applyMention(text: string, caret: number, name: string): { text: string; caret: number } {
  const sub = text.slice(0, caret);
  const atIdx = sub.lastIndexOf("@");
  const before = text.slice(0, atIdx);
  const after = text.slice(caret);
  const insert = "@" + name + " ";
  return { text: before + insert + after, caret: before.length + insert.length };
}
