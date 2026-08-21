// yamdview E2E driver: drives headless Chrome via puppeteer-core to verify
// scroll preservation across live SSE block patches.
//
// Usage: NODE_PATH=<dir with puppeteer-core> node driver.js <config.json>
//
// Config JSON:
//   { "url": "...", "executablePath": "...", "minBlocks": 121 }
//
// Protocol (JSON lines on stdout):
//   {"phase":"ready",...}  page loaded, scrolled to middle, reference block tagged
//   {"phase":"done",...}   measurements taken after the patch was applied
//   {"phase":"error",...}  fatal driver failure
//
// The driver blocks on stdin between the ready and done phases; the parent
// Go test rewrites the Markdown file and broadcasts SSE patches, then writes
// "go" to stdin to let the driver take the after measurements.

"use strict";

const fs = require("fs");
const puppeteer = require("puppeteer-core");

const cfg = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));

const SENTINEL = "yamdview-e2e-scroll";
const POLL_INTERVAL_MS = 100;
const PATCH_TIMEOUT_MS = 20000;

function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

function fail(message) {
  emit({ phase: "error", message: message });
  process.exitCode = 1;
}

function sleep(ms) {
  return new Promise(function (resolve) {
    setTimeout(resolve, ms);
  });
}

/**
 * Block until the parent writes "go" on stdin. Rejects if stdin closes
 * first, so an aborted test never leaves the driver hanging.
 */
function waitForGo() {
  return new Promise(function (resolve, reject) {
    let buf = "";
    function onData(d) {
      buf += d.toString();
      if (buf.indexOf("go") !== -1) {
        cleanup();
        resolve();
      }
    }
    function onEnd() {
      cleanup();
      reject(new Error("parent closed stdin before signalling go"));
    }
    function cleanup() {
      process.stdin.removeListener("data", onData);
      process.stdin.removeListener("end", onEnd);
    }
    process.stdin.on("data", onData);
    process.stdin.on("end", onEnd);
  });
}

(async function main() {
  let browser;
  try {
    browser = await puppeteer.launch({
      executablePath: cfg.executablePath,
      args: [
        "--headless=new",
        "--no-sandbox",
        "--disable-gpu",
        "--disable-dev-shm-usage",
      ],
    });
  } catch (err) {
    fail("launch chrome: " + err.message);
    return;
  }

  try {
    const page = await browser.newPage();

    // Client-side fallback warnings ("falling back to snapshot reset")
    // are strong evidence a full reset happened; collect them.
    const consoleWarnings = [];
    page.on("console", function (msg) {
      if (msg.type() === "warning" || msg.type() === "error") {
        consoleWarnings.push(msg.text());
      }
    });

    await page.setViewport({ width: 900, height: 700 });
    await page.goto(cfg.url, { waitUntil: "networkidle2", timeout: 30000 });
    await page.waitForFunction(
      function (min) {
        return document.querySelectorAll(".md-block").length >= min;
      },
      { timeout: 15000 },
      cfg.minBlocks
    );

    // Scroll into the middle of the document. restoreScrollAnchor keeps
    // readers at the very top anchored by design, so the interesting case
    // requires being scrolled away from the top.
    await page.evaluate(function () {
      window.scrollTo(
        0,
        Math.floor(document.documentElement.scrollHeight / 2)
      );
    });
    await sleep(150);

    // Pick a fully visible reference block and tag it with a JS expando.
    // A full reset replaces #document's innerHTML, destroying every
    // original node; if the tagged node is still reachable afterwards,
    // no reset occurred.
    const ref = await page.evaluate(function () {
      var vp = window.innerHeight;
      var blocks = Array.prototype.slice.call(
        document.querySelectorAll(".md-block")
      );
      var el = null;
      for (var i = 0; i < blocks.length; i++) {
        var r = blocks[i].getBoundingClientRect();
        if (r.top >= 0 && r.bottom <= vp && r.height > 0) {
          el = blocks[i];
          break;
        }
      }
      if (!el) {
        el = blocks[Math.floor(blocks.length / 2)];
      }
      el.__e2eSentinel = "yamdview-e2e-scroll";
      return {
        id: el.id,
        top: el.getBoundingClientRect().top,
        totalBlocks: blocks.length,
      };
    });

    emit({
      phase: "ready",
      refId: ref.id,
      topBefore: ref.top,
      blocksBefore: ref.totalBlocks,
    });

    await waitForGo();

    // Poll until the inserted blocks appear in the DOM.
    const deadline = Date.now() + PATCH_TIMEOUT_MS;
    let after = null;
    while (Date.now() < deadline) {
      after = await page.evaluate(function (refId) {
        var el = document.getElementById(refId);
        return {
          present: !!el,
          top: el ? el.getBoundingClientRect().top : null,
          sentinelKept: !!(el && el.__e2eSentinel === "yamdview-e2e-scroll"),
          totalBlocks: document.querySelectorAll(".md-block").length,
        };
      }, ref.id);
      if (after.present && after.totalBlocks > ref.totalBlocks) {
        break;
      }
      await sleep(POLL_INTERVAL_MS);
    }

    if (!after || !after.present || after.totalBlocks <= ref.totalBlocks) {
      fail("patch never applied: inserted blocks did not appear");
      return;
    }

    emit({
      phase: "done",
      topAfter: after.top,
      sentinelKept: after.sentinelKept,
      blocksAfter: after.totalBlocks,
      consoleWarnings: consoleWarnings,
    });
  } catch (err) {
    fail(err.message);
  } finally {
    if (browser) {
      await browser.close();
    }
  }
})();
