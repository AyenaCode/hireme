// Package config loads and validates runtime configuration from the
// environment. All knobs are env vars so the same binary runs unchanged on a
// laptop, in Docker, or on a VPS. A local .env file is loaded if present
// (convenience for development only; never commit real secrets).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Search is one query plus the local keyword filter applied to its results.
// Multiple searches run per cycle (e.g. one for DevOps, one for React Native),
// each with its own keyword set; the freshness/remote/locale knobs are shared.
type Search struct {
	Query    string   // JSearch `query`, always includes a location term
	Keywords []string // local match filter against title + description
}

// Config holds every runtime setting. Secrets are kept here, never hard-coded.
type Config struct {
	// Secrets / endpoints
	JSearchAPIKey    string
	TelegramBotToken string
	TelegramChatID   string

	// Search behaviour
	Searches   []Search // one or more (query, keywords) pairs run each cycle
	DatePosted string   // JSearch freshness filter: all|today|3days|week|month
	Country    string   // optional, e.g. "fr"
	Language   string   // optional, e.g. "fr"
	RemoteOnly bool     // maps to work_from_home=true

	// Scheduling
	PollInterval  time.Duration // ticker period; default 5h keeps us in the free tier
	RunOnce       bool          // run a single cycle then exit (CLI / testing / cron)
	MaxPages      int           // pages to fetch per cycle via the JSearch cursor; 1 = first page only
	RequestBudget int           // max JSearch requests/month before pausing; 0 = unlimited (guard off)

	// Storage
	DBPath string
}

// Load reads configuration from the environment, applying defaults and then
// validating. It returns an error listing every problem at once rather than
// failing on the first, so misconfiguration is fixed in a single pass.
func Load() (*Config, error) {
	loadDotEnv(".env")

	cfg := &Config{
		JSearchAPIKey:    os.Getenv("JSEARCH_API_KEY"),
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
		Searches:         loadSearches(),
		DatePosted:       getEnv("DATE_POSTED", "today"),
		Country:          os.Getenv("JOB_COUNTRY"),
		Language:         os.Getenv("JOB_LANGUAGE"),
		RemoteOnly:       getEnvBool("REMOTE_ONLY", true),
		RunOnce:          getEnvBool("RUN_ONCE", false),
		DBPath:           getEnv("DB_PATH", "data/jobs.db"),
	}

	interval, err := time.ParseDuration(getEnv("POLL_INTERVAL", "5h"))
	if err != nil {
		return nil, fmt.Errorf("POLL_INTERVAL is not a valid duration (e.g. 5h, 90m): %w", err)
	}
	cfg.PollInterval = interval

	pages, err := strconv.Atoi(getEnv("MAX_PAGES", "1"))
	if err != nil {
		return nil, fmt.Errorf("MAX_PAGES is not an integer: %w", err)
	}
	cfg.MaxPages = pages

	budget, err := strconv.Atoi(getEnv("MONTHLY_REQUEST_BUDGET", "200"))
	if err != nil {
		return nil, fmt.Errorf("MONTHLY_REQUEST_BUDGET is not an integer: %w", err)
	}
	cfg.RequestBudget = budget

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.JSearchAPIKey == "" {
		missing = append(missing, "JSEARCH_API_KEY")
	}
	if c.TelegramBotToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if c.TelegramChatID == "" {
		missing = append(missing, "TELEGRAM_CHAT_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if c.PollInterval < time.Minute {
		return fmt.Errorf("POLL_INTERVAL too small (%s); minimum is 1m to avoid burning the API quota", c.PollInterval)
	}
	if len(c.Searches) == 0 {
		return fmt.Errorf("no search configured; set JOB_QUERY (+ KEYWORDS) or JOB_QUERY_1 (+ KEYWORDS_1), …")
	}
	for i, s := range c.Searches {
		if s.Query == "" {
			return fmt.Errorf("search #%d has an empty query", i+1)
		}
		if len(s.Keywords) == 0 {
			return fmt.Errorf("search #%d (query %q) has no keywords; set KEYWORDS_%d", i+1, s.Query, i+1)
		}
	}
	if c.MaxPages < 1 {
		return fmt.Errorf("MAX_PAGES must be >= 1 (got %d)", c.MaxPages)
	}
	if c.RequestBudget < 0 {
		return fmt.Errorf("MONTHLY_REQUEST_BUDGET must be >= 0 (got %d; 0 disables the guard)", c.RequestBudget)
	}
	return nil
}

// loadSearches reads the search list. Numbered vars (JOB_QUERY_1 + KEYWORDS_1,
// JOB_QUERY_2 + …) take precedence and are scanned contiguously until the first
// gap. If none are set it falls back to the legacy single JOB_QUERY + KEYWORDS
// (with the historical defaults), so existing deployments are unchanged.
func loadSearches() []Search {
	var out []Search
	for i := 1; ; i++ {
		q := strings.TrimSpace(os.Getenv(fmt.Sprintf("JOB_QUERY_%d", i)))
		if q == "" {
			break
		}
		out = append(out, Search{Query: q, Keywords: splitCSV(os.Getenv(fmt.Sprintf("KEYWORDS_%d", i)))})
	}
	if len(out) > 0 {
		return out
	}
	return []Search{{
		Query:    getEnv("JOB_QUERY", "devops engineer remote"),
		Keywords: splitCSV(getEnv("KEYWORDS", "devops,platform engineer,kubernetes,site reliability,react native,expo")),
	}}
}

// Redacted returns a log-safe summary that never exposes secrets.
func (c *Config) Redacted() string {
	searches := make([]string, len(c.Searches))
	for i, s := range c.Searches {
		searches[i] = fmt.Sprintf("%q(kw=%d)", s.Query, len(s.Keywords))
	}
	return fmt.Sprintf(
		"searches=[%s] date_posted=%s remote_only=%t poll=%s run_once=%t max_pages=%d req_budget=%d db=%s country=%q",
		strings.Join(searches, ", "), c.DatePosted, c.RemoteOnly, c.PollInterval, c.RunOnce, c.MaxPages, c.RequestBudget, c.DBPath, c.Country,
	)
}

// --- helpers ---------------------------------------------------------------

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadDotEnv loads KEY=VALUE pairs from a file into the environment without
// overriding variables already set in the real environment (real env wins).
// Missing file is not an error. This is a minimal, dependency-free loader; it
// supports `export KEY=val`, `#` comments, and optional single/double quotes.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // no .env is fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
