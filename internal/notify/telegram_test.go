package notify

import (
	"strings"
	"testing"

	"hireme/internal/jsearch"
)

func TestEsc(t *testing.T) {
	got := esc(`A & B <tag> "x"`)
	want := `A &amp; B &lt;tag&gt; "x"`
	if got != want {
		t.Fatalf("esc() = %q, want %q", got, want)
	}
}

func TestFormatJob_EscapesAndIncludesLink(t *testing.T) {
	min := 90000.0
	j := jsearch.Job{
		Title:        "DevOps <Lead> & SRE",
		Employer:     "Acme",
		Location:     "Paris",
		Remote:       true,
		EmployType:   "FULLTIME",
		Publisher:    "linkedin",
		ApplyLink:    "https://example.com/apply?a=1&b=2",
		MinSalary:    &min,
		SalaryPeriod: "YEAR",
	}
	out := formatJob(j)

	if strings.Contains(out, "<Lead>") || strings.Contains(out, "& SRE") {
		t.Fatalf("title not escaped: %s", out)
	}
	if !strings.Contains(out, "&lt;Lead&gt;") {
		t.Fatalf("expected escaped title, got: %s", out)
	}
	if !strings.Contains(out, "Paris · Remote") {
		t.Fatalf("expected remote location, got: %s", out)
	}
	if !strings.Contains(out, `href="https://example.com/apply?a=1&amp;b=2"`) {
		t.Fatalf("expected escaped apply link, got: %s", out)
	}
}
