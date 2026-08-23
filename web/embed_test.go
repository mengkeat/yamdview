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
