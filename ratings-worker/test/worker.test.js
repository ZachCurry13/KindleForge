import test from "node:test";
import assert from "node:assert/strict";
import { DatabaseSync } from "node:sqlite";
import { readFileSync } from "node:fs";
import worker from "../src/index.js";

// A tiny D1 stand-in on top of node:sqlite, same call shapes as Cloudflare D1.
function fakeD1() {
  const db = new DatabaseSync(":memory:");
  db.exec(readFileSync(new URL("../schema.sql", import.meta.url), "utf8"));
  class Stmt {
    constructor(sql) { this.sql = sql; this.args = []; }
    bind(...a) { this.args = a; return this; }
    async run() { const r = db.prepare(this.sql).run(...this.args); return { success: true, meta: { changes: Number(r.changes) } }; }
    async first() { return db.prepare(this.sql).get(...this.args) ?? null; }
    async all() { return { results: db.prepare(this.sql).all(...this.args) }; }
  }
  return { prepare: (sql) => new Stmt(sql), raw: db };
}

const INSTALL_A = "a".repeat(32);
const INSTALL_B = "b".repeat(32);
const INSTALL_C = "c".repeat(32);

function env(extra = {}) { return { DB: fakeD1(), MIN_VOTES: "3", ...extra }; }
function post(body, ip = "1.2.3.4") {
  return new Request("https://x/rate", { method: "POST", body: typeof body === "string" ? body : JSON.stringify(body), headers: { "CF-Connecting-IP": ip } });
}
async function agg(e) { return (await worker.fetch(new Request("https://x/aggregate"), e)).json(); }

test("health and unknown routes", async () => {
  const e = env();
  assert.equal(await (await worker.fetch(new Request("https://x/health"), e)).text(), "ok");
  assert.equal((await worker.fetch(new Request("https://x/nope"), e)).status, 404);
});

test("a vote is stored and aggregated", async () => {
  const e = env();
  for (const [inst, stars] of [[INSTALL_A, 5], [INSTALL_B, 4], [INSTALL_C, 4]]) {
    const r = await worker.fetch(post({ pkg: "hello", install: inst, stars }), e);
    assert.equal(r.status, 200);
  }
  const a = await agg(e);
  assert.deepEqual(a.packages.hello, { avg: 4.3, count: 3 });
  assert.equal(a.minVotes, 3);
});

test("voting again changes the vote instead of adding one", async () => {
  const e = env();
  await worker.fetch(post({ pkg: "hello", install: INSTALL_A, stars: 1 }), e);
  await worker.fetch(post({ pkg: "hello", install: INSTALL_A, stars: 5 }), e);
  assert.deepEqual((await agg(e)).packages.hello, { avg: 5, count: 1 });
});

test("bad input is rejected", async () => {
  const e = env();
  const bad = [
    { pkg: "hello", install: INSTALL_A, stars: 0 },
    { pkg: "hello", install: INSTALL_A, stars: 6 },
    { pkg: "hello", install: INSTALL_A, stars: 4.5 },
    { pkg: "hello", install: INSTALL_A, stars: "5" },
    { pkg: "../etc", install: INSTALL_A, stars: 5 },
    { pkg: "a b", install: INSTALL_A, stars: 5 },
    { pkg: "", install: INSTALL_A, stars: 5 },
    { pkg: "hello", install: "short", stars: 5 },
    { pkg: "hello", install: "G".repeat(32), stars: 5 },
    { pkg: "hello", stars: 5 },
    null,
  ];
  for (const b of bad) assert.equal((await worker.fetch(post(b), e)).status, 400, JSON.stringify(b));
  assert.equal((await worker.fetch(post("not json"), e)).status, 400);
  assert.equal((await worker.fetch(post("x".repeat(2000)), e)).status, 413);
  assert.deepEqual((await agg(e)).packages, {});
});

test("rate limit: 40 votes an hour per caller, others unaffected", async () => {
  const e = env();
  let last;
  for (let i = 0; i < 41; i++) {
    const inst = i.toString(16).padStart(32, "0");
    last = await worker.fetch(post({ pkg: "hello", install: inst, stars: 5 }, "9.9.9.9"), e);
  }
  assert.equal(last.status, 429);
  assert.equal(last.headers.get("retry-after"), "3600");
  const other = await worker.fetch(post({ pkg: "hello", install: INSTALL_A, stars: 5 }, "8.8.8.8"), e);
  assert.equal(other.status, 200);
  assert.equal((await agg(e)).packages.hello.count, 41); // the 41st was refused
});

test("no address is stored anywhere", async () => {
  const e = env();
  await worker.fetch(post({ pkg: "hello", install: INSTALL_A, stars: 5 }, "203.0.113.77"), e);
  const dump = JSON.stringify([
    e.DB.raw.prepare("SELECT * FROM votes").all(),
    e.DB.raw.prepare("SELECT * FROM ratelimit").all(),
  ]);
  assert.ok(!dump.includes("203.0.113.77"));
});

test("package cap stops junk ids", async () => {
  const e = env();
  for (let i = 0; i < 500; i++) e.DB.raw.prepare("INSERT INTO votes VALUES (?, ?, 5, 'x')").run("pkg" + i, INSTALL_A);
  assert.equal((await worker.fetch(post({ pkg: "brandnew", install: INSTALL_B, stars: 5 }), e)).status, 400);
  assert.equal((await worker.fetch(post({ pkg: "pkg3", install: INSTALL_B, stars: 5 }), e)).status, 200); // known pkg still OK
});

test("PKG_ALLOW restricts to a list", async () => {
  const e = env({ PKG_ALLOW: "hello, kwordle" });
  assert.equal((await worker.fetch(post({ pkg: "kwordle", install: INSTALL_A, stars: 3 }), e)).status, 200);
  assert.equal((await worker.fetch(post({ pkg: "evil", install: INSTALL_A, stars: 3 }), e)).status, 400);
});

test("admin delete needs the token and is invisible without one", async () => {
  const e = env({ ADMIN_TOKEN: "s3cret" });
  await worker.fetch(post({ pkg: "hello", install: INSTALL_A, stars: 1 }), e);
  const del = (hdr) => worker.fetch(new Request("https://x/admin/pkg/hello", { method: "DELETE", headers: hdr }), e);
  assert.equal((await del({})).status, 404);
  assert.equal((await del({ Authorization: "Bearer wrong" })).status, 404);
  const ok = await del({ Authorization: "Bearer s3cret" });
  assert.equal(ok.status, 200);
  assert.deepEqual((await agg(e)).packages, {});
  const noToken = env(); // ADMIN_TOKEN unset
  const r = await worker.fetch(new Request("https://x/admin/pkg/hello", { method: "DELETE", headers: { Authorization: "Bearer undefined" } }), noToken);
  assert.equal(r.status, 404);
});
