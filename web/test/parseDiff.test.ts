import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { parseDiff } from "../src/diff.js";

// --- fixtures ---

const MODIFIED_DIFF = `diff --git a/src/foo.ts b/src/foo.ts
index abc123..def456 100644
--- a/src/foo.ts
+++ b/src/foo.ts
@@ -1,4 +1,4 @@
 line1
-old
+new
 line3
 line4
`;

const ADDED_DIFF = `diff --git a/src/new.ts b/src/new.ts
new file mode 100644
index 0000000..abc123
--- /dev/null
+++ b/src/new.ts
@@ -0,0 +1,2 @@
+line1
+line2
`;

const DELETED_DIFF = `diff --git a/src/old.ts b/src/old.ts
deleted file mode 100644
index abc123..0000000
--- a/src/old.ts
+++ /dev/null
@@ -1,2 +0,0 @@
-line1
-line2
`;

const RENAMED_DIFF = `diff --git a/src/a.ts b/src/b.ts
similarity index 80%
rename from src/a.ts
rename to src/b.ts
index abc123..def456 100644
--- a/src/a.ts
+++ b/src/b.ts
@@ -1,3 +1,3 @@
 unchanged
-old
+new
 end
`;

const BINARY_DIFF = `diff --git a/assets/logo.png b/assets/logo.png
index abc123..def456 100644
Binary files a/assets/logo.png and b/assets/logo.png differ
`;

const MULTI_HUNK_DIFF = `diff --git a/big.ts b/big.ts
--- a/big.ts
+++ b/big.ts
@@ -1,3 +1,3 @@
 a
-b
+B
 c
@@ -10,3 +10,3 @@
 x
-y
+Y
 z
`;

const OMITTED_COUNT_DIFF = `diff --git a/single.ts b/single.ts
--- a/single.ts
+++ b/single.ts
@@ -5 +5 @@
-old
+new
`;

// --- tests ---

describe("parseDiff", () => {
  it("returns [] for empty string", () => {
    assert.deepStrictEqual(parseDiff(""), []);
  });

  it("returns [] for whitespace-only string", () => {
    assert.deepStrictEqual(parseDiff("   \n   \n  "), []);
  });

  describe("added file", () => {
    it("status is added", () => {
      const [file] = parseDiff(ADDED_DIFF);
      assert.strictEqual(file.status, "added");
    });

    it("newPath is extracted without b/ prefix", () => {
      const [file] = parseDiff(ADDED_DIFF);
      assert.strictEqual(file.newPath, "src/new.ts");
    });

    it("counts only additions", () => {
      const [file] = parseDiff(ADDED_DIFF);
      assert.strictEqual(file.additions, 2);
      assert.strictEqual(file.deletions, 0);
    });

    it("binary is false", () => {
      const [file] = parseDiff(ADDED_DIFF);
      assert.strictEqual(file.binary, false);
    });
  });

  describe("deleted file", () => {
    it("status is deleted", () => {
      const [file] = parseDiff(DELETED_DIFF);
      assert.strictEqual(file.status, "deleted");
    });

    it("oldPath is extracted without a/ prefix", () => {
      const [file] = parseDiff(DELETED_DIFF);
      assert.strictEqual(file.oldPath, "src/old.ts");
    });

    it("counts only deletions", () => {
      const [file] = parseDiff(DELETED_DIFF);
      assert.strictEqual(file.additions, 0);
      assert.strictEqual(file.deletions, 2);
    });
  });

  describe("modified file", () => {
    it("status is modified", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      assert.strictEqual(file.status, "modified");
    });

    it("paths are stripped of a/ and b/ prefixes", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      assert.strictEqual(file.oldPath, "src/foo.ts");
      assert.strictEqual(file.newPath, "src/foo.ts");
    });

    it("counts additions and deletions", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      assert.strictEqual(file.additions, 1);
      assert.strictEqual(file.deletions, 1);
    });

    it("binary is false", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      assert.strictEqual(file.binary, false);
    });

    it("has one hunk", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      assert.strictEqual(file.hunks.length, 1);
    });
  });

  describe("renamed file", () => {
    it("status is renamed", () => {
      const [file] = parseDiff(RENAMED_DIFF);
      assert.strictEqual(file.status, "renamed");
    });

    it("oldPath comes from rename from", () => {
      const [file] = parseDiff(RENAMED_DIFF);
      assert.strictEqual(file.oldPath, "src/a.ts");
    });

    it("newPath comes from rename to", () => {
      const [file] = parseDiff(RENAMED_DIFF);
      assert.strictEqual(file.newPath, "src/b.ts");
    });

    it("has content hunks", () => {
      const [file] = parseDiff(RENAMED_DIFF);
      assert.strictEqual(file.hunks.length, 1);
    });
  });

  describe("binary file", () => {
    it("binary is true", () => {
      const [file] = parseDiff(BINARY_DIFF);
      assert.strictEqual(file.binary, true);
    });

    it("hunks array is empty", () => {
      const [file] = parseDiff(BINARY_DIFF);
      assert.deepStrictEqual(file.hunks, []);
    });

    it("additions and deletions are zero", () => {
      const [file] = parseDiff(BINARY_DIFF);
      assert.strictEqual(file.additions, 0);
      assert.strictEqual(file.deletions, 0);
    });
  });

  describe("multi-file diff", () => {
    const MULTI = MODIFIED_DIFF + ADDED_DIFF;

    it("returns one entry per file", () => {
      assert.strictEqual(parseDiff(MULTI).length, 2);
    });

    it("first file is modified", () => {
      assert.strictEqual(parseDiff(MULTI)[0].status, "modified");
    });

    it("second file is added", () => {
      assert.strictEqual(parseDiff(MULTI)[1].status, "added");
    });

    it("three-file diff has length 3", () => {
      const three = MODIFIED_DIFF + ADDED_DIFF + DELETED_DIFF;
      assert.strictEqual(parseDiff(three).length, 3);
    });
  });

  describe("multi-hunk file", () => {
    it("produces two hunks", () => {
      const [file] = parseDiff(MULTI_HUNK_DIFF);
      assert.strictEqual(file.hunks.length, 2);
    });

    it("first hunk oldStart and newStart", () => {
      const [file] = parseDiff(MULTI_HUNK_DIFF);
      assert.strictEqual(file.hunks[0].oldStart, 1);
      assert.strictEqual(file.hunks[0].newStart, 1);
    });

    it("second hunk oldStart and newStart", () => {
      const [file] = parseDiff(MULTI_HUNK_DIFF);
      assert.strictEqual(file.hunks[1].oldStart, 10);
      assert.strictEqual(file.hunks[1].newStart, 10);
    });

    it("total additions and deletions are summed across hunks", () => {
      const [file] = parseDiff(MULTI_HUNK_DIFF);
      assert.strictEqual(file.additions, 2);
      assert.strictEqual(file.deletions, 2);
    });
  });

  describe("omitted hunk count defaults to 1", () => {
    it("oldLines defaults to 1", () => {
      const [file] = parseDiff(OMITTED_COUNT_DIFF);
      assert.strictEqual(file.hunks[0].oldLines, 1);
    });

    it("newLines defaults to 1", () => {
      const [file] = parseDiff(OMITTED_COUNT_DIFF);
      assert.strictEqual(file.hunks[0].newLines, 1);
    });

    it("oldStart and newStart from the header", () => {
      const [file] = parseDiff(OMITTED_COUNT_DIFF);
      assert.strictEqual(file.hunks[0].oldStart, 5);
      assert.strictEqual(file.hunks[0].newStart, 5);
    });
  });

  describe("line numbering", () => {
    it("line numbers start at hunk oldStart / newStart", () => {
      const diff = `diff --git a/f.ts b/f.ts
--- a/f.ts
+++ b/f.ts
@@ -5,3 +5,3 @@
 ctx
-del
+add
 ctx2
`;
      const [file] = parseDiff(diff);
      const lines = file.hunks[0].lines;
      assert.strictEqual(lines[0].type, "context");
      assert.strictEqual(lines[0].oldNo, 5);
      assert.strictEqual(lines[0].newNo, 5);
      assert.strictEqual(lines[1].type, "del");
      assert.strictEqual(lines[1].oldNo, 6);
      assert.strictEqual(lines[1].newNo, null);
      assert.strictEqual(lines[2].type, "add");
      assert.strictEqual(lines[2].oldNo, null);
      assert.strictEqual(lines[2].newNo, 6);
      assert.strictEqual(lines[3].type, "context");
      assert.strictEqual(lines[3].oldNo, 7);
      assert.strictEqual(lines[3].newNo, 7);
    });

    it("context lines have both oldNo and newNo as numbers", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      const ctx = file.hunks[0].lines.find((l) => l.type === "context");
      assert.ok(ctx);
      assert.strictEqual(typeof ctx.oldNo, "number");
      assert.strictEqual(typeof ctx.newNo, "number");
    });

    it("add lines have null oldNo", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      const add = file.hunks[0].lines.find((l) => l.type === "add");
      assert.ok(add);
      assert.strictEqual(add.oldNo, null);
      assert.strictEqual(typeof add.newNo, "number");
    });

    it("del lines have null newNo", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      const del = file.hunks[0].lines.find((l) => l.type === "del");
      assert.ok(del);
      assert.strictEqual(del.newNo, null);
      assert.strictEqual(typeof del.oldNo, "number");
    });

    it("line numbers increment correctly across add/del/context mix", () => {
      const diff = `diff --git a/f.ts b/f.ts
--- a/f.ts
+++ b/f.ts
@@ -1,5 +1,6 @@
 a
+inserted
 b
-removed
+replaced
 c
 d
`;
      const [file] = parseDiff(diff);
      const ls = file.hunks[0].lines;
      // " a" → context oldNo=1 newNo=1
      assert.strictEqual(ls[0].oldNo, 1); assert.strictEqual(ls[0].newNo, 1);
      // "+inserted" → add oldNo=null newNo=2
      assert.strictEqual(ls[1].oldNo, null); assert.strictEqual(ls[1].newNo, 2);
      // " b" → context oldNo=2 newNo=3
      assert.strictEqual(ls[2].oldNo, 2); assert.strictEqual(ls[2].newNo, 3);
      // "-removed" → del oldNo=3 newNo=null
      assert.strictEqual(ls[3].oldNo, 3); assert.strictEqual(ls[3].newNo, null);
      // "+replaced" → add oldNo=null newNo=4
      assert.strictEqual(ls[4].oldNo, null); assert.strictEqual(ls[4].newNo, 4);
      // " c" → context oldNo=4 newNo=5
      assert.strictEqual(ls[5].oldNo, 4); assert.strictEqual(ls[5].newNo, 5);
    });
  });

  describe("text content", () => {
    it("strips leading +/-/space from text", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      const hunk = file.hunks[0];
      assert.strictEqual(hunk.lines[0].text, "line1");
      const del = hunk.lines.find((l) => l.type === "del");
      assert.strictEqual(del?.text, "old");
      const add = hunk.lines.find((l) => l.type === "add");
      assert.strictEqual(add?.text, "new");
    });

    it('ignores "no newline at end of file" marker', () => {
      const diff = `diff --git a/f.ts b/f.ts
--- a/f.ts
+++ b/f.ts
@@ -1 +1 @@
-old
\\ No newline at end of file
+new
\\ No newline at end of file
`;
      const [file] = parseDiff(diff);
      assert.strictEqual(file.hunks[0].lines.length, 2);
      assert.strictEqual(file.additions, 1);
      assert.strictEqual(file.deletions, 1);
    });
  });

  describe("hunk header", () => {
    it("preserves the full @@ ... @@ header string", () => {
      const [file] = parseDiff(MODIFIED_DIFF);
      assert.strictEqual(file.hunks[0].header, "@@ -1,4 +1,4 @@");
    });

    it("header includes optional trailing context label", () => {
      const diff = `diff --git a/f.ts b/f.ts
--- a/f.ts
+++ b/f.ts
@@ -10,3 +10,3 @@ function foo() {
 a
-b
+B
 c
`;
      const [file] = parseDiff(diff);
      assert.ok(file.hunks[0].header.includes("@@ -10,3 +10,3 @@"));
    });
  });

  describe("graceful degradation", () => {
    it("returns [] when input has no diff --git markers", () => {
      assert.deepStrictEqual(parseDiff("some random text\nno diff here\n"), []);
    });

    it("handles a diff section with no hunks (metadata-only)", () => {
      const diff = `diff --git a/f.ts b/f.ts
similarity index 100%
rename from f.ts
rename to g.ts
`;
      const files = parseDiff(diff);
      assert.strictEqual(files.length, 1);
      assert.strictEqual(files[0].status, "renamed");
      assert.strictEqual(files[0].hunks.length, 0);
    });
  });
});
