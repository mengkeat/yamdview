// yamdview review annotator
// Selection-aware comments for review pages only. This file is deliberately
// dependency-free and keeps annotation marks outside the rendered Markdown DOM.
(function () {
  "use strict";

  function ready(fn) {
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", fn);
    else fn();
  }

  function closestBlock(node) {
    while (node && node !== document) {
      if (node.nodeType === 1 && (" " + node.className + " ").indexOf(" md-block ") >= 0) return node;
      node = node.parentNode;
    }
    return null;
  }

  function textNodes(root) {
    var result = [], walker = document.createTreeWalker(root, 4, null, false), node;
    while ((node = walker.nextNode())) if (node.nodeValue) result.push(node);
    return result;
  }

  function normalise(value) {
    return (value || "").replace(/\s+/g, " ").replace(/^\s+|\s+$/g, "");
  }

  function quoteRanges(block, quote, prefix, suffix) {
    var nodes = textNodes(block), full = "", offsets = [], i;
    for (i = 0; i < nodes.length; i++) {
      offsets.push(full.length);
      full += nodes[i].nodeValue;
    }
    var at = full.indexOf(quote), fallback = at;
    while (at >= 0 && (prefix || suffix)) {
      var before = prefix ? full.slice(Math.max(0, at - prefix.length), at) : "";
      var after = suffix ? full.slice(at + quote.length, at + quote.length + suffix.length) : "";
      if ((!prefix || before === prefix) && (!suffix || after === suffix)) break;
      at = full.indexOf(quote, at + 1);
    }
    if (at < 0) at = fallback;
    if (at < 0) {
      var wanted = normalise(quote), compact = "", map = [], raw, compactAt, rawStart, rawEnd;
      for (i = 0; i < full.length; i++) {
        raw = full.charAt(i);
        if (/\s/.test(raw)) {
          if (!compact || compact.charAt(compact.length - 1) === " ") continue;
          raw = " ";
        }
        compact += raw;
        map.push(i);
      }
      while (compact.charAt(0) === " ") { compact = compact.slice(1); map.shift(); }
      while (compact.charAt(compact.length - 1) === " ") { compact = compact.slice(0, -1); map.pop(); }
      compactAt = compact.indexOf(wanted);
      if (compactAt < 0 || !wanted) return [];
      rawStart = map[compactAt];
      rawEnd = map[compactAt + wanted.length - 1] + 1;
      return makeRanges(nodes, offsets, rawStart, rawEnd);
    }
    return makeRanges(nodes, offsets, at, at + quote.length);
  }

  function makeRanges(nodes, offsets, start, end) {
    var ranges = [], i;
    for (i = 0; i < nodes.length; i++) {
      var nodeStart = offsets[i], nodeEnd = nodeStart + nodes[i].nodeValue.length;
      if (nodeEnd <= start || nodeStart >= end) continue;
      var range = document.createRange();
      range.setStart(nodes[i], Math.max(0, start - nodeStart));
      range.setEnd(nodes[i], Math.min(nodes[i].nodeValue.length, end - nodeStart));
      ranges.push(range);
    }
    return ranges;
  }

  function lineValue(block, name) {
    var value = parseInt(block.getAttribute(name) || "0", 10);
    return isNaN(value) ? 0 : value;
  }

  function textOffset(block, container, offset) {
    var before = document.createRange();
    before.selectNodeContents(block);
    before.setEnd(container, offset);
    return before.toString().length;
  }

  function selectionPieces(selectionRange) {
    var blocks = document.querySelectorAll(".md-block"), pieces = [], i;
    for (i = 0; i < blocks.length; i++) {
      var blockRange = document.createRange();
      blockRange.selectNodeContents(blocks[i]);
      if (selectionRange.compareBoundaryPoints(Range.END_TO_START, blockRange) <= 0 ||
          selectionRange.compareBoundaryPoints(Range.START_TO_END, blockRange) >= 0) continue;
      var part = document.createRange();
      if (selectionRange.compareBoundaryPoints(Range.START_TO_START, blockRange) > 0) {
        part.setStart(selectionRange.startContainer, selectionRange.startOffset);
      } else part.setStart(blockRange.startContainer, blockRange.startOffset);
      if (selectionRange.compareBoundaryPoints(Range.END_TO_END, blockRange) < 0) {
        part.setEnd(selectionRange.endContainer, selectionRange.endOffset);
      } else part.setEnd(blockRange.endContainer, blockRange.endOffset);
      var rawQuote = part.toString(), leading = rawQuote.search(/\S|$/);
      var quote = rawQuote.replace(/^\s+|\s+$/g, "");
      if (!quote) continue;
      var visible = block.textContent || "", quoteAt = visible.indexOf(quote);
      var selectedAt = textOffset(block, part.startContainer, part.startOffset) + leading;
      if (visible.slice(selectedAt, selectedAt + quote.length) === quote) quoteAt = selectedAt;
      else {
        var fromSelection = visible.indexOf(quote, selectedAt);
        if (fromSelection >= 0) quoteAt = fromSelection;
      }
      var prefix = quoteAt > 0 ? visible.slice(Math.max(0, quoteAt - 64), quoteAt) : "";
      var after = quoteAt >= 0 ? quoteAt + quote.length : visible.length;
      var suffix = visible.slice(after, after + 64);
      pieces.push({
        block_id: block.id,
        start_line: lineValue(block, "data-start-line"),
        end_line: lineValue(block, "data-end-line"),
        quote: quote,
        prefix: prefix,
        suffix: suffix
      });
    }
    return pieces;
  }

  function api(path, method, token, body) {
    var options = { method: method || "GET", headers: {} };
    if (body !== undefined) {
      options.headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(body);
    }
    if (token) options.headers["X-Yamdview-Token"] = token;
    return fetch(path, options).then(function (response) {
      return response.text().then(function (text) {
        var payload = {};
        try { payload = text ? JSON.parse(text) : {}; } catch (_) { payload = {}; }
        if (!response.ok) throw new Error(payload.error || text || "Request failed (" + response.status + ")");
        return payload;
      });
    });
  }

  function init() {
    var panel = document.getElementById("review-session");
    if (!panel) return;
    var token = panel.getAttribute("data-session-token") || "";
    var terminal = panel.getAttribute("data-session-state") !== "open";
    var annotations = [], draft = null, saveTimer = null, saving = false, saveAgain = false, documentReloadTimer = null;
    var add = document.createElement("button");
    add.type = "button";
    add.className = "review-annotator-add";
    add.setAttribute("aria-label", "Add comment to selected text");
    add.textContent = "Add comment";
    add.setAttribute("data-visible", "false");
    document.body.appendChild(add);

    var sidebar = document.createElement("aside");
    sidebar.className = "review-annotator-sidebar";
    sidebar.setAttribute("aria-label", "Document annotations");
    sidebar.innerHTML = '<div class="review-annotator-sidebar__header"><h2 class="review-annotator-sidebar__title">Annotations</h2><span class="review-annotator-sidebar__count"></span></div><div class="review-annotator-sidebar__list"></div>';
    document.body.appendChild(sidebar);
    var list = sidebar.querySelector(".review-annotator-sidebar__list");
    var count = sidebar.querySelector(".review-annotator-sidebar__count");

    var composer = document.createElement("section");
    composer.className = "review-annotator-composer";
    composer.setAttribute("role", "dialog");
    composer.setAttribute("aria-label", "Annotation composer");
    composer.setAttribute("aria-hidden", "true");
    composer.innerHTML = '<h2 class="review-annotator-composer__title">Add annotation</h2>' +
      '<label for="review-annotation-kind">Type</label><select id="review-annotation-kind">' +
      '<option value="comment">Comment</option><option value="suggestion">Suggestion</option><option value="question">Question</option><option value="concern">Concern</option><option value="approval">Approval</option></select>' +
      '<div class="review-annotator-composer__replacement"><label for="review-annotation-replacement">Suggested replacement</label><input id="review-annotation-replacement" type="text"></div>' +
      '<label for="review-annotation-comment">Comment</label><textarea id="review-annotation-comment" placeholder="Write a note…"></textarea>' +
      '<div class="review-annotator-composer__footer"><button type="button" data-action="done">Done</button><button type="button" data-action="cancel">Cancel</button><p class="review-annotator-composer__status" role="status" aria-live="polite"></p></div>';
    document.body.appendChild(composer);
    var kind = composer.querySelector("#review-annotation-kind");
    var comment = composer.querySelector("#review-annotation-comment");
    var replacement = composer.querySelector("#review-annotation-replacement");
    var replacementWrap = composer.querySelector(".review-annotator-composer__replacement");
    var status = composer.querySelector(".review-annotator-composer__status");

    function setStatus(message, state) {
      status.textContent = message || "";
      status.setAttribute("data-state", state || "");
    }
    function hideAdd() { add.setAttribute("data-visible", "false"); add.style.left = ""; add.style.top = ""; }
    function setTerminal(message) {
      terminal = true;
      hideAdd();
      sidebar.setAttribute("data-terminal", "true");
      if (message) setStatus(message, "error");
    }
    function syncDraft() {
      if (!draft) return;
      for (var i = 0; i < draft.items.length; i++) {
        draft.items[i].kind = draft.kind;
        draft.items[i].comment = draft.comment;
        draft.items[i].suggested_replacement = draft.replacement || "";
        var found = false;
        for (var j = 0; j < annotations.length; j++) if (annotations[j].id === draft.items[i].id) {
          annotations[j].kind = draft.items[i].kind;
          annotations[j].comment = draft.items[i].comment;
          annotations[j].suggested_replacement = draft.items[i].suggested_replacement;
          found = true;
          break;
        }
        if (!found && draft.items[i].id) annotations.push(draft.items[i]);
      }
    }
    function grouped() {
      var groups = [], by = {};
      for (var i = 0; i < annotations.length; i++) {
        var item = annotations[i], key = item.group_id || item.id;
        if (!by[key]) { by[key] = { key: key, items: [] }; groups.push(by[key]); }
        by[key].items.push(item);
      }
      return groups;
    }
    function renderList() {
      var groups = grouped();
      count.textContent = groups.length + (groups.length === 1 ? " note" : " notes");
      list.innerHTML = "";
      if (!groups.length) { list.innerHTML = '<p class="review-annotator-sidebar__empty">Select text to leave the first note.</p>'; return; }
      for (var i = 0; i < groups.length; i++) {
        var group = groups[i], item = group.items[0], row = document.createElement("article");
        row.className = "review-annotator-item";
        var meta = document.createElement("div"); meta.className = "review-annotator-item__meta";
        var label = document.createElement("span"); label.textContent = item.kind || "comment";
        var state = document.createElement("span"); state.textContent = item.status === "outdated" ? "outdated" : "";
        meta.appendChild(label); meta.appendChild(state); row.appendChild(meta);
        var quote = document.createElement("q"); quote.className = "review-annotator-item__quote"; quote.textContent = item.quote || "Selected text"; row.appendChild(quote);
        var note = document.createElement("p"); note.className = "review-annotator-item__comment"; note.textContent = item.comment || (item.suggested_replacement ? "→ " + item.suggested_replacement : "Draft note"); row.appendChild(note);
        var actions = document.createElement("div"); actions.className = "review-annotator-item__actions";
        var jump = actionButton("Jump", (function (blockID) { return function () { var target = document.getElementById(blockID); if (target) { target.scrollIntoView({ behavior: "smooth", block: "center" }); target.focus({ preventScroll: true }); } }; })(item.block_id));
        var edit = actionButton("Edit", (function (g) { return function () { openEditor(g); }; })(group));
        var remove = actionButton("Delete", (function (g) { return function () { deleteGroup(g); }; })(group));
        actions.appendChild(jump); actions.appendChild(edit); actions.appendChild(remove); row.appendChild(actions); list.appendChild(row);
      }
    }
    function actionButton(text, fn) { var button = document.createElement("button"); button.type = "button"; button.textContent = text; button.addEventListener("click", fn); return button; }

    function updateReplacement() { replacementWrap.setAttribute("data-visible", kind.value === "suggestion" ? "true" : "false"); }
    function composerPosition(rect) {
      var left = Math.max(8, Math.min(window.innerWidth - composer.offsetWidth - 8, rect.left));
      var top = rect.bottom + 10;
      if (top + composer.offsetHeight > window.innerHeight - 8) top = Math.max(8, rect.top - composer.offsetHeight - 10);
      composer.style.left = left + "px"; composer.style.top = top + "px";
    }
    function copyItem(item) { var copy = {}; for (var key in item) if (Object.prototype.hasOwnProperty.call(item, key)) copy[key] = item[key]; return copy; }

    function deleteItems(items) {
      for (var i = 0; i < items.length; i++) if (items[i].id) api("api/session/annotations/" + encodeURIComponent(items[i].id), "DELETE", token).catch(function () {});
    }
    function openEditor(group) {
      hideAdd();
      var item = group.items[0];
      draft = { items: group.items.map(copyItem), pieces: group.items.map(function (a) { return { block_id: a.block_id, start_line: a.start_line || 0, end_line: a.end_line || 0, quote: a.quote, prefix: a.prefix || "", suffix: a.suffix || "" }; }), kind: item.kind || "comment", comment: item.comment || "", replacement: item.suggested_replacement || "", isNew: false };
      kind.value = draft.kind; comment.value = draft.comment; replacement.value = draft.replacement; updateReplacement();
      composer.querySelector(".review-annotator-composer__title").textContent = "Edit annotation";
      composer.setAttribute("data-visible", "true"); composer.setAttribute("aria-hidden", "false"); setStatus("Saved", "saved");
      composerPosition(document.getElementById(item.block_id) ? document.getElementById(item.block_id).getBoundingClientRect() : { left: 20, top: 80, bottom: 80 });
      comment.focus();
    }
    function openNew(pieces, rect) {
      hideAdd();
      draft = { pieces: pieces, items: [], kind: "comment", comment: "", replacement: "", isNew: true };
      kind.value = "comment"; comment.value = ""; replacement.value = ""; updateReplacement();
      composer.querySelector(".review-annotator-composer__title").textContent = "Add annotation";
      composer.setAttribute("data-visible", "true"); composer.setAttribute("aria-hidden", "false"); setStatus("Saving draft…", "");
      composerPosition(rect); comment.focus();
      queueSave();
    }
    function closeComposer(removeDraft) {
      var closing = draft;
      if (removeDraft && closing) {
        closing.cancelled = true;
        if (closing.isNew) {
          var old = closing.items.slice(0);
          deleteItems(old);
          annotations = annotations.filter(function (a) { return !old.some(function (o) { return o.id === a.id; }); });
        }
      }
      draft = null; composer.setAttribute("data-visible", "false"); composer.setAttribute("aria-hidden", "true"); setStatus("", ""); renderList(); renderHighlights();
    }
    function payloadForCreate(current) {
      var payload = { kind: current.kind, comment: current.comment };
      if (current.kind === "suggestion") payload.suggested_replacement = current.replacement;
      if (current.pieces.length > 1) payload.pieces = current.pieces;
      else {
        var p = current.pieces[0]; payload.block_id = p.block_id; payload.start_line = p.start_line; payload.end_line = p.end_line; payload.quote = p.quote; payload.prefix = p.prefix; payload.suffix = p.suffix;
      }
      return payload;
    }
    function patchPayload(current) {
      var payload = { kind: current.kind, comment: current.comment };
      if (current.kind === "suggestion") payload.suggested_replacement = current.replacement;
      else payload.suggested_replacement = "";
      return payload;
    }
    function queueSave() {
      if (saveTimer) clearTimeout(saveTimer);
      saveTimer = setTimeout(saveDraft, 450);
    }
    function saveDraft() {
      saveTimer = null;
      var current = draft;
      if (!current || terminal) return;
      current.kind = kind.value; current.comment = comment.value; current.replacement = replacement.value;
      if (current.kind === "suggestion" && !current.replacement.replace(/^\s+|\s+$/g, "")) { setStatus("A suggested replacement is required.", "error"); return; }
      syncDraft(); renderList();
      if (saving) { saveAgain = true; return; }
      saving = true; setStatus("Saving…", "");
      var request;
      if (!current.items.length) request = api("api/session/annotations", "POST", token, payloadForCreate(current)).then(function (created) {
        var items = Array.isArray(created) ? created : [created];
        if (draft !== current || current.cancelled) { if (current.isNew) deleteItems(items); return; }
        current.items = items; syncDraft();
      });
      else request = Promise.all(current.items.map(function (item) { return api("api/session/annotations/" + encodeURIComponent(item.id), "PATCH", token, patchPayload(current)); })).then(function (updated) {
        if (draft !== current || current.cancelled) return;
        current.items = updated; syncDraft();
      });
      request.then(function () { if (draft !== current) return; setStatus("Saved", "saved"); renderList(); renderHighlights(); }).catch(function (err) {
        if (draft !== current) return;
        if (err.message.indexOf("no longer open") >= 0 || err.message.indexOf("session") >= 0) setTerminal(err.message); else setStatus(err.message || "Could not save annotation.", "error");
      }).then(function () { saving = false; if (saveAgain) { saveAgain = false; queueSave(); } });
    }
    function deleteGroup(group) {
      if (terminal || !window.confirm("Delete this annotation?")) return;
      Promise.all(group.items.map(function (item) { return api("api/session/annotations/" + encodeURIComponent(item.id), "DELETE", token); })).then(function () {
        annotations = annotations.filter(function (a) { return group.items.indexOf(a) < 0; }); renderList(); renderHighlights();
      }).catch(function (err) { setStatus(err.message || "Could not delete annotation.", "error"); });
    }

    function renderHighlights() {
      var ranges = [], i, j;
      for (i = 0; i < annotations.length; i++) {
        if (annotations[i].status === "outdated") continue;
        var block = document.getElementById(annotations[i].block_id);
        if (!block) continue;
        var found = quoteRanges(block, annotations[i].quote || "", annotations[i].prefix || "", annotations[i].suffix || "");
        for (j = 0; j < found.length; j++) ranges.push(found[j]);
      }
      if (window.CSS && CSS.highlights && window.Highlight) {
        try {
          CSS.highlights.delete("yamdview-annotations");
          if (ranges.length) {
            var highlight = new Highlight();
            for (i = 0; i < ranges.length; i++) highlight.add(ranges[i]);
            CSS.highlights.set("yamdview-annotations", highlight);
          }
          removeFallback();
          return;
        } catch (_) {}
      }
      removeFallback();
      for (i = 0; i < ranges.length; i++) {
        var rects = ranges[i].getClientRects();
        for (j = 0; j < rects.length; j++) { var mark = document.createElement("i"); mark.className = "review-annotator-fallback-mark"; mark.setAttribute("aria-hidden", "true"); mark.style.left = rects[j].left + "px"; mark.style.top = rects[j].top + "px"; mark.style.width = rects[j].width + "px"; mark.style.height = rects[j].height + "px"; document.body.appendChild(mark); }
      }
    }
    function removeFallback() { var old = document.querySelectorAll(".review-annotator-fallback-mark"); for (var i = 0; i < old.length; i++) old[i].remove(); }
    function refresh() { renderHighlights(); }
    function preserveDraftEdits(items) {
      if (!draft) return items;
      draft.kind = kind.value; draft.comment = comment.value; draft.replacement = replacement.value;
      for (var i = 0; i < items.length; i++) {
        for (var j = 0; j < draft.items.length; j++) if (draft.items[j].id === items[i].id) {
          items[i].kind = draft.kind;
          items[i].comment = draft.comment;
          items[i].suggested_replacement = draft.replacement || "";
          break;
        }
      }
      return items;
    }
    function reloadAnnotations() {
      return api("api/session", "GET", "").then(function (metadata) {
        if (metadata.state && metadata.state !== "open") setTerminal("This review is closed; annotations are read-only.");
        annotations = preserveDraftEdits(Array.isArray(metadata.annotations) ? metadata.annotations : []);
        renderList(); renderHighlights();
      }).catch(function (err) { setStatus(err.message || "Could not load annotations.", "error"); renderList(); });
    }
    function scheduleDocumentReload() {
      if (documentReloadTimer) clearTimeout(documentReloadTimer);
      documentReloadTimer = setTimeout(function () {
        documentReloadTimer = null;
        reloadAnnotations();
      }, 200);
    }

    document.addEventListener("mouseup", function () {
      if (terminal || composer.getAttribute("data-visible") === "true") return;
      setTimeout(function () {
        var selection = window.getSelection();
        if (!selection || selection.rangeCount === 0 || selection.isCollapsed) { hideAdd(); return; }
        var range = selection.getRangeAt(0), pieces = selectionPieces(range);
        if (!pieces.length) { hideAdd(); return; }
        var rect = range.getBoundingClientRect(); add.style.left = Math.max(8, Math.min(window.innerWidth - 120, rect.left)) + "px"; add.style.top = Math.max(8, rect.top - 42) + "px"; add._pieces = pieces; add._rect = rect; add.setAttribute("data-visible", "true"); add.focus();
      }, 0);
    });
    add.addEventListener("mousedown", function (event) { event.preventDefault(); });
    add.addEventListener("click", function () { if (add._pieces) openNew(add._pieces, add._rect); });
    kind.addEventListener("change", function () { updateReplacement(); queueSave(); });
    comment.addEventListener("input", queueSave); replacement.addEventListener("input", queueSave);
    composer.querySelector('[data-action="done"]').addEventListener("click", function () { if (saveTimer) { clearTimeout(saveTimer); saveDraft(); } else if (draft) setStatus("Saved", "saved"); });
    composer.querySelector('[data-action="cancel"]').addEventListener("click", function () { closeComposer(true); });
    window.addEventListener("resize", refresh); window.addEventListener("scroll", refresh, true);

    reloadAnnotations();
    window.addEventListener("pageshow", function (event) { if (event.persisted) reloadAnnotations(); });

    if (window.MutationObserver) {
      var observer = new MutationObserver(function (mutations) {
        var documentChanged = false;
        for (var i = 0; i < mutations.length; i++) {
          if (mutations[i].target === panel) {
            if (panel.getAttribute("data-session-state") !== "open") setTerminal("This review is closed; annotations are read-only.");
          } else documentChanged = true;
        }
        setTimeout(refresh, 0);
        if (documentChanged) scheduleDocumentReload();
      });
      var documentEl = document.getElementById("document"); if (documentEl) observer.observe(documentEl, { childList: true, subtree: true });
      observer.observe(panel, { attributes: true, attributeFilter: ["data-session-state"] });
    }
    renderList();
  }

  ready(init);
})();
