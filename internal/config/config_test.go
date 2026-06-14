package config

import (
	"strings"
	"testing"
)

// setSecrets sets the three required vars so Load() passes validation; tests can
// then focus on search parsing. t.Setenv also isolates each test's environment.
func setSecrets(t *testing.T) {
	t.Helper()
	t.Setenv("JSEARCH_API_KEY", "k")
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok")
	t.Setenv("TELEGRAM_CHAT_ID", "1")
}

func TestLoad_LegacySingleQueryFallback(t *testing.T) {
	setSecrets(t)
	t.Setenv("JOB_QUERY", "data engineer remote")
	t.Setenv("KEYWORDS", "python, airflow")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Searches) != 1 {
		t.Fatalf("want 1 search, got %d", len(cfg.Searches))
	}
	if cfg.Searches[0].Query != "data engineer remote" {
		t.Fatalf("query = %q", cfg.Searches[0].Query)
	}
	if got := cfg.Searches[0].Keywords; len(got) != 2 || got[0] != "python" || got[1] != "airflow" {
		t.Fatalf("keywords = %v", got)
	}
}

func TestLoad_NumberedQueriesTakePrecedenceAndStopAtGap(t *testing.T) {
	setSecrets(t)
	t.Setenv("JOB_QUERY", "ignored legacy")
	t.Setenv("JOB_QUERY_1", "devops remote")
	t.Setenv("KEYWORDS_1", "devops,k8s")
	t.Setenv("JOB_QUERY_2", "react native remote")
	t.Setenv("KEYWORDS_2", "react native,expo")
	// Gap: _3 missing, so _4 must be ignored.
	t.Setenv("JOB_QUERY_4", "dropped")
	t.Setenv("KEYWORDS_4", "x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Searches) != 2 {
		t.Fatalf("want 2 searches (legacy ignored, gap stops scan), got %d: %+v", len(cfg.Searches), cfg.Searches)
	}
	if cfg.Searches[0].Query != "devops remote" || cfg.Searches[1].Query != "react native remote" {
		t.Fatalf("queries = %q, %q", cfg.Searches[0].Query, cfg.Searches[1].Query)
	}
}

func TestLoad_NumberedQueryMissingKeywordsIsError(t *testing.T) {
	setSecrets(t)
	t.Setenv("JOB_QUERY_1", "devops remote")
	// KEYWORDS_1 deliberately unset.

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "KEYWORDS_1") {
		t.Fatalf("expected a missing-KEYWORDS_1 error, got %v", err)
	}
}
