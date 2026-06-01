package tablefix

import (
	"strings"
	"testing"
)

func TestValidMarkdownTableUnchanged(t *testing.T) {
	input := "| Name | Score |\n| --- | ---: |\n| Alice | 10 |\n"

	got := Fix(input)

	if !got.TableLike {
		t.Fatal("expected valid table to be detected")
	}
	if got.Applied {
		t.Fatal("valid table should not be rewritten")
	}
	if got.Markdown != input {
		t.Fatalf("valid table changed:\ngot:  %q\nwant: %q", got.Markdown, input)
	}
	if len(got.Diagnostics) != 0 {
		t.Fatalf("valid table diagnostics = %+v, want none", got.Diagnostics)
	}
}

func TestRepairsMissingSeparator(t *testing.T) {
	input := "Name | Score\nAlice | 10\nBob | 9\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	checks := []string{
		"| Name | Score |",
		"| --- | --- |",
		"| Alice | 10 |",
		"| Bob | 9 |",
	}
	for _, want := range checks {
		if !strings.Contains(got.Markdown, want) {
			t.Fatalf("repaired table missing %q:\n%s", want, got.Markdown)
		}
	}
}

func TestPadsShortRows(t *testing.T) {
	input := "Name | Score | Note\nAlice | 10\nBob | 9 | ok\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, "| Alice | 10 |  |") {
		t.Fatalf("expected short row to be padded, got:\n%s", got.Markdown)
	}
}

func TestAmbiguousInconsistentRowsUnchanged(t *testing.T) {
	input := "Name | Score | Note\nAlice | 10\nBob | 9 | ok | extra\n"

	got := Fix(input)

	if got.Applied {
		t.Fatalf("ambiguous table should not be repaired:\n%s", got.Markdown)
	}
	if got.Markdown != input {
		t.Fatalf("ambiguous table changed:\ngot:  %q\nwant: %q", got.Markdown, input)
	}
	if !hasDiagnostic(got.Diagnostics, CodeAmbiguous) {
		t.Fatalf("expected %s diagnostic, got %+v", CodeAmbiguous, got.Diagnostics)
	}
}

func TestEscapedPipesAndInlineCodePreserved(t *testing.T) {
	input := "Pattern | Meaning\n`a | b` | inline code\nx \\| y | escaped pipe\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	for _, want := range []string{"`a | b`", "x \\| y"} {
		if !strings.Contains(got.Markdown, want) {
			t.Fatalf("repaired table lost %q:\n%s", want, got.Markdown)
		}
	}
}

func TestPreprocessRepairsTableBlock(t *testing.T) {
	input := "Intro\n\nName | Score\nAlice | 10\nBob | 9\n\nDone\n"

	got := string(Preprocess([]byte(input)))

	if !strings.Contains(got, "| --- | --- |") {
		t.Fatalf("preprocess did not add separator:\n%s", got)
	}
	if !strings.Contains(got, "Intro\n\n") || !strings.Contains(got, "\n\nDone") {
		t.Fatalf("preprocess did not preserve surrounding prose:\n%s", got)
	}
}

func TestPreprocessLeavesFencedCodeUnchanged(t *testing.T) {
	input := "```\nName | Score\nAlice | 10\n```\n"

	got := string(Preprocess([]byte(input)))

	if got != input {
		t.Fatalf("fenced code changed:\ngot:  %q\nwant: %q", got, input)
	}
}

func TestLooksLikeTableStart(t *testing.T) {
	if !LooksLikeTableStart("Name | Score", "Alice | 10") {
		t.Fatal("missing-separator table start not detected")
	}
	if !LooksLikeTableStart("Name | Score", "--- | ---") {
		t.Fatal("valid table start not detected")
	}
	if LooksLikeTableStart("Use `a | b` here", "not a table") {
		t.Fatal("inline code pipe should not be table start")
	}
}

func hasDiagnostic(diags []Diagnostic, code string) bool {
	for _, diag := range diags {
		if diag.Code == code {
			return true
		}
	}
	return false
}
