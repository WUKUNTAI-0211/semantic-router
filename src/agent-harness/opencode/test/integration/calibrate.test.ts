import assert from "node:assert/strict";
import { test } from "node:test";
import { fixture } from "./output-fixture.ts";
import type {
  CalibrationQuestion,
  CalibrationReport,
  ProbeResult,
  ViewSnapshot,
} from "../../src/view.ts";

test(
  "native decisions are logged for calibration across a restart, and /vsr-calibrate asks about them",
  { timeout: 90000 },
  async (t) => {
    const native = await fixture(t);
    const run = await native.run("OUTPUT_CASE READ");
    const snapshot = (await native.rpc("snapshot", {
      sessionID: run.sessionID,
    })) as ViewSnapshot;
    const shield = snapshot.decisions.find(
      (decision) => decision.app === "shield",
    );
    assert.match(shield?.subject ?? "", /^read .*fixture\.txt$/);
    assert.ok(
      !JSON.stringify(snapshot).includes("ignore previous"),
      "the subject names the source, never its content",
    );

    await native.restart();
    const report = (await native.rpc(
      "calibrationReport",
      {},
    )) as CalibrationReport;
    assert.equal(report.persistence, "durable");
    const counts = Object.fromEntries(
      report.apps.map((item) => [item.app, item.decisions]),
    );
    assert.ok(
      counts.shield! >= 1 && counts.redact! >= 1,
      JSON.stringify(counts),
    );
    const [question] = (await native.rpc("calibrationSample", {
      app: "shield",
    })) as CalibrationQuestion[];
    assert.equal(question?.acted, true);
    assert.deepEqual(question?.thresholds, [0.9]);
    const again = (await native.rpc("calibrationSample", {
      app: "shield",
      skip: [question!.id],
    })) as CalibrationQuestion[];
    assert.ok(
      !again.some((item) => item.id === question!.id),
      "a question asked this run is not asked again",
    );

    const second = await native.run("OUTPUT_CASE READ");
    const screen = await native.terminal(
      second.sessionID,
      // Opened well after the TUI started, as a person would, not in its first seconds.
      [
        { at: 12000, keys: "/vsr-calibrate" },
        { at: 12800, keys: "\r" },
        { at: 14000, keys: "Shield" },
        { at: 14600, keys: "\r" },
        { at: 16000, keys: "\r" }, // Yes, it should have warned.
        { at: 17500, keys: "\r" }, // And the next one, if any.
        { at: 19000, keys: "\r" },
        { at: 20500, keys: "\r" }, // Balanced.
      ],
      23,
    );
    assert.ok(
      screen.text.includes("vSR calibrate: which app?") &&
        screen.text.includes("Should vSR have warned the agent about") &&
        // The decision is shown in full under the question, not cut to one option line.
        screen.text.includes("it warned; score 0.99") &&
        screen.text.includes("Not enough answers yet"),
      screen.text.slice(-6000),
    );
    const after = (await native.rpc(
      "calibrationReport",
      {},
    )) as CalibrationReport;
    assert.ok(
      after.apps.find((item) => item.app === "shield")!.answered >= 1,
      "the answer is kept",
    );
    assert.equal(
      after.apps.find((item) => item.app === "shield")!.calibrated,
      false,
      "too few answers change nothing",
    );
    assert.equal(
      after.model,
      "fixture",
      "the decision model the runtime lists",
    );

    // The probe: the known cases of the apps asked for, judged at the thresholds they were measured at.
    const probed = (await native.rpc("calibrationProbe", {
      apps: ["shield"],
    })) as ProbeResult;
    assert.equal(probed.model, "fixture");
    assert.deepEqual(
      probed.rows.map((row) => [row.app, row.status === "error"]),
      [
        ["shield", false],
        ["shield", false],
        ["shield", false],
        ["shield", false],
      ],
    );
    const shown = await native.terminal(
      second.sessionID,
      [
        { at: 12000, keys: "/vsr-probe" },
        { at: 12800, keys: "\r" },
      ],
      18,
    );
    assert.ok(
      shown.text.includes("Decision model: fixture.") &&
        /Shield, 4 known cases: /.test(shown.text),
      shown.text.slice(-6000),
    );
  },
);

test(
  "/vsr-calibrate offers another round while an app is short of 8 answers, as Claude Code's does",
  { timeout: 300000 },
  async (t) => {
    const native = await fixture(t);
    let last = await native.run("OUTPUT_CASE READ");
    for (let i = 0; i < 5; i++) last = await native.run("OUTPUT_CASE READ");
    const screen = await native.terminal(
      last.sessionID,
      [
        { at: 12000, keys: "/vsr-calibrate" },
        { at: 12800, keys: "\r" },
        { at: 14000, keys: "Shield" },
        { at: 14600, keys: "\r" },
        { at: 16000, keys: "\r" }, // Both questions of the first round.
        { at: 17500, keys: "\r" },
        { at: 19000, keys: "\r" }, // Answer more questions.
        { at: 20500, keys: "\r" },
        { at: 22000, keys: "\r" },
      ],
      25,
    );
    assert.ok(
      screen.text.includes("vSR calibrate: 2 of 8 answers") &&
        screen.text.includes("Answer more questions") &&
        screen.text.includes("vSR calibrate: 4 of 8 answers"),
      screen.text.slice(-6000),
    );
    const after = (await native.rpc(
      "calibrationReport",
      {},
    )) as CalibrationReport;
    assert.equal(after.apps.find((item) => item.app === "shield")!.answered, 4);
  },
);
