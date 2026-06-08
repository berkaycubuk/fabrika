// Pure, framework-free board filtering/counting logic.
// No DOM, no imports from other app modules — pure functions only.

export interface CardFilter {
  search: string;
  risk: string[];
  agent: string[];
  status: string[];
}

export interface Filterable {
  title: string;
  riskTier?: string;
  agentId?: string;
  pushStatus?: string;
}

export function emptyFilter(): CardFilter {
  return { search: "", risk: [], agent: [], status: [] };
}

export function matchesSearch(title: string, query: string): boolean {
  const q = query.trim();
  if (q === "") return true;
  return title.toLowerCase().includes(q.toLowerCase());
}

export function matchesFilter(item: Filterable, f: CardFilter): boolean {
  return (
    matchesSearch(item.title, f.search) &&
    (f.risk.length === 0 || f.risk.includes(item.riskTier ?? "")) &&
    (f.agent.length === 0 || f.agent.includes(item.agentId ?? "")) &&
    (f.status.length === 0 || f.status.includes(item.pushStatus ?? ""))
  );
}

export function filterItems<T extends Filterable>(items: T[], f: CardFilter): T[] {
  return items.filter((i) => matchesFilter(i, f));
}

export function isFilterActive(f: CardFilter): boolean {
  return (
    f.search.trim() !== "" ||
    f.risk.length > 0 ||
    f.agent.length > 0 ||
    f.status.length > 0
  );
}

export function countLabel(shown: number, total: number): string {
  if (total === 0) return "";
  if (shown === total) return String(total);
  return `${shown} / ${total}`;
}

export function distinctValues<T extends Filterable>(
  items: T[],
  key: "riskTier" | "agentId" | "pushStatus",
): string[] {
  const set = new Set<string>();
  for (const item of items) {
    const value = item[key];
    if (value !== undefined && value !== "") set.add(value);
  }
  return Array.from(set).sort();
}
