import assert from "node:assert/strict";
import { test } from "node:test";
import { fixture } from "./output-fixture.ts";
import type { ViewSnapshot } from "../../src/view.ts";

test(
  "native durable controls survive a server restart and reject stale clients",
  { timeout: 60000 },
  async (t) => {
    const native = await fixture(t);
    const run = await native.run("OUTPUT_CASE READ");
    const snapshot = async () =>
      (await native.rpc("snapshot", {
        sessionID: run.sessionID,
      })) as ViewSnapshot;
    const before = await snapshot();
    const input = {
      instanceID: before.instanceID,
      expectedRevision: before.controls.revision,
      apps: { check: true },
      waits: { redact: "done" },
      goal: "quality",
    };
    assert.deepEqual(await native.rpc("controls", input), {
      saved: true,
      revision: 1,
    });
    assert.deepEqual(await native.rpc("controls", input), {
      saved: false,
      reason: "stale",
    });
    await native.restart();
    const after = await snapshot();
    assert.notEqual(after.instanceID, before.instanceID);
    assert.equal(after.apps.check, true);
    assert.equal(after.waits.redact, "done");
    assert.equal(after.controls.goal, "quality");
    assert.equal(after.controls.revision, 1);
    assert.equal(after.controls.persistence, "durable");
    assert.ok(before.decisions.length);
    assert.deepEqual(
      after.decisions,
      before.decisions,
      "decisions are kept in plugin storage across a restart",
    );
    assert.deepEqual(
      await native.rpc("controls", { ...input, expectedRevision: 1 }),
      { saved: false, reason: "stale" },
    );
    assert.deepEqual(
      await native.rpc("controls", {
        ...input,
        instanceID: after.instanceID,
        expectedRevision: 1,
        apps: { check: false },
      }),
      { saved: true, revision: 2 },
    );
    assert.equal((await snapshot()).apps.check, false);
    assert.deepEqual(
      await native.rpc("controls", {
        instanceID: after.instanceID,
        expectedRevision: 2,
        apps: { check: !before.apps.check },
      }),
      { saved: true, revision: 3 },
    );
    // No listener answers here (404), so following hands the app back to the plugin's own default.
    assert.deepEqual(
      await native.rpc("controls", {
        instanceID: after.instanceID,
        expectedRevision: 3,
        follow: true,
      }),
      { saved: true, revision: 4 },
    );
    assert.equal((await snapshot()).apps.check, before.apps.check);
  },
);

test(
  "a Router listener's profile sets the router's goal and turns, says so once, and /vsr-turns changes them",
  { timeout: 120000 },
  async (t) => {
    const native = await fixture(t, false, {
      redact: false,
      router_goal: "save",
      router_turns: true,
    });
    const run = await native.run("OUTPUT_CASE READ");
    const snapshot = async () =>
      (await native.rpc("snapshot", {
        sessionID: run.sessionID,
      })) as ViewSnapshot;
    const before = await snapshot();
    assert.equal(before.apps.redact, false);
    assert.equal(before.controls.goal, "save");
    assert.equal(before.controls.turns, true);
    assert.equal(
      before.controls.note,
      "vSR follows the listener: redact off, goal save, turns on",
    );
    const screen = await native.terminal(
      run.sessionID,
      [
        { at: 2500, keys: "/vsr-turns" },
        { at: 3000, keys: "\r" }, // Completes the command, which takes a word,
        { at: 3400, keys: "\r" }, // and without one says where your turns run.
        { at: 5000, keys: "/vsr-turns off" },
        { at: 5500, keys: "\r" },
        { at: 7000, keys: "/vsr" },
        { at: 7500, keys: "\r" },
      ],
      9.5,
    );
    assert.ok(
      // The toast wraps; the RPC above holds its whole text.
      screen.text.includes("vSR follows the listener: redact off,") &&
        screen.text.includes(
          "vSR: the router picks the model of your turns.",
        ) &&
        screen.text.includes("vSR: your turns run on the model you picked.") &&
        /your turns +yours +routed/.test(screen.text),
      screen.text.slice(-6000),
    );
    const after = await snapshot();
    assert.equal(after.controls.turns, false, "the person's change wins");
    // Following the listener again hands the turns back.
    assert.deepEqual(
      await native.rpc("controls", {
        instanceID: after.instanceID,
        expectedRevision: after.controls.revision,
        follow: true,
      }),
      { saved: true, revision: after.controls.revision + 1 },
    );
    assert.equal((await snapshot()).controls.turns, true);
  },
);
