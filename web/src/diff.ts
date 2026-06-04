export type DiffStatus = "added" | "deleted" | "modified" | "renamed";

export interface DiffLine {
  type: "add" | "del" | "context";
  oldNo: number | null;
  newNo: number | null;
  text: string;
}

export interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}

export interface DiffFile {
  oldPath: string;
  newPath: string;
  status: DiffStatus;
  additions: number;
  deletions: number;
  binary: boolean;
  hunks: DiffHunk[];
}

export function parseDiff(raw: string): DiffFile[] {
  if (!raw || !raw.trim()) return [];

  const result: DiffFile[] = [];
  const sections = raw.split(/(?=^diff --git )/m);

  for (const section of sections) {
    if (!section.trim()) continue;

    const rawLines = section.split("\n");
    let i = 0;

    const firstLine = rawLines[i] ?? "";
    const gitMatch = firstLine.match(/^diff --git a\/(.*) b\/(.*)$/);
    if (!gitMatch) continue;

    let oldPath = gitMatch[1];
    let newPath = gitMatch[2];
    let status: DiffStatus = "modified";
    let binary = false;
    const hunks: DiffHunk[] = [];
    let additions = 0;
    let deletions = 0;

    i++;

    // Parse metadata lines until we reach content (---, @@, or Binary)
    let reachedContent = false;
    while (i < rawLines.length && !reachedContent) {
      const line = rawLines[i];

      if (line.startsWith("new file mode")) {
        status = "added";
        i++;
      } else if (line.startsWith("deleted file mode")) {
        status = "deleted";
        i++;
      } else if (line.startsWith("rename from ")) {
        oldPath = line.slice("rename from ".length);
        status = "renamed";
        i++;
      } else if (line.startsWith("rename to ")) {
        newPath = line.slice("rename to ".length);
        i++;
      } else if (line.startsWith("Binary files")) {
        binary = true;
        reachedContent = true;
        i++;
      } else if (line.startsWith("--- ")) {
        const fromPath = line.slice(4);
        if (fromPath !== "/dev/null") {
          oldPath = fromPath.startsWith("a/") ? fromPath.slice(2) : fromPath;
        } else if (status === "modified") {
          status = "added";
        }
        i++;
        if (i < rawLines.length && rawLines[i].startsWith("+++ ")) {
          const toPath = rawLines[i].slice(4);
          if (toPath !== "/dev/null") {
            newPath = toPath.startsWith("b/") ? toPath.slice(2) : toPath;
          } else if (status === "modified") {
            status = "deleted";
          }
          i++;
        }
        reachedContent = true;
      } else if (line.startsWith("@@")) {
        reachedContent = true;
      } else {
        i++;
      }
    }

    // Parse hunks
    while (i < rawLines.length) {
      const line = rawLines[i];
      if (!line.startsWith("@@")) {
        i++;
        continue;
      }

      const hm = line.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/);
      if (!hm) {
        i++;
        continue;
      }

      const oldStart = parseInt(hm[1], 10);
      const oldLines = hm[2] !== undefined ? parseInt(hm[2], 10) : 1;
      const newStart = parseInt(hm[3], 10);
      const newLines = hm[4] !== undefined ? parseInt(hm[4], 10) : 1;
      const header = line;
      const hunkLines: DiffLine[] = [];
      let oldNo = oldStart;
      let newNo = newStart;

      i++;

      while (i < rawLines.length && !rawLines[i].startsWith("@@")) {
        const l = rawLines[i];
        if (l.startsWith("+")) {
          hunkLines.push({ type: "add", oldNo: null, newNo: newNo++, text: l.slice(1) });
          additions++;
        } else if (l.startsWith("-")) {
          hunkLines.push({ type: "del", oldNo: oldNo++, newNo: null, text: l.slice(1) });
          deletions++;
        } else if (l.startsWith(" ")) {
          hunkLines.push({ type: "context", oldNo: oldNo++, newNo: newNo++, text: l.slice(1) });
        }
        // else: "\ No newline at end of file" or empty trailing line — skip
        i++;
      }

      hunks.push({ header, oldStart, oldLines, newStart, newLines, lines: hunkLines });
    }

    result.push({ oldPath, newPath, status, additions, deletions, binary, hunks });
  }

  return result;
}
