// yamdview viewer script
// Phase 3+: block-level live patching over SSE with KaTeX math rendering.

(function () {
  "use strict";

  // ── KaTeX rendering ────────────────────────────────────

  /**
   * Render all un-rendered math elements inside the given root element.
   * Returns an array of error objects for any failures.
   */
  function renderMath(root) {
    if (typeof katex === "undefined") {
      console.warn("yamdview: KaTeX not loaded");
      return [];
    }

    var errors = [];
    var elements = root.querySelectorAll(".math[data-tex]:not([data-math-rendered])");

    for (var i = 0; i < elements.length; i++) {
      var el = elements[i];
      var tex = el.getAttribute("data-tex");
      var isDisplay = el.classList.contains("math-display");

      try {
        katex.render(tex, el, {
          displayMode: isDisplay,
          throwOnError: true,
          strict: "warn",
        });
        el.setAttribute("data-math-rendered", "true");
      } catch (err) {
        // Show the raw TeX as fallback with a warning badge.
        el.textContent = tex;
        el.setAttribute("data-math-rendered", "error");
        el.setAttribute("data-math-error", err.message);
        var block = el.closest ? el.closest(".md-block") : null;
        errors.push({
          block_id: block ? block.id : (el.id || ""),
          kind: "math",
          message: err.message,
          tex: tex,
        });
      }
    }

    return errors;
  }

  /**
   * Report client-side render errors to the server.
   */
  function reportErrors(errors) {
    if (!errors || errors.length === 0) return;

    fetch("/client-error", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(errors),
    }).catch(function (err) {
      console.warn("yamdview: failed to report errors", err);
    });
  }

  // ── Document update ────────────────────────────────────

  function getDocumentElement() {
    return document.getElementById("document");
  }

  function elementFromHTML(html) {
    var template = document.createElement("template");
    template.innerHTML = html.trim();
    return template.content.firstElementChild;
  }

  function renderAndReport(root) {
    var errors = renderMath(root);
    reportErrors(errors);
  }

  // ── Update highlighting ────────────────────────────────

  var UPDATE_CLASS = "md-block--updated";
  var UPDATE_ANIMATION = "md-block-fade";
  var UPDATE_TIMEOUT_MS = 2200;

  /**
   * Briefly highlight a freshly inserted or replaced block: the class
   * drives a CSS fade-out animation and is removed on animationend,
   * with a timeout fallback in case the event never fires. Skipped
   * entirely when the user prefers reduced motion. The class sits on
   * the wrapper section only; blockFingerprint reads textContent, so
   * it is unaffected either way.
   */
  function markBlockUpdated(el) {
    if (!el || typeof el.classList.add !== "function") {
      return;
    }
    var reduced = window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) {
      return;
    }

    el.classList.add(UPDATE_CLASS);

    function remove() {
      el.classList.remove(UPDATE_CLASS);
      el.removeEventListener("animationend", onEnd);
      clearTimeout(timer);
    }

    function onEnd(evt) {
      // Ignore animations bubbling up from descendants.
      if (evt.animationName !== UPDATE_ANIMATION) {
        return;
      }
      remove();
    }

    var timer = setTimeout(remove, UPDATE_TIMEOUT_MS);
    el.addEventListener("animationend", onEnd);
  }

  // ── Scroll preservation ────────────────────────────────

  /**
   * Build a text fingerprint for a block that stays stable across KaTeX
   * rendering: math spans are collapsed back to their raw TeX source before
   * the text is read, then whitespace is normalized.
   */
  function blockFingerprint(block) {
    var clone = block.cloneNode(true);
    var maths = clone.querySelectorAll(".math[data-tex]");
    for (var i = 0; i < maths.length; i++) {
      maths[i].textContent = maths[i].getAttribute("data-tex");
    }
    return (clone.textContent || "").replace(/\s+/g, " ").trim();
  }

  /**
   * Capture a visual anchor near the top of the viewport: the first block
   * element intersecting the viewport, its offset from the viewport top, its
   * id, and a text fingerprint used as an identity fallback.
   */
  function captureScrollAnchor() {
    var blocks = document.querySelectorAll(".md-block");
    for (var i = 0; i < blocks.length; i++) {
      var rect = blocks[i].getBoundingClientRect();
      if (rect.bottom > 0 && rect.top < window.innerHeight) {
        return {
          id: blocks[i].id || "",
          offset: rect.top,
          fingerprint: blockFingerprint(blocks[i]),
        };
      }
    }
    return null;
  }

  /**
   * Keep the anchored block visually stable after a batch of patch ops:
   * locate it again by id (falling back to a fingerprint match among the
   * remaining or newly inserted blocks) and scroll by whatever amount the
   * patches shifted it. If the anchor block is gone and cannot be matched,
   * leave the scroll position untouched rather than guessing.
   */
  function restoreScrollAnchor(anchor) {
    if (!anchor) {
      return;
    }

    var el = anchor.id ? document.getElementById(anchor.id) : null;
    if (!el && anchor.fingerprint) {
      var blocks = document.querySelectorAll(".md-block");
      for (var i = 0; i < blocks.length; i++) {
        if (blockFingerprint(blocks[i]) === anchor.fingerprint) {
          el = blocks[i];
          break;
        }
      }
    }
    if (!el) {
      return;
    }

    // A reader still at the very top of the document stays there.
    if ((window.pageYOffset || 0) === 0) {
      return;
    }

    var delta = el.getBoundingClientRect().top - anchor.offset;
    if (delta !== 0) {
      window.scrollBy(0, delta);
    }
  }

  /**
   * Replace the inner HTML of the document element with a brief fade transition,
   * then render any math in the new content. The reader's scroll position is
   * captured up front and restored once the new HTML is in place.
   */
  function replaceDocument(html) {
    var documentEl = getDocumentElement();
    if (!documentEl) {
      return;
    }

    var scrollTop = window.pageYOffset ||
      document.documentElement.scrollTop ||
      document.body.scrollTop || 0;

    documentEl.style.opacity = "0";
    documentEl.style.transition = "opacity 120ms ease";

    // Wait one frame for the opacity transition to start, then swap.
    requestAnimationFrame(function () {
      requestAnimationFrame(function () {
        documentEl.innerHTML = html;
        documentEl.style.opacity = "1";
        window.scrollTo(0, scrollTop);
        renderAndReport(documentEl);
      });
    });
  }

  function refreshFromSnapshot(reason) {
    console.warn("yamdview: falling back to snapshot reset", reason || "");
    fetch("/snapshot")
      .then(function (response) { return response.text(); })
      .then(replaceDocument)
      .catch(function (err) {
        console.error("yamdview: failed to fetch snapshot", err);
      });
  }

  function applyPatchOp(op) {
    var documentEl = getDocumentElement();
    if (!documentEl || !op || typeof op.op !== "string") {
      return false;
    }

    if (op.op === "replace") {
      var target = document.getElementById(op.id);
      var replacement = elementFromHTML(op.html || "");
      if (!target || !replacement) return false;
      target.replaceWith(replacement);
      markBlockUpdated(replacement);
      renderAndReport(replacement);
      return true;
    }

    if (op.op === "insert_after") {
      var after = document.getElementById(op.after);
      var afterElement = elementFromHTML(op.html || "");
      if (!after || !afterElement) return false;
      after.after(afterElement);
      markBlockUpdated(afterElement);
      renderAndReport(afterElement);
      return true;
    }

    if (op.op === "insert_before") {
      var before = document.getElementById(op.before);
      var beforeElement = elementFromHTML(op.html || "");
      if (!before || !beforeElement) return false;
      before.before(beforeElement);
      markBlockUpdated(beforeElement);
      renderAndReport(beforeElement);
      return true;
    }

    if (op.op === "delete") {
      var doomed = document.getElementById(op.id);
      if (!doomed) return false;
      doomed.remove();
      return true;
    }

    if (op.op === "reset" && typeof op.html === "string") {
      replaceDocument(op.html);
      return true;
    }

    return false;
  }

  function applyPatches(ops) {
    if (!Array.isArray(ops)) {
      return false;
    }
    var anchor = captureScrollAnchor();
    for (var i = 0; i < ops.length; i++) {
      if (!applyPatchOp(ops[i])) {
        return false;
      }
    }
    restoreScrollAnchor(anchor);
    return true;
  }

  /**
   * Handle SSE reset events from the server.
   */
  function handleReset(event) {
    try {
      var payload = JSON.parse(event.data);
      if (payload.op === "reset" && typeof payload.html === "string") {
        replaceDocument(payload.html);
      }
    } catch (err) {
      console.error("yamdview: invalid reset event", err);
    }
  }

  /**
   * Handle SSE patch events from the server.
   */
  function handlePatch(event) {
    try {
      var payload = JSON.parse(event.data);
      if (!applyPatches(payload.ops)) {
        refreshFromSnapshot("patch target missing or invalid");
      }
    } catch (err) {
      console.error("yamdview: invalid patch event", err);
      refreshFromSnapshot("invalid patch event");
    }
  }

  /**
   * Connect to the SSE event stream for live reloads.
   */
  function connectEvents() {
    if (!window.EventSource) {
      console.warn("yamdview: EventSource is not supported by this browser");
      return;
    }

    var source = new EventSource("/events");
    source.addEventListener("reset", handleReset);
    source.addEventListener("patch", handlePatch);
  }

  // ── Review session ────────────────────────────────────

  /** Wire the optional review banner without affecting ordinary viewers. */
  function initReviewSession() {
    var panel = document.getElementById("review-session");
    if (!panel) return;

    var token = panel.getAttribute("data-session-token") || "";
    var choices = panel.querySelectorAll("[data-verdict]");
    var submit = document.getElementById("review-submit");
    var summary = document.getElementById("review-summary");
    var status = document.getElementById("review-status");
    var verdict = "";

    // Reformulation preview state ("ask" mode only). Kept separate from
    // the plain submission path so it can never block raw feedback.
    var reformulate = null;

    function currentVerdict() {
      return verdict;
    }

    function setStatus(message, state) {
      status.textContent = message;
      status.setAttribute("data-state", state || "");
    }

    for (var i = 0; i < choices.length; i++) {
      choices[i].addEventListener("click", function () {
        verdict = this.getAttribute("data-verdict") || "";
        for (var j = 0; j < choices.length; j++) {
          choices[j].setAttribute("aria-pressed", choices[j] === this ? "true" : "false");
        }
        setStatus("", "");
      });
    }

    submit.addEventListener("click", function () {
      if (!verdict) {
        setStatus("Choose a verdict before submitting.", "error");
        return;
      }

      submit.disabled = true;
      for (var k = 0; k < choices.length; k++) choices[k].disabled = true;
      setStatus("Submitting…", "");

      fetch("/api/session/submit", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Yamdview-Token": token,
        },
        body: JSON.stringify({
          verdict: verdict,
          summary: summary.value,
          // Falls back to raw feedback when no successful preview exists.
          use_reformulated: reformulate ? reformulate.shouldUse() : false,
        }),
      })
        .then(function (response) {
          return response.text().then(function (body) {
            var payload = {};
            try { payload = body ? JSON.parse(body) : {}; } catch (_) {}
            if (!response.ok) throw new Error(payload.error || body || "Submission failed");
            return payload;
          });
        })
        .then(function () {
          panel.setAttribute("data-session-state", "submitted");
          setStatus("Review submitted. Thank you.", "success");
        })
        .catch(function (err) {
          submit.disabled = false;
          for (var m = 0; m < choices.length; m++) choices[m].disabled = false;
          setStatus(err.message || "Could not submit review.", "error");
        });
    });

    reformulate = initReformulate(panel, token, currentVerdict, setStatus);
  }

  /**
   * Wire the reformulation preview UI. Only active when the page exposes
   * respond metadata with mode "ask"; returns null otherwise so callers
   * know the capability is absent.
   */
  function initReformulate(panel, token, getVerdict, setStatus) {
    if ((panel.getAttribute("data-respond-mode") || "") !== "ask") {
      return null;
    }

    var run = document.getElementById("review-reformulate-run");
    var modelSelect = document.getElementById("review-reformulate-model");
    var statusLine = document.getElementById("review-reformulate-status");
    var diagnostics = document.getElementById("review-reformulate-diagnostics");
    var results = document.getElementById("review-reformulate-results");
    var rawText = document.getElementById("review-reformulate-raw-text");
    var refText = document.getElementById("review-reformulated-text");
    var choiceGroup = document.getElementById("review-reformulate-choice");
    var options = choiceGroup ? choiceGroup.querySelectorAll("[data-use]") : [];
    var summary = document.getElementById("review-summary");
    var state = { applied: false, requested: false };

    if (!run || !modelSelect || !results || !choiceGroup) {
      return null;
    }

    function setReformulateStatus(message, kind) {
      statusLine.textContent = message;
      statusLine.setAttribute("data-state", kind || "");
    }

    // Populate the model picker from the comma-joined data attribute,
    // falling back to the single configured model.
    var preferred = panel.getAttribute("data-respond-model") || "";
    var modelsAttr = panel.getAttribute("data-respond-models") || "";
    var models = modelsAttr ? modelsAttr.split(",") : [];
    if (models.length === 0 && preferred) models = [preferred];
    modelSelect.textContent = "";
    for (var i = 0; i < models.length; i++) {
      var name = models[i].replace(/^\s+/, "").replace(/\s+$/, "");
      if (!name) continue;
      var option = document.createElement("option");
      option.value = name;
      option.textContent = name;
      if (name === preferred) option.selected = true;
      modelSelect.appendChild(option);
    }

    function selectOption(use) {
      state.requested = use;
      for (var j = 0; j < options.length; j++) {
        var active = (options[j].getAttribute("data-use") === "true") === use;
        options[j].setAttribute("aria-pressed", active ? "true" : "false");
      }
    }

    for (var k = 0; k < options.length; k++) {
      options[k].addEventListener("click", function () {
        var use = this.getAttribute("data-use") === "true";
        if (use && !state.applied) {
          setReformulateStatus("No reformulated preview yet — run a draft first.", "error");
          return;
        }
        selectOption(use);
        setReformulateStatus("", "");
      });
    }

    function renderDiagnostics(entries) {
      if (!diagnostics) return;
      while (diagnostics.firstChild) {
        diagnostics.removeChild(diagnostics.firstChild);
      }
      if (!entries || typeof entries.length !== "number" || entries.length === 0) {
        diagnostics.hidden = true;
        return;
      }
      for (var m = 0; m < entries.length; m++) {
        var entry = entries[m] || {};
        var item = document.createElement("li");
        item.setAttribute("data-severity", entry.severity || "");
        item.textContent = entry.message || entry.code || "issue reported";
        diagnostics.appendChild(item);
      }
      diagnostics.hidden = false;
    }

    function rawFeedbackText() {
      var parts = [];
      var chosen = getVerdict();
      if (chosen) parts.push("Verdict: " + chosen);
      if (summary.value.replace(/\s+/g, "") !== "") parts.push(summary.value);
      return parts.join("\n\n") || "(no feedback entered)";
    }

    run.addEventListener("click", function () {
      run.disabled = true;
      setReformulateStatus("Drafting consolidated instruction…", "");

      fetch("/api/session/reformulate", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Yamdview-Token": token,
        },
        body: JSON.stringify({ summary: summary.value, model: modelSelect.value }),
      })
        .then(function (response) {
          return response.text().then(function (body) {
            var payload = {};
            try { payload = body ? JSON.parse(body) : {}; } catch (_) {}
            if (!response.ok) throw new Error(payload.error || body || "Reformulation failed");
            return payload;
          });
        })
        .then(function (payload) {
          var applied = payload.applied === true;
          state.applied = applied;
          if (!applied) selectOption(false);
          rawText.textContent = rawFeedbackText();
          refText.textContent = applied && payload.reformulated
            ? payload.reformulated.text
            : "(reformulation unavailable — raw feedback will be used)";
          renderDiagnostics(payload.diagnostics);
          results.hidden = false;
          choiceGroup.hidden = false;
          setReformulateStatus(applied ? "Preview ready." : "No reformulation available.", "");
        })
        .catch(function (err) {
          setReformulateStatus(err.message || "Could not draft a reformulation.", "error");
        })
        .then(function () {
          run.disabled = false;
        });
    });

    return {
      /** True only when the user chose the reformulated text AND a preview exists. */
      shouldUse: function () {
        return state.requested && state.applied;
      },
      hasPreview: function () {
        return state.applied;
      },
    };
  }

  // ── Initialisation ─────────────────────────────────────

  // Render math on initial page load.
  function init() {
    var doc = document.getElementById("document");
    if (doc) {
      var errors = renderMath(doc);
      reportErrors(errors);
    }
    connectEvents();
    initReviewSession();
  }

  // Gentle initial reveal.
  var doc = document.getElementById("document");
  if (doc) {
    doc.style.opacity = "0";
    doc.style.transition = "opacity 300ms ease";
    window.addEventListener("load", function () {
      doc.style.opacity = "1";
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
