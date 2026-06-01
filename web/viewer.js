// yamdview viewer script
// Phase 4: full-page live reload over SSE with KaTeX math rendering.

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
        errors.push({
          block_id: el.id || "",
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

  /**
   * Replace the inner HTML of the document element with a brief fade transition,
   * then render any math in the new content.
   */
  function replaceDocument(html) {
    var documentEl = document.getElementById("document");
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
        var errors = renderMath(documentEl);
        reportErrors(errors);
      });
    });
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
   * Connect to the SSE event stream for live reloads.
   */
  function connectEvents() {
    if (!window.EventSource) {
      console.warn("yamdview: EventSource is not supported by this browser");
      return;
    }

    var source = new EventSource("/events");
    source.addEventListener("reset", handleReset);
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
