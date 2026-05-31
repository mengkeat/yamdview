// yamdview viewer script
// Phase 2: full-page live reload over Server-Sent Events.

(function () {
  "use strict";

  function replaceDocument(html) {
    var documentEl = document.getElementById("document");
    if (!documentEl) {
      return;
    }
    documentEl.innerHTML = html;
  }

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

  function connectEvents() {
    if (!window.EventSource) {
      console.warn("yamdview: EventSource is not supported by this browser");
      return;
    }

    var source = new EventSource("/events");
    source.addEventListener("reset", handleReset);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", connectEvents);
  } else {
    connectEvents();
  }
})();
