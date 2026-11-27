package themepkg

import "fmt"

// Rule ids reported in Problem.Rule (contracts/theme-package.md "Rules").
const (
	RuleSchema         = "schema"
	RuleTokensNames    = "tokens.names"
	RuleTokensFormat   = "tokens.format"
	RuleCSSSize        = "css.size"
	RuleCSSImport      = "css.import"
	RuleCSSURL         = "css.url"
	RuleCSSRemote      = "css.remote"
	RuleCSSSyntax      = "css.syntax"
	RulePreviewMissing = "preview.missing"
	RulePreviewFormat  = "preview.format"
	RuleFilesExtra     = "files.extra"
	RuleSizeTotal      = "size.total"
)

// Problem is one validation failure. Rule is one of the Rule* ids, Detail a
// caller-safe English sentence, and Line the 1-based line in the file the
// rule is about when it is known (0 otherwise).
type Problem struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
	Line   int    `json:"line,omitempty"`
}

// Error formats the problem as "rule: detail" or "rule (line N): detail".
func (p Problem) Error() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s (line %d): %s", p.Rule, p.Line, p.Detail)
	}
	return fmt.Sprintf("%s: %s", p.Rule, p.Detail)
}
