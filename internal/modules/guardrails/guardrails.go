// Package guardrails provides the platform-side, config-driven safety
// hooks borrowed from the OpenAI Agents API model: input tripwires that
// reject a batch before the model runs, and output redaction applied
// before an agent message is persisted. These are deployment extensions,
// not part of the public Bigmodel contract.
package guardrails

import (
	"regexp"

	"wave-ai.local/wave/internal/platform/apierr"
)

type Set struct {
	inputBlock   []*regexp.Regexp
	outputRedact []*regexp.Regexp
}

// New compiles the configured patterns; invalid patterns fail startup
// (fail-closed: a typo'd guardrail must not silently disable protection).
func New(inputBlock, outputRedact []string) (*Set, error) {
	s := &Set{}
	for _, pat := range inputBlock {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, apierr.InternalErr(err, "guardrail input pattern %q: %v", pat, err)
		}
		s.inputBlock = append(s.inputBlock, re)
	}
	for _, pat := range outputRedact {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, apierr.InternalErr(err, "guardrail output pattern %q: %v", pat, err)
		}
		s.outputRedact = append(s.outputRedact, re)
	}
	return s, nil
}

// Empty reports whether no guardrails are configured (fast path).
func (s *Set) Empty() bool { return s == nil || (len(s.inputBlock) == 0 && len(s.outputRedact) == 0) }

// CheckTextInput returns an error when any pattern trips on the given
// user-visible text. Callers reject the whole batch (atomic admission).
// A nil set passes everything.
func (s *Set) CheckTextInput(text string) error {
	if s == nil {
		return nil
	}
	for _, re := range s.inputBlock {
		if re.MatchString(text) {
			return apierr.Invalid("input rejected by platform guardrail (pattern %q)", re.String())
		}
	}
	return nil
}

// RedactOutput rewrites matching substrings in agent output before the
// message is persisted. It never fails; unmatched text passes through.
// A nil set returns text unchanged.
func (s *Set) RedactOutput(text string) string {
	if s == nil {
		return text
	}
	for _, re := range s.outputRedact {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}
