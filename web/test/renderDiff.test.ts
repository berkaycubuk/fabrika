import { JSDOM } from "jsdom";
import { describe, it } from "node:test";
import assert from "node:assert/strict";

// Must set document and Node globals before any call to renderDiff
// (el() uses document.createElement and c instanceof Node)
const { window } = new JSDOM("<!DOCTYPE html><html><body></body></html>");
const g = global as unknown as Record<string, unknown>;
g.document = window.document;
g.Node = window.Node;

import { renderDiff } from "../src/views/diff-view.js";

const MULTI_DIFF = `diff --git a/src/foo.ts b/src/foo.ts
index abc123..def456 100644
--- a/src/foo.ts
+++ b/src/foo.ts
@@ -1,3 +1,3 @@
 context-line
-old-line
+new-line
 end-line
diff --git a/src/bar.ts b/src/bar.ts
new file mode 100644
index 0000000..abc123
--- /dev/null
+++ b/src/bar.ts
@@ -0,0 +1,2 @@
+added1
+added2
`;

const RENAMED_DIFF = `diff --git a/src/a.ts b/src/b.ts
similarity index 80%
rename from src/a.ts
rename to src/b.ts
index abc123..def456 100644
--- a/src/a.ts
+++ b/src/b.ts
@@ -1,2 +1,2 @@
 unchanged
-old
+new
`;

const BINARY_DIFF = `diff --git a/assets/img.png b/assets/img.png
index abc123..def456 100644
Binary files a/assets/img.png and b/assets/img.png differ
`;

describe("renderDiff", () => {
  it("returns an HTMLElement", () => {
    const result = renderDiff(MULTI_DIFF);
    assert.ok(result instanceof window.HTMLElement);
  });

  it("produces one .diff-file section per file", () => {
    const result = renderDiff(MULTI_DIFF);
    const sections = result.querySelectorAll(".diff-file");
    assert.strictEqual(sections.length, 2);
  });

  it("each summary contains the file path", () => {
    const result = renderDiff(MULTI_DIFF);
    const summaries = result.querySelectorAll(".diff-file > summary");
    assert.strictEqual(summaries.length, 2);
    assert.ok(summaries[0].textContent?.includes("src/foo.ts"));
    assert.ok(summaries[1].textContent?.includes("src/bar.ts"));
  });

  it("add lines carry class dl add", () => {
    const result = renderDiff(MULTI_DIFF);
    const addLines = result.querySelectorAll(".dl.add");
    assert.ok(addLines.length > 0, "expected at least one .dl.add row");
  });

  it("del lines carry class dl del", () => {
    const result = renderDiff(MULTI_DIFF);
    const delLines = result.querySelectorAll(".dl.del");
    assert.ok(delLines.length > 0, "expected at least one .dl.del row");
  });

  it("context lines carry class dl context", () => {
    const result = renderDiff(MULTI_DIFF);
    const ctxLines = result.querySelectorAll(".dl.context");
    assert.ok(ctxLines.length > 0, "expected at least one .dl.context row");
  });

  it("hunk header lines carry class dl hunk", () => {
    const result = renderDiff(MULTI_DIFF);
    const hunkLines = result.querySelectorAll(".dl.hunk");
    assert.ok(hunkLines.length > 0, "expected at least one .dl.hunk row");
  });

  it("renamed file summary shows old → new path", () => {
    const result = renderDiff(RENAMED_DIFF);
    const summary = result.querySelector(".diff-file > summary");
    assert.ok(summary?.textContent?.includes("src/a.ts"));
    assert.ok(summary?.textContent?.includes("src/b.ts"));
  });

  it("binary file renders binary note instead of hunks", () => {
    const result = renderDiff(BINARY_DIFF);
    const binaryNote = result.querySelector(".diff-binary");
    assert.ok(binaryNote, "expected .diff-binary element");
    assert.ok(binaryNote.textContent?.includes("binary file"));
  });

  it("single file diff has one section", () => {
    const result = renderDiff(RENAMED_DIFF);
    const sections = result.querySelectorAll(".diff-file");
    assert.strictEqual(sections.length, 1);
  });

  it("includes diff-summary line", () => {
    const result = renderDiff(MULTI_DIFF);
    const summary = result.querySelector(".diff-summary");
    assert.ok(summary, "expected .diff-summary element");
    assert.ok(summary.textContent?.includes("files changed"));
  });

  it("returns div for empty diff", () => {
    const result = renderDiff("");
    assert.ok(result instanceof window.HTMLElement);
    const sections = result.querySelectorAll(".diff-file");
    assert.strictEqual(sections.length, 0);
  });
});
