// Package filter decides whether a job is interesting, using a case-insensitive
// keyword match against the job title and description. Kept deliberately simple
// for the MVP; scoring/ranking can be layered on later (see roadmap.md).
package filter

import (
	"strings"

	"hireme/internal/jsearch"
)

// Filter matches jobs against a configured keyword list.
type Filter struct {
	keywords []string // pre-lowercased
}

// New builds a Filter from raw keywords (matching is case-insensitive).
func New(keywords []string) *Filter {
	lowered := make([]string, 0, len(keywords))
	for _, k := range keywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			lowered = append(lowered, k)
		}
	}
	return &Filter{keywords: lowered}
}

// Match reports whether the job matches any keyword and returns the first
// keyword that matched (useful for logging why a job was kept).
func (f *Filter) Match(j jsearch.Job) (matched bool, keyword string) {
	haystack := strings.ToLower(j.Title + "\n" + j.Description)
	for _, k := range f.keywords {
		if strings.Contains(haystack, k) {
			return true, k
		}
	}
	return false, ""
}
