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
	for _, want := range []string{"`a \\| b`", "x \\| y"} {
		if !strings.Contains(got.Markdown, want) {
			t.Fatalf("repaired table lost %q:\n%s", want, got.Markdown)
		}
	}
}

func TestValidSeparatorTableEscapesCodePipesForGoldmark(t *testing.T) {
	input := "| Pattern | Meaning |\n| --- | --- |\n| `a | b` | inline code |\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected render repair for code pipe, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, "`a \\| b`") {
		t.Fatalf("code pipe was not escaped for table parsing:\n%s", got.Markdown)
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

// ── Math pipe (absolute value) tests ──────────────────────

func TestMathPipeGreekLetterInHeader(t *testing.T) {
	// Table with |ω| (absolute value of omega) in a header cell.
	input := "| Method | kM·|ω| err (%) | Runtime (ms) |\n| --- | ---: | ---: |\n| EKF | 12.322 | 35.244 |\n"

	got := Fix(input)

	if !got.TableLike {
		t.Fatal("expected table to be detected")
	}
	if !got.Applied {
		t.Fatalf("expected repair for math pipes, diagnostics: %+v", got.Diagnostics)
	}
	// The math pipes should be escaped in the output.
	if !strings.Contains(got.Markdown, `kM·\|ω\| err (%)`) {
		t.Fatalf("math pipes not escaped in output:\n%s", got.Markdown)
	}
	// Should have exactly 3 columns (not split by ω pipes).
	if !strings.Contains(got.Markdown, "| --- | ---: | ---: |") {
		t.Fatalf("separator not preserved:\n%s", got.Markdown)
	}
}

func TestMathPipeSingleLetterAbsoluteValue(t *testing.T) {
	// Table with |v| (absolute value of v) in a header.
	input := "| Param | |v| value |\n| --- | --- |\n| kD | 0.374 |\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, `\|v\| value`) {
		t.Fatalf("single-letter math pipe not escaped:\n%s", got.Markdown)
	}
}

func TestMathPipeDoesNotMatchDigits(t *testing.T) {
	// |10| should NOT be treated as absolute value.
	input := "| Name | Score |\n| --- | --- |\n| Alice | 10 |\n"

	got := Fix(input)

	if got.Applied {
		t.Fatalf("digits between pipes should not trigger math pipe escape:\n%s", got.Markdown)
	}
}

func TestMathPipeDoesNotMatchEnglishWords(t *testing.T) {
	// |ok|, |no|, |pi| should NOT be treated as absolute value.
	input := "| Status | ok | pi |\n| --- | --- | --- |\n| good | yes | 3.14 |\n"

	got := Fix(input)

	// "ok" and "pi" are 2-letter words — should not be treated as math pipes.
	// But "pi" could look like math... let's check it doesn't break the table.
	if got.Applied {
		// If it does get applied, make sure it's not escaping ok/pi
		if strings.Contains(got.Markdown, `\|ok\|`) || strings.Contains(got.Markdown, `\|pi\|`) {
			t.Fatalf("English words incorrectly treated as math pipes:\n%s", got.Markdown)
		}
	}
}

func TestMathPipeInMultipleRows(t *testing.T) {
	// Multiple rows with math pipes in the header.
	input := "| kD | kM·|ω| | Axis (°) |\n| ---: | ---: | ---: |\n| 5.5 | 12.3 | 0.9 |\n| 3.4 | 16.5 | 21.8 |\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, `kM·\|ω\|`) {
		t.Fatalf("math pipe not escaped:\n%s", got.Markdown)
	}
}

func TestMathPipePreprocessFullDocument(t *testing.T) {
	input := "## Benchmark\n\n| Method | kM·|ω| err |\n| --- | --- |\n| EKF | 12.3 |\n\nDone.\n"

	got := string(Preprocess([]byte(input)))

	if !strings.Contains(got, `kM·\|ω\| err`) {
		t.Fatalf("preprocess did not escape math pipes:\n%s", got)
	}
	if !strings.Contains(got, "## Benchmark\n\n") || !strings.Contains(got, "\n\nDone.") {
		t.Fatalf("preprocess corrupted surrounding text:\n%s", got)
	}
}

// ── Additional malformed table edge cases ──────────────────

func TestValidTableWithSuperscriptInHeader(t *testing.T) {
	// Superscript characters (like ²) should not trigger math pipe detection.
	input := "| x² | y³ |\n| --- | --- |\n| 4 | 27 |\n"

	got := Fix(input)

	// This is a valid table — should not be rewritten.
	if got.Applied {
		t.Fatalf("superscript-only table should not need repair:\n%s", got.Markdown)
	}
}

func TestValidTableWithDegreeInHeader(t *testing.T) {
	// Degree symbol (°) should not trigger math pipe detection.
	input := "| Angle (°) | Value |\n| --- | --- |\n| 90 | 1.0 |\n"

	got := Fix(input)

	if got.Applied {
		t.Fatalf("degree-only table should not need repair:\n%s", got.Markdown)
	}
}

func TestTableWithEscapedMathPipeAlreadyEscaped(t *testing.T) {
	// If the math pipes are already escaped, table should be handled gracefully.
	input := "| kD | kM·\\|ω\\| err |\n| --- | --- |\n| 5.5 | 12.3 |\n"

	got := Fix(input)

	if !got.TableLike {
		t.Fatal("expected table to be detected")
	}
}

func TestTableWithMixedPipesAndMath(t *testing.T) {
	// Table with both code pipes and math pipes.
	input := "| `a | b` | kM·|ω| |\n| --- | --- |\n| code | math |\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, "`a \\| b`") {
		t.Fatalf("code pipe not escaped:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, `kM·\|ω\|`) {
		t.Fatalf("math pipe not escaped:\n%s", got.Markdown)
	}
}

func TestTableWithOnlyMathPipeNoSeparator(t *testing.T) {
	// Missing-separator table with math pipe in header.
	input := "Method | kM·|ω| err\nEKF | 12.3\nGP | 13.0\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, `kM·\|ω\| err`) {
		t.Fatalf("math pipe not escaped in missing-separator repair:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "| --- |") {
		t.Fatalf("separator not added:\n%s", got.Markdown)
	}
}

func TestTableWithTrailingPipeAndMathPipe(t *testing.T) {
	// Ensure trailing pipe detection works with math pipes.
	input := "| Method | kM·|ω| err (%) |\n| --- | --- |\n| EKF | 12.322 |\n"

	got := Fix(input)

	if !got.TableLike {
		t.Fatal("expected table detection")
	}
	if !got.Applied {
		t.Fatalf("expected math pipe repair, diagnostics: %+v", got.Diagnostics)
	}
}

func TestMultipleMathPipePairsInOneRow(t *testing.T) {
	// Header with multiple math absolute value expressions.
	input := "| |v| | |ω| | Combined |\n| --- | --- | --- |\n| 5.2 | 12.3 | 17.5 |\n"

	got := Fix(input)

	if !got.Applied {
		t.Fatalf("expected repair, diagnostics: %+v", got.Diagnostics)
	}
	if !strings.Contains(got.Markdown, `\|v\|`) {
		t.Fatalf("first math pipe not escaped:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, `\|ω\|`) {
		t.Fatalf("second math pipe not escaped:\n%s", got.Markdown)
	}
}

func TestTableStartsWithSeparatorUnfixable(t *testing.T) {
	input := "| --- | --- |\n| Alice | 10 |\n"

	got := Fix(input)

	if got.Applied {
		t.Fatalf("table starting with separator should not be repaired:\n%s", got.Markdown)
	}
	if !hasDiagnostic(got.Diagnostics, CodeUnfixable) {
		t.Fatalf("expected unfixable diagnostic, got: %+v", got.Diagnostics)
	}
}

func TestSinglePipeRowNotTable(t *testing.T) {
	input := "Just a | in text\n"

	got := Fix(input)

	if got.TableLike {
		t.Fatalf("single pipe line should not be table-like")
	}
}

func TestEmptyInput(t *testing.T) {
	got := Fix("")

	if got.TableLike || got.Applied {
		t.Fatal("empty input should not be table-like")
	}
}

func TestPreprocessEmptyInput(t *testing.T) {
	got := Preprocess(nil)
	if got != nil {
		t.Fatalf("nil input should return nil, got %q", string(got))
	}
}

func TestPreprocessNoPipes(t *testing.T) {
	input := []byte("Hello world\nNo pipes here\n")
	got := Preprocess(input)
	if string(got) != string(input) {
		t.Fatalf("pipe-free input changed:\ngot:  %q\nwant: %q", string(got), string(input))
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
