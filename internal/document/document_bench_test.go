package document

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mengkeat/yamdview/internal/markdown"
)

// benchSectionCount is the number of repeated sections in the synthetic
// documents. Each section contributes four top-level blocks (heading,
// paragraph, table, closing note), keeping the total just under the diff
// matrix cutoff (maxDiffMatrixCells) so Diff produces patch ops instead of a
// full reset at every benchmark size.
const benchSectionCount = 120

// generateBenchDoc builds a deterministic synthetic Markdown document of
// approximately targetBytes. Each section contains a heading, a long
// multi-line paragraph with inline math, a valid pipe table, and a closing
// paragraph, so segmentation, tablefix, mathfix, and goldmark all do real
// work during rendering.
func generateBenchDoc(targetBytes int) []byte {
	var out strings.Builder
	out.WriteString("# Synthetic Benchmark Document\n\n")

	perSection := targetBytes / benchSectionCount
	for i := 1; i <= benchSectionCount && out.Len() < targetBytes; i++ {
		writeBenchSection(&out, i, perSection)
	}
	return []byte(out.String())
}

func writeBenchSection(out *strings.Builder, index, budget int) {
	fmt.Fprintf(out, "### Section %d\n\n", index)

	lines := budget * 3 / 4 / 96
	if lines < 1 {
		lines = 1
	}
	for j := 0; j < lines; j++ {
		fmt.Fprintf(out,
			"Paragraph %d line %d: lorem ipsum dolor sit amet with inline math $a_%d + b_%d = %d$ and **emphasis**.\n",
			index, j+1, j, j, (j*index)%89)
	}
	out.WriteByte('\n')

	out.WriteString("| Column A | Column B | Column C |\n")
	out.WriteString("| --- | --- | --- |\n")
	for r := 0; r < 4; r++ {
		fmt.Fprintf(out, "| row %d-%d | value %d | $%d$ units |\n", index, r, r*index, r+index)
	}
	out.WriteByte('\n')

	fmt.Fprintf(out,
		"Closing note %d with Euler identity $e^{i\\pi} + 1 = 0$ and trailing text to round out the section.\n\n",
		index)
}

// editMiddleParagraph changes one word in the paragraph nearest the middle of
// the document, so exactly one block differs from the previous snapshot.
func editMiddleParagraph(src []byte) []byte {
	s := string(src)
	mid := len(s) / 2
	offset := strings.Index(s[mid:], "lorem ipsum")
	if offset < 0 {
		panic("benchmark document missing edit anchor")
	}
	pos := mid + offset
	return []byte(s[:pos] + "lorem ipsum EDITED" + s[pos+len("lorem ipsum"):])
}

// BenchmarkBuildSnapshotFresh measures a cold BuildSnapshot: no previous
// snapshot is supplied, so every block runs through the full render pipeline.
func BenchmarkBuildSnapshotFresh(b *testing.B) {
	for _, sizeMB := range []int{1, 5, 10} {
		src := generateBenchDoc(sizeMB << 20)
		md := markdown.NewRenderer()
		b.Run(fmt.Sprintf("%dMB", sizeMB), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := BuildSnapshot(md, src, DocumentSnapshot{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkBuildSnapshotSingleBlockEdit measures an incremental BuildSnapshot
// where one paragraph in the middle changed and every other block is reused
// from the previous snapshot without re-rendering.
func BenchmarkBuildSnapshotSingleBlockEdit(b *testing.B) {
	for _, sizeMB := range []int{1, 5, 10} {
		src := generateBenchDoc(sizeMB << 20)
		md := markdown.NewRenderer()
		prev, err := BuildSnapshot(md, src, DocumentSnapshot{})
		if err != nil {
			b.Fatal(err)
		}
		edited := editMiddleParagraph(src)
		b.Run(fmt.Sprintf("%dMB", sizeMB), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := BuildSnapshot(md, edited, prev); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDiffSingleBlockEdit measures generating the patch operations for a
// single-block edit via Diff over snapshots built from the edited source.
func BenchmarkDiffSingleBlockEdit(b *testing.B) {
	for _, sizeMB := range []int{1, 5, 10} {
		src := generateBenchDoc(sizeMB << 20)
		md := markdown.NewRenderer()
		prev, err := BuildSnapshot(md, src, DocumentSnapshot{})
		if err != nil {
			b.Fatal(err)
		}
		next, err := BuildSnapshot(md, editMiddleParagraph(src), prev)
		if err != nil {
			b.Fatal(err)
		}
		result := Diff(prev, next)
		if result.Reset {
			b.Fatalf("diff fell back to full reset for %dMB document", sizeMB)
		}
		if len(result.Ops) == 0 {
			b.Fatalf("expected patch ops for %dMB single-block edit, got none", sizeMB)
		}
		b.Run(fmt.Sprintf("%dMB", sizeMB), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				Diff(prev, next)
			}
		})
	}
}
