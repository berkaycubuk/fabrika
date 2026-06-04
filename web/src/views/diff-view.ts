import { parseDiff } from "../diff.js";
import { el } from "../dom.js";

export function renderDiff(raw: string): HTMLElement {
  const files = parseDiff(raw);
  const totalAdds = files.reduce((s, f) => s + f.additions, 0);
  const totalDels = files.reduce((s, f) => s + f.deletions, 0);
  const n = files.length;

  const summaryLine = el("p", { class: "diff-summary" }, [
    `${n} file${n === 1 ? "" : "s"} changed, +${totalAdds} -${totalDels}`,
  ]);

  const autoOpen = n <= 5;

  const sections = files.map((file) => {
    const displayPath =
      file.status === "renamed"
        ? `${file.oldPath} → ${file.newPath}`
        : file.newPath || file.oldPath;

    const summaryEl = el("summary", {}, [
      el("span", { class: `diff-badge diff-badge-${file.status}` }, [
        file.status[0].toUpperCase() + file.status.slice(1),
      ]),
      el("span", { class: "diff-path" }, [displayPath]),
      el("span", { class: "diff-stats" }, [
        `+${file.additions} -${file.deletions}`,
      ]),
    ]);

    const bodyChildren: HTMLElement[] = [];

    if (file.binary) {
      bodyChildren.push(el("div", { class: "diff-binary" }, ["(binary file)"]));
    } else {
      for (const hunk of file.hunks) {
        bodyChildren.push(el("div", { class: "dl hunk" }, [hunk.header]));
        for (const line of hunk.lines) {
          const oldNo = line.oldNo !== null ? String(line.oldNo) : "";
          const newNo = line.newNo !== null ? String(line.newNo) : "";
          bodyChildren.push(
            el("div", { class: `dl ${line.type}` }, [
              el("span", { class: "ln-old" }, [oldNo]),
              el("span", { class: "ln-new" }, [newNo]),
              el("span", { class: "ln-txt" }, [line.text]),
            ]),
          );
        }
      }
    }

    const body = el("div", { class: "diff-body" }, bodyChildren);

    return el("details", { class: "diff-file", open: autoOpen }, [
      summaryEl,
      body,
    ]);
  });

  return el("div", { class: "diff-view" }, [summaryLine, ...sections]);
}
