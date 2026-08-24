package llm

import (
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"
)

//go:embed prompts/*.md
var promptFS embed.FS

// PromptData carries the per-candidate fields substituted into a prompt
// template. Fields are optional; templates use {{- if ...}} guards so empty
// fields produce clean output rather than blank sections.
type PromptData struct {
	// Kind selects the prompt template; it is also echoed into the template.
	Kind RequestKind
	// BlockKind is the document block kind the candidate came from
	// (paragraph, code_fence, math_block, table, table_cell).
	BlockKind string
	// SourceSpan is the exact candidate text to repair or classify.
	SourceSpan string
	// Context is optional surrounding context for disambiguation only.
	Context string
	// Diagnostics are the deterministic diagnostics that triggered fallback.
	Diagnostics []string
	// HeuristicTeX is an existing heuristic TeX candidate (math only).
	HeuristicTeX string
}

var (
	promptTemplates     map[RequestKind]*template.Template
	promptTemplatesOnce sync.Once
	promptTemplatesErr  error
)

func loadPromptTemplates() (map[RequestKind]*template.Template, error) {
	promptTemplatesOnce.Do(func() {
		entries := map[RequestKind]string{
			KindMathFix:           "prompts/math_fix.md",
			KindTableFix:          "prompts/table_fix.md",
			KindClassifyCandidate: "prompts/classify_candidate.md",
			KindFeedbackRephrase:  "prompts/feedback_rephrase.md",
		}
		promptTemplates = make(map[RequestKind]*template.Template, len(entries))
		for kind, name := range entries {
			raw, err := promptFS.ReadFile(name)
			if err != nil {
				promptTemplatesErr = fmt.Errorf("load prompt %s: %w", name, err)
				return
			}
			tmpl, err := template.New(name).Parse(string(raw))
			if err != nil {
				promptTemplatesErr = fmt.Errorf("parse prompt %s: %w", name, err)
				return
			}
			promptTemplates[kind] = tmpl
		}
	})
	return promptTemplates, promptTemplatesErr
}

// SystemPrompt returns the short role instruction prepended as the system
// message for a repair request. The detailed rules live in the rendered user
// prompt template.
func SystemPrompt(kind RequestKind) string {
	switch kind {
	case KindMathFix:
		return "You are a precise math notation repair tool. Output strict JSON only."
	case KindTableFix:
		return "You are a precise Markdown table repair tool. Output strict JSON only."
	case KindClassifyCandidate:
		return "You are a precise math-prose classifier. Output strict JSON only."
	case KindFeedbackRephrase:
		return "You are a precise feedback reformulation tool. Output strict JSON only."
	default:
		return "You are a precise repair tool. Output strict JSON only."
	}
}

// RenderPrompt renders the user prompt template for kind with data. It returns
// an error if the kind has no template or rendering fails.
func RenderPrompt(kind RequestKind, data PromptData) (string, error) {
	templates, err := loadPromptTemplates()
	if err != nil {
		return "", err
	}
	tmpl, ok := templates[kind]
	if !ok {
		return "", fmt.Errorf("no prompt template for kind %q", kind)
	}
	data.Kind = kind
	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render prompt %q: %w", kind, err)
	}
	return out.String(), nil
}
