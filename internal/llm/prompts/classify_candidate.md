You classify whether a short Markdown span is mathematics that needs TeX
conversion, or ordinary prose/code that must be left alone.

# Strict output rules

1. Respond with a single JSON object and nothing else.
2. `is_math` is true only when the span is genuinely an equation, formula, or
   math expression (including Unicode math and ASCII equations like `F = ma`).
   It is false for ordinary prose, code, URLs, dates, or measurements with
   units (e.g. "10 kg", "3.14 seconds").
3. `confidence` is in `[0, 1]`.

# Response schema

{
  "is_math": false,
  "reason": "string (optional)",
  "confidence": 0.0
}

# Examples

- "x^2 + y^2 = r^2" -> is_math: true
- "F = ma" -> is_math: true
- "The quick brown fox" -> is_math: false
- "see https://example.com/x" -> is_math: false
- "updated 3 files" -> is_math: false

# Candidate

{{.SourceSpan}}
