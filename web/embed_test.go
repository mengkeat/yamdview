package web

import (
	"strings"
	"testing"
)

func TestReviewAnnotatorAssetWiring(t *testing.T) {
	assets, err := LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"X-Yamdview-Token", "/api/session", "/api/session/annotations", "suggested_replacement", "CSS.highlights", "Add comment"} {
		if !strings.Contains(assets.AnnotatorJS, want) {
			t.Errorf("annotator asset missing %q", want)
		}
	}
	if !strings.Contains(assets.IndexHTML, "{{if .Review}}") || !strings.Contains(assets.IndexHTML, "{{.AnnotatorJS}}") {
		t.Fatal("review template does not conditionally load annotator asset")
	}
}

func TestReviewAnnotatorHardensSelectionAndDraftLifecycle(t *testing.T) {
	assets, err := LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	checks := []string{
		"(function (blockID)",
		"actionButton(\"Edit\", (function (g)",
		"actionButton(\"Delete\", (function (g)",
		"if (current.pieces.length > 1) payload.pieces = current.pieces",
		"textOffset(block, part.startContainer, part.startOffset)",
		"quoteRanges(block, annotations[i].quote || \"\", annotations[i].prefix || \"\", annotations[i].suffix || \"\")",
		"if (draft !== current || current.cancelled)",
		"function reloadAnnotations()",
		"event.persisted",
		"function scheduleDocumentReload()",
		"documentReloadTimer = setTimeout(function () {\n        documentReloadTimer = null;\n        reloadAnnotations();",
		"if (documentChanged) scheduleDocumentReload();",
	}
	for _, want := range checks {
		if !strings.Contains(assets.AnnotatorJS, want) {
			t.Errorf("annotator regression guard missing %q", want)
		}
	}
	if strings.Contains(assets.AnnotatorJS, "draft.isNew = false") {
		t.Fatal("new drafts must remain deletable after their first autosave")
	}
}
