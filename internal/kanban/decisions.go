// Package kanban provides decision prompt detection for agent runs.
package kanban

import (
	"regexp"
	"strings"
)

// DecisionDetector scans agent output for human-decision prompts.
type DecisionDetector struct {
	// Patterns for detecting questions with numbered options.
	// These are heuristic patterns that catch common agent question formats.
	optionPatterns []*regexp.Regexp
}

// NewDecisionDetector creates a detector with default patterns.
func NewDecisionDetector() *DecisionDetector {
	return &DecisionDetector{
		optionPatterns: []*regexp.Regexp{
			// Numbered options: "1. Option text" or "1️⃣ Option text"
			regexp.MustCompile(`(?m)^\s*(?:\d+[.):]|\d\p{Emoji}\s)\s+.+`),
			// Lettered options: "A. Option text" or "a) Option text"
			regexp.MustCompile(`(?m)^\s*(?:[A-Da-d][.):])\s+.+`),
			// Bullet options with question prefix
			regexp.MustCompile(`(?i)(?:which|what|how|should|choose|pick|select|option|approach)\s+(?:one|would|do|you|prefer|best)\s*[?\n]`),
		},
	}
}

// Detect scans agent output and returns a decision if found.
// Returns nil if no decision prompt is detected.
func (d *DecisionDetector) Detect(text string) *DetectedDecision {
	// Look for a question followed by numbered/lettered options
	lines := strings.Split(text, "\n")

	var questionLines []string
	var options []string
	inOptions := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check if this line looks like an option
		isOption := d.isOptionLine(trimmed)

		if isOption {
			if !inOptions {
				// Previous lines form the question
				question := strings.Join(questionLines, " ")
				question = strings.TrimSpace(question)
				if len(question) > 10 {
					inOptions = true
				}
			}
			if inOptions {
				options = append(options, d.cleanOption(trimmed))
			}
		} else {
			if inOptions {
				// We were in options and now hit non-option text.
				// If we have at least 2 options, return the decision.
				if len(options) >= 2 {
					question := strings.Join(questionLines, " ")
					return &DetectedDecision{
						Question: question,
						Options:  options,
						Raw:      strings.Join(lines[max(0, i-len(options)-len(questionLines)-1):i], "\n"),
					}
				}
				// Reset and try again
				questionLines = nil
				options = nil
				inOptions = false
			}
			questionLines = append(questionLines, trimmed)
		}
	}

	// End of text: check if we have a valid decision
	if inOptions && len(options) >= 2 {
		question := strings.Join(questionLines, " ")
		return &DetectedDecision{
			Question: question,
			Options:  options,
			Raw:      strings.Join(lines, "\n"),
		}
	}

	return nil
}

// DetectedDecision represents a decision prompt found in agent output.
type DetectedDecision struct {
	Question string   // The question text
	Options  []string // Numbered/lettered options
	Raw      string   // Original text segment
}

func (d *DecisionDetector) isOptionLine(line string) bool {
	for _, re := range d.optionPatterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func (d *DecisionDetector) cleanOption(line string) string {
	// Strip leading number/letter and punctuation
	re := regexp.MustCompile(`^\s*(?:\d+[.):]|\d\p{Emoji}\s|[A-Da-d][.):])\s*`)
	return re.ReplaceAllString(line, "")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
