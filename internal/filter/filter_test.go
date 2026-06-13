package filter

import (
	"testing"

	"hireme/internal/jsearch"
)

func TestMatch(t *testing.T) {
	f := New([]string{"DevOps", "react native", "kubernetes"})

	tests := []struct {
		name    string
		job     jsearch.Job
		want    bool
		wantKey string
	}{
		{
			name:    "title match, case-insensitive",
			job:     jsearch.Job{Title: "Senior DEVOPS Engineer"},
			want:    true,
			wantKey: "devops",
		},
		{
			name:    "description match",
			job:     jsearch.Job{Title: "Mobile Dev", Description: "Experience with React Native and Expo"},
			want:    true,
			wantKey: "react native",
		},
		{
			name: "no match",
			job:  jsearch.Job{Title: "Accountant", Description: "spreadsheets"},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, key := f.Match(tc.job)
			if got != tc.want {
				t.Fatalf("Match() = %v, want %v", got, tc.want)
			}
			if tc.want && key != tc.wantKey {
				t.Fatalf("keyword = %q, want %q", key, tc.wantKey)
			}
		})
	}
}

func TestNew_TrimsAndLowercases(t *testing.T) {
	f := New([]string{"  Kubernetes  ", "", "   "})
	if len(f.keywords) != 1 || f.keywords[0] != "kubernetes" {
		t.Fatalf("expected [kubernetes], got %v", f.keywords)
	}
}
