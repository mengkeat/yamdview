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

  /**
   * Replace the inner HTML of the document element with a brief fade transition,
   * then render any math in the new content.
   */
  function replaceDocument(html) {
    var documentEl = getDocumentElement();
    if (!documentEl) {
      return;
    }

    documentEl.style.opacity = "0";
    documentEl.style.transition = "opacity 120ms ease";

    // Wait one frame for the opacity transition to start, then swap.
    requestAnimationFrame(function () {
      requestAnimationFrame(function () {
        documentEl.innerHTML = html;
        documentEl.style.opacity = "1";
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
      renderAndReport(replacement);
      return true;
    }

    if (op.op === "insert_after") {
      var after = document.getElementById(op.after);
      var afterElement = elementFromHTML(op.html || "");
      if (!after || !afterElement) return false;
      after.after(afterElement);
      renderAndReport(afterElement);
      return true;
    }

    if (op.op === "insert_before") {
      var before = document.getElementById(op.before);
      var beforeElement = elementFromHTML(op.html || "");
      if (!before || !beforeElement) return false;
      before.before(beforeElement);
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
    for (var i = 0; i < ops.length; i++) {
      if (!applyPatchOp(ops[i])) {
        return false;
      }
    }
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

  // ── Initialisation ─────────────────────────────────────

  // Render math on initial page load.
  function init() {
    var doc = document.getElementById("document");
    if (doc) {
      var errors = renderMath(doc);
      reportErrors(errors);
    }
    connectEvents();
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
