// yamdview viewer script
// Phase 2: full-page live reload over Server-Sent Events.
// Adds a subtle fade-in on content swaps.

(function () {
  "use strict";

  /**
   * Replace the inner HTML of the document element with a brief fade transition.
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

  // Gentle initial reveal
  var doc = document.getElementById("document");
  if (doc) {
    doc.style.opacity = "0";
    doc.style.transition = "opacity 300ms ease";
    window.addEventListener("load", function () {
      doc.style.opacity = "1";
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", connectEvents);
  } else {
    connectEvents();
  }
})();
