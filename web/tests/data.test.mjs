import test from "node:test";
import assert from "node:assert/strict";
import { flattenSpans, comparable, duration } from "../src/data.js";
test("trace tree orders descendants and collapse hides an entire subtree", () => {
  const spans = [
    { id: "grand", parent_id: "child" },
    { id: "root" },
    { id: "child", parent_id: "root" },
    { id: "orphan", parent_id: "missing" },
  ];
  assert.deepEqual(
    flattenSpans(spans).map((s) => [s.id, s.depth]),
    [
      ["root", 0],
      ["child", 1],
      ["grand", 2],
      ["orphan", 0],
    ],
  );
  assert.deepEqual(
    flattenSpans(spans, new Set(["root"])).map((s) => s.id),
    ["root", "orphan"],
  );
});
test("trace tree tolerates cycles and 20,000 deeply nested spans without recursion overflow", () => {
  assert.equal(
    flattenSpans([
      { id: "a", parent_id: "b" },
      { id: "b", parent_id: "a" },
    ]).length,
    2,
  );
  const spans = Array.from({ length: 20000 }, (_, i) => ({
    id: String(i),
    parent_id: i ? String(i - 1) : undefined,
  }));
  const before = performance.now();
  const rows = flattenSpans(spans);
  assert.equal(rows.length, 20000);
  assert.equal(rows.at(-1).depth, 19999);
  assert.ok(performance.now() - before < 1500);
  assert.equal(flattenSpans(spans, new Set(["0"])).length, 1);
});
test("benchmark compatibility ignores JSON key order but rejects different workload and phases", () => {
  const a = {
    kind: "live",
    workload_sha256: "abc",
    model: "m",
    options: { requests: 3, duration: 0 },
    phases: [{ workers: 1 }, { workers: 4 }],
  };
  assert.equal(
    comparable(a, {
      ...a,
      options: { duration: 0, requests: 3 },
      phases: [{ workers: 4 }, { workers: 1 }],
    }),
    true,
  );
  assert.equal(comparable(a, { ...a, workload_sha256: "other" }), false);
  assert.equal(comparable(a, { ...a, phases: [{ workers: 2 }] }), false);
  assert.equal(comparable(a, null), false);
  assert.equal(duration(null), "—");
  assert.equal(duration(0), "0.0ms");
});
