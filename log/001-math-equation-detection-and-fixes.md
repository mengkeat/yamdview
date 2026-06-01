# Mathematical equation detection and fix algorithms

Date: 2026-06-01
Status: Current implementation notes and robustness roadmap
Affected area: `internal/mathfix`, `internal/markdown`

## Summary

`yamdview` renders Markdown through Goldmark, then client-side KaTeX renders math placeholders emitted by the custom math extension. The math-fixing layer sits before Goldmark. It rewrites likely mathematical text into TeX-delimited Markdown so the existing math renderer can turn it into KaTeX HTML placeholders.

There are currently two related but distinct detection paths:

1. **Unicode math detection in ordinary Markdown prose**
   - Converts LLM-style Unicode math such as `∀x ∈ ℝ, x² ≥ 0` into TeX.
   - Operates on paragraphs, headings, blockquotes, and other non-code Markdown text.
   - Uses Unicode math characters as the primary trigger.

2. **Probable equation detection inside short `text`/`txt` fences**
   - Converts short fenced plaintext equation snippets such as:

     ```text
     dv/dt = g − kD |v| v + (kM·|ω|)(axis × v) + f_NN(v)
     ```

     into display math:

     ```tex
     \frac{dv}{dt} = g - k_{D} \lvert v\rvert v + (k_{M}\cdot\lvert \omega\rvert)(\mathrm{axis} \times v) + f_{\mathrm{NN}}(v)
     ```

The source Markdown file is not modified. All conversion is render-only inside the preprocessed source passed to Goldmark.

## Rendering pipeline

At a high level:

```text
Markdown bytes
  ↓
mathfix.Preprocess
  ↓
Goldmark Markdown parser + yamdview math extension
  ↓
HTML containing .math elements with data-tex attributes
  ↓
Browser-side KaTeX render
```

The important design choice is that `mathfix.Preprocess` produces ordinary Markdown math syntax (`$...$` or `$$...$$`). After preprocessing, the rest of the system does not need to know whether the math came from explicit user TeX, Unicode math, or a probable plaintext equation.

## Preprocessor structure

`internal/mathfix/preproc.go` processes input line by line.

It maintains three pieces of state:

- `paragraph`: accumulated non-blank, non-structural Markdown lines.
- `inCodeFence`: whether the scanner is inside a fenced block.
- `fenceLines`, `fenceMarker`, `fenceInfo`: buffered fenced block content and metadata.

### Paragraph flow

For ordinary text:

1. Accumulate adjacent non-blank lines into `paragraph`.
2. On a blank line or structural Markdown boundary, flush the paragraph.
3. `fixParagraphText` applies Unicode math conversion only when:
   - the paragraph does not already contain recognized TeX delimiters, and
   - `HasUnicodeMath` sees a supported Unicode math character.

This means purely ASCII text such as `F = ma` is not currently converted in ordinary prose. That is intentional: ASCII-only equation detection in prose has a high false-positive risk.

### Fence flow

For fenced blocks:

1. On fence open, flush any pending paragraph.
2. Buffer all fence lines until a matching closing fence is seen.
3. If the fence is closed and its info string is `text` or `txt`, attempt probable-equation conversion.
4. If conversion succeeds, replace the entire fence with a `$$...$$` display math block.
5. Otherwise, emit the original fenced block unchanged.

Only `text`/`txt` fences are considered. Fences such as `python`, `bash`, `json`, and unspecified code fences are preserved. This is a safety boundary: code fences are often command output or source code, and converting those would be surprising.

## Unicode math detection

Unicode math detection is implemented in `internal/mathfix/detect.go`.

### Character categories

`HasUnicodeMath` returns true if any rune belongs to one of the supported math categories:

- Mathematical operators and symbols: `∀`, `∈`, `∑`, `∫`, `≤`, `→`, `×`, `·`, `√`, `°`, `−`, etc.
- Greek letters: `α`, `β`, `π`, `ω`, plus uppercase and variant forms.
- Superscripts: `²`, `³`, `ⁿ`, `ⁱ`, etc.
- Subscripts: `₀`, `₁`, `ᵢ`, `ᵣ`, etc.
- Blackboard bold: `ℝ`, `ℕ`, `ℤ`, `ℂ`, etc.
- Vulgar fractions: `½`, `⅓`, `¼`, etc.

### Confidence score

`Score` produces a confidence value in `[0, 1]`. It is not a statistical model; it is a hand-weighted heuristic.

Current scoring signals:

- Baseline if any Unicode math character exists: `+0.20`
- Math operators: `+0.20`
- Superscripts/subscripts: `+0.15`
- Greek letters: `+0.10`
- Blackboard bold: `+0.10`
- Vulgar fractions: `+0.10`
- Math-character density at least 15%: `+0.10`
- Math-character density at least 40%: `+0.15`

The current paragraph-level `Fix` path applies when the score is at least `0.05`, after which span detection determines what substring to wrap as math.

## Unicode math span detection

`Fix` does not necessarily wrap an entire paragraph. It finds math spans inside prose.

The span detector:

1. Finds seed positions: Unicode math characters outside inline code spans.
2. Extends each seed left and right through math-relevant characters.
3. Merges overlapping spans.
4. Trims leading/trailing spaces and unbalanced delimiters.

### Extension rules

The extension logic is deliberately conservative around English words.

Allowed while extending includes:

- Unicode math characters.
- Digits.
- ASCII single-letter variables.
- Operators such as `+`, `-`, `=`, `<`, `>`, `*`, `/`, `^`, `_`.
- Parentheses/brackets/braces.
- Some punctuation such as comma and period.

Word-length rules reduce false positives:

- Directly adjacent ASCII words up to two letters can be included, which helps with terms like `dx`.
- Across a space, only single-letter ASCII variables are allowed.
- Longer English words such as `where`, `axis`, `always`, or `while` usually stop span growth.

This is why Unicode math inside prose can be converted without swallowing entire sentences.

## Unicode-to-TeX conversion

Character conversion is implemented in `internal/mathfix/convert.go`.

### Direct mapping

The `charMap` table maps individual Unicode symbols to TeX commands:

```text
α → \alpha
ω → \omega
∈ → \in
∑ → \sum
× → \times
· → \cdot
ℝ → \mathbb{R}
½ → \frac{1}{2}
− → -
° → {}^\circ
```

After writing a TeX command, the converter inserts a space before a following ASCII letter when needed. This avoids accidental command concatenation, such as `\alphax` instead of `\alpha x`.

### Superscript and subscript runs

Consecutive Unicode superscript or subscript characters are grouped:

```text
x²³   → x^{23}
x₀₁₂ → x_{012}
αᵢ   → \alpha_{i}
```

Grouping prevents output like `x^{2}^{3}` when the input clearly represents one exponent run.

### Square roots

The square-root handler treats a few common cases specially:

```text
√x        → \sqrt{x}
√2        → \sqrt{2}
√(x²+y²) → \sqrt{x^{2}+y^{2}}
```

If the square-root symbol has no clear argument, it emits `\sqrt{}` and records a diagnostic.

## Probable text-fenced equation detection

The recent fix addresses equations written inside `text` fences. In the BallFlight benchmark log, the trajectory equation was fenced as plaintext:

```text
dv/dt = g − kD |v| v + (kM·|ω|)(axis × v) + f_NN(v)
```

Goldmark correctly treated that as code, so the previous math conversion did not touch it. The new path recognizes short `text` fences that look more like display equations than logs or source code.

### Scope restrictions

A text fence is eligible only when all of these are true:

1. The fence is closed.
2. The fence info string is `text` or `txt`.
3. The content is non-empty.
4. There are at most four non-empty lines.
5. The block does not already contain recognized TeX delimiters.
6. Every non-empty line independently looks equation-like.

These restrictions are intentionally strict. A `text` fence might contain CLI output, stack traces, tables, logs, or data. The current implementation prefers missing some equations over converting ordinary text output into broken display math.

### Equation-line scoring

A line must contain a relation operator from this set:

```text
= < > ≤ ≥ ≠ ≈ ≡ ∼ ∝
```

Then a simple score is computed:

| Signal | Points | Rationale |
| --- | ---: | --- |
| Relation operator present | 2 | Equations usually relate two expressions. |
| ASCII or partial derivative pattern | 3 | `dv/dt` and `∂f/∂x` are strong equation signals. |
| Supported Unicode math character | 2 | Unicode math symbols are strong math indicators. |
| Underscore present | 1 | Often indicates subscript-like notation. |
| Absolute-value bars present | 1 | Common in math/physics equations. |
| Operators or delimiters present | 1 | `+`, `/`, `()`, `−`, `×`, `·`, etc. |

The line is accepted when the score is at least `4`.

For the trajectory equation:

```text
dv/dt = g − kD |v| v + (kM·|ω|)(axis × v) + f_NN(v)
```

Signals include:

- `=` relation: `+2`
- `dv/dt` derivative: `+3`
- Unicode math symbols `−`, `·`, `ω`, `×`: `+2`
- `_` in `f_NN`: `+1`
- `|v|` and `|ω|`: `+1`
- operators and parentheses: `+1`

The score is well above the threshold.

## Probable equation fix algorithm

Once a text-fenced block is accepted, each non-empty line is converted with `equationLineToTeX`.

### Step 1: derivative normalization

Before generic Unicode conversion, derivative-like ASCII and partial-derivative patterns are normalized.

Current patterns:

```text
dv/dt      → \frac{dv}{dt}
D v / D t  → \frac{dv}{dt} style, depending on captured variables
∂f/∂x      → \frac{\partial f}{\partial x}
```

The current ASCII derivative regex captures single-letter numerator and denominator variables:

```text
\b[dD]\s*([A-Za-z])\s*/\s*[dD]\s*([A-Za-z])\b
```

This is sufficient for `dv/dt`, but not for richer derivatives such as `d^2x/dt^2` or `dtheta/dt`.

### Step 2: Unicode conversion

The line is passed through the existing Unicode converter. This handles:

```text
− → -
· → \cdot
ω → \omega
× → \times
```

It also handles any superscripts, subscripts, Greek letters, blackboard bold, fractions, roots, and other Unicode math already supported by `convertChars`.

### Step 3: equation-specific normalization

After Unicode conversion, `normalizeEquationTeX` applies additional equation-oriented rewrites.

#### Numeric fractions

Simple numeric fractions become TeX fractions:

```text
1/2 → \frac{1}{2}
3/10 → \frac{3}{10}
```

This is currently limited to integer-over-integer forms. It intentionally does not transform arbitrary symbolic divisions such as `a/b`, because blindly converting every slash can damage expressions like units (`m/s`) or prose-like text.

#### Absolute-value bars

Simple bar-delimited expressions become `\lvert ... \rvert`:

```text
|v|       → \lvert v\rvert
|\omega| → \lvert \omega\rvert
```

This is regex-based and handles simple non-nested cases only.

#### Explicit underscore subscripts

Identifiers of the form `letter_suffix` become subscripts:

```text
f_NN → f_{\mathrm{NN}}
x_1  → x_{1}
a_i  → a_{i}
```

A multi-letter all-uppercase suffix is wrapped in `\mathrm{...}` because it usually denotes a label or acronym rather than a product of variables. This produces better output for neural-network notation:

```text
f_NN(v) → f_{\mathrm{NN}}(v)
```

#### Camel-case one-letter physics parameters

Lowercase-uppercase two-character identifiers become single-letter subscripts:

```text
kD → k_{D}
kM → k_{M}
```

This targets common parameter notation in plaintext, especially physics logs where `kD` means drag coefficient `k_D` and `kM` means Magnus coefficient `k_M`.

#### Domain word normalization

The word `axis` is converted to roman text inside math:

```text
axis → \mathrm{axis}
```

This prevents KaTeX from rendering it as a product of variables `a x i s`.

## Worked example: trajectory equation

Input Markdown:

```markdown
```text
dv/dt = g − kD |v| v + (kM·|ω|)(axis × v) + f_NN(v)
```
```

Preprocessor output:

```markdown
$$
\frac{dv}{dt} = g - k_{D} \lvert v\rvert v + (k_{M}\cdot\lvert \omega\rvert)(\mathrm{axis} \times v) + f_{\mathrm{NN}}(v)
$$
```

Rendered HTML contains a display math placeholder:

```html
<div class="math math-display" data-tex="\frac{dv}{dt} = g - k_{D} \lvert v\rvert v + (k_{M}\cdot\lvert \omega\rvert)(\mathrm{axis} \times v) + f_{\mathrm{NN}}(v)"></div>
```

The browser-side KaTeX renderer then typesets that placeholder.

## Safety properties

The current design has several intentional safety properties.

### Render-only conversion

The original Markdown source file is never rewritten. If a heuristic is wrong, the source remains intact.

### TeX idempotence

Paragraphs and probable equation blocks that already contain recognized TeX delimiters are not rewritten. This prevents double-wrapping and avoids mangling hand-authored TeX.

### Code protection

Inline code spans are excluded from Unicode math span detection.

Fenced code blocks are preserved except for short closed `text`/`txt` fences that pass the probable-equation detector. Language-specific fences remain untouched.

### Conservative ASCII handling

ASCII-only equation detection is not applied to ordinary prose. It is only applied to short text fences, where the user has already visually isolated the content.

## Known limitations

The current system is useful, but it is not a real mathematical parser. Important limitations remain.

### Regex-based transformations are shallow

The equation fixer uses regexes and local replacements. It does not build an expression tree. Therefore it cannot reliably understand precedence, grouping, nested bars, or semantic intent.

Examples that are not robustly handled:

```text
||v||
|x + |y||
d²x/dt²
dtheta/dt
(a+b)/(c+d)
1/(2πσ²)
```

### Slash conversion is intentionally limited

Only numeric fractions are converted. Symbolic divisions are left alone to avoid false positives.

This means:

```text
x/y
(a+b)/(c+d)
m/s²
```

are not generally transformed into TeX fractions. That is safer, but less visually polished for true equations.

### Domain-specific notation is hardcoded

`kD`, `kM`, `f_NN`, and `axis` are handled well for the BallFlight trajectory equation, but the rules are not broadly semantic. Other projects might use different conventions:

```text
Cd → C_d or Cd?
CL → C_L or \mathrm{CL}?
r0 → r_0 or variable r0?
windSpeed → \mathrm{windSpeed} or wind_{Speed}?
```

A generic heuristic cannot always know the author's intent.

### Multi-line equations are accepted only when each line is equation-like

A multi-line derivation with continuation lines may be rejected because every non-empty line must independently pass detection.

For example:

```text
E = mc²
  + correction terms
```

The second line may not pass the relation requirement.

### Existing TeX delimiter detection is coarse

`hasTeXDelimiters` checks for `$$`, `\(`, and `\[`. This is simple and safe, but it does not fully parse Markdown math. It also intentionally treats a lone `$` as ambiguous because `$42.99` might be currency.

### False positives are still possible

A short `text` fence containing non-equation data with `=`, `_`, bars, and Unicode symbols could be converted accidentally. The strict scoring reduces this risk but does not eliminate it.

Example risky inputs:

```text
status = ok | latency_ms = 10
```

or logs containing mathematical-looking values.

### False negatives are expected

Some genuine equations will not be converted because they lack Unicode math or because they are ASCII-only outside a `text` fence:

```text
F = ma
E = mc^2
p = mv
```

This is intentional in the current phase: robustness against false positives is more important than maximum recall.

## Robustness improvement roadmap

The current implementation is a pragmatic heuristic. It can be made substantially more robust in several layers.

## 1. Add a tokenizer before rewriting

A tokenizer would split candidate equations into tokens such as:

- identifiers
- numbers
- operators
- relation symbols
- parentheses/brackets/braces
- bars
- Unicode symbols
- slash-separated terms
- existing TeX commands

This would improve robustness because transformations could be applied to token sequences instead of raw regex matches.

Benefits:

- Avoid converting underscores inside paths or identifiers that are clearly not math.
- Distinguish division slash from URL/path slash.
- Recognize balanced delimiters before rewriting.
- Preserve existing TeX commands safely.
- Enable better diagnostics when tokenization fails.

## 2. Parse probable equations into a small AST

A lightweight expression parser would allow safer transformations:

```text
(a+b)/(c+d) → \frac{a+b}{c+d}
d²x/dt²    → \frac{d^{2}x}{dt^{2}}
|v × ω|    → \lvert v \times \omega\rvert
```

The parser does not need full LaTeX coverage. It could support a useful subset:

- relation expressions: `lhs = rhs`, `lhs ≤ rhs`
- sums and differences
- products and implicit products
- simple fractions
- exponent/subscript attachment
- function calls
- absolute values
- derivatives

AST-based rewriting would also make idempotence easier to reason about.

## 3. Improve derivative recognition

Current derivative support is limited to single-letter variables. A stronger derivative recognizer should handle:

```text
dx/dt
 dv / dt
d²x/dt²
d^2 x / dt^2
dtheta/dt
dv⃗/dt
∂²f/∂x²
∂f/∂x_i
```

Potential output:

```tex
\frac{dx}{dt}
\frac{d^{2}x}{dt^{2}}
\frac{d\theta}{dt}
\frac{\partial^{2} f}{\partial x^{2}}
\frac{\partial f}{\partial x_i}
```

This should be grammar-based rather than a growing set of regexes.

## 4. Add a richer symbol and identifier model

The fixer needs better handling for identifiers that encode math semantics.

Potential rules:

- Greek names in ASCII: `theta`, `omega`, `lambda` → `\theta`, `\omega`, `\lambda` when strongly equation-like.
- Known physics coefficients: `Cd`, `CL`, `kD`, `kM`, `mu0`, `epsilon0`.
- Common vectors: bold or arrow notation for `v`, `r`, `a`, `ω` when explicitly configured.
- Text labels: `axis`, `drag`, `spin`, `NN`, `residual` as `\mathrm{...}`.

This should probably be configurable by domain rather than hardcoded globally.

## 5. Separate display-equation detection from inline-equation detection

Display equations and inline equations have different risk profiles.

A display-equation detector can be more aggressive when:

- content is isolated in a short paragraph or text fence;
- equation density is high;
- the line starts with a math token;
- relation operators are central;
- surrounding lines introduce it with phrases like “equation”, “where”, “model”, or “trajectory”.

Inline detection should remain stricter because false positives inside prose are more disruptive.

## 6. Use negative evidence, not only positive evidence

The current scoring model mostly adds positive evidence. A more robust model should subtract for non-math signals:

- timestamps: `2026-06-01 14:38:04`
- log levels: `INFO`, `ERROR`, `WARN`
- file paths: `/home/user/file`, `src/foo_bar.py`
- URLs: `https://...`
- JSON/YAML-like key-value lines
- shell assignments: `PATH=/usr/bin`
- table rows and CSV-like output
- stack traces

This would reduce false positives in `text` fences.

## 7. Validate generated TeX with KaTeX before applying

The browser currently renders KaTeX and reports client-side math errors. Robustness would improve if preprocessing could optionally validate generated TeX before committing to the conversion.

Possible approaches:

1. Use a server-side KaTeX parse path if a JS runtime is available.
2. Add an offline validation command for tests and exports.
3. Keep conversion but attach diagnostics if browser-side rendering fails.
4. Fall back to the original text fence when generated TeX fails validation.

A good policy would be:

```text
if detector confidence is moderate and KaTeX validation fails:
    preserve original text fence
if detector confidence is high and KaTeX validation fails:
    render original plus warning diagnostics
```

## 8. Preserve a reversible mapping to original text

A robust fixer should keep source-to-output mappings:

```text
original span → converted TeX span → rendered node
```

This would enable:

- better diagnostics;
- hover-to-see-original UI;
- safe “apply fix to source” workflows later;
- precise bug reports when a conversion is wrong.

For example, a KaTeX error could report:

```text
Generated \frac{...} from original `dv/dt`
```

rather than only showing the final TeX.

## 9. Introduce confidence bands and conversion modes

Rather than a single boolean decision, detection could produce:

- confidence score;
- detected equation kind;
- positive and negative signals;
- proposed fix list;
- diagnostics.

Then rendering modes could choose behavior:

| Mode | Behavior |
| --- | --- |
| conservative | Convert only high-confidence equations. |
| balanced | Current default; convert high and moderate confidence with safety checks. |
| aggressive | Convert more ASCII-only equations and continuation lines. |
| inspect | Show proposed fixes without applying them. |

This would support different user expectations for research notes vs. code-heavy docs.

## 10. Improve multi-line equation handling

Many equations are naturally split across lines:

```text
dv/dt = g
      − kD |v| v
      + (kM·|ω|)(axis × v)
      + f_NN(v)
```

The current detector would likely reject continuation lines because they lack relation operators. A better detector should support:

- first line contains relation;
- subsequent lines are indented or start with `+`, `−`, `-`, `=`, `&`, or alignment markers;
- all continuation lines have high math-token density;
- delimiters remain balanced across the block.

Output could use an aligned environment:

```tex
\begin{aligned}
\frac{dv}{dt} &= g \\
&\quad - k_D \lvert v\rvert v \\
&\quad + (k_M\cdot\lvert\omega\rvert)(\mathrm{axis}\times v) \\
&\quad + f_{\mathrm{NN}}(v)
\end{aligned}
```

## 11. Add golden fixtures from real documents

The BallFlight note is a useful real-world fixture. More should be added from actual research logs, including:

- physics equations;
- machine-learning notation;
- tables containing math symbols;
- code fences that must not change;
- logs that look equation-like but are not math;
- mixed Unicode and ASCII equations.

Each fixture should test both:

1. expected TeX appears;
2. unrelated text/code remains unchanged.

## 12. Add property and fuzz tests

Fuzz tests can help catch crashes and malformed output. Useful invariants:

- Preprocess never panics.
- Preprocess output is valid UTF-8.
- Running `Preprocess` twice is idempotent for converted content.
- Code fences with non-text languages remain byte-for-byte unchanged.
- Inline code spans remain unchanged.
- Generated display math has balanced `$$` delimiters.
- Generated TeX has balanced braces for simple transformations.

## 13. Make domain rules configurable

Hardcoded rules like `axis → \mathrm{axis}` are useful for the current equation but may not generalize. A future configuration layer could support project-local math conventions:

```toml
[mathfix.identifiers]
kD = "k_D"
kM = "k_M"
f_NN = "f_{\\mathrm{NN}}"
axis = "\\mathrm{axis}"
Cd = "C_D"
CL = "C_L"
```

This would let domain projects improve rendering without making global heuristics too aggressive.

## 14. Support user-visible diagnostics

A robust UX should explain why something was converted:

```text
Converted probable equation in text fence:
- relation operator: =
- derivative pattern: dv/dt
- Unicode math symbols: − · ω ×
- subscript-like identifiers: kD, kM, f_NN
```

Diagnostics should also be available for skipped candidates:

```text
Skipped text fence: likely log output; contains timestamp and no math operators.
```

This helps users trust the heuristic and report precise failures.

## 15. Consider optional LLM-assisted repair later

For high-value but ambiguous equations, an LLM can propose TeX repairs more accurately than regexes. However, this should be opt-in and privacy-aware.

A safe LLM-assisted flow would be:

1. Local heuristic detects a candidate equation.
2. User enables repair or accepts a preview.
3. Only the candidate span is sent, not the whole document.
4. The generated TeX is validated by KaTeX.
5. The UI shows original text, proposed TeX, and diagnostics.
6. The source file is modified only with explicit user approval.

This aligns with the project roadmap for future safe fix persistence and provider abstraction.

## Recommended next implementation steps

A practical incremental plan:

1. **Add negative-evidence scoring for text fences.**
   - Detect timestamps, log levels, file paths, URLs, and shell assignments.
   - Reduce false positives before adding more aggressive equation support.

2. **Support continuation lines for multi-line equations.**
   - Require the first line to be strongly equation-like.
   - Allow indented operator-starting continuation lines.
   - Emit `aligned` display math.

3. **Improve derivative parsing.**
   - Add support for second derivatives and multi-letter variables.
   - Keep this isolated behind tests.

4. **Add KaTeX validation for exported HTML or test fixtures.**
   - At minimum, add a fixture that exports the BallFlight note and checks the generated `data-tex`.

5. **Introduce diagnostics in `FixResult` for probable equation blocks.**
   - Include applied rules and confidence.
   - This will make later UI/reporting work easier.

6. **Move hardcoded domain terms into a small convention table.**
   - Start with built-ins for current behavior.
   - Leave room for project-specific config later.

## Conclusion

The current math-fixing system is a conservative, render-only heuristic that handles a useful class of LLM-style Unicode equations and short plaintext equation fences. It solves the immediate trajectory-equation problem by recognizing that the `text` fence is a display equation, then applying targeted TeX normalizations for derivatives, subscripts, absolute values, Unicode operators, Greek letters, and roman text labels.

The main robustness challenge is balancing recall against false positives. Mathematical notation is ambiguous in Markdown because code, logs, paths, tables, units, and prose can all contain symbols that look equation-like. The safest path forward is to add structure gradually: tokenization, negative evidence, lightweight parsing, validation, diagnostics, and domain-specific configuration. That keeps the current low-surprise behavior while expanding the class of equations that render well.
