package store

import (
	"context"
	"path/filepath"
	"testing"

	"hireme/internal/jsearch"
)

func TestSaveNew_Dedup(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	job := jsearch.Job{JobID: "abc123", Title: "DevOps Engineer", Employer: "Acme"}

	isNew, err := st.SaveNew(ctx, job)
	if err != nil {
		t.Fatalf("first SaveNew: %v", err)
	}
	if !isNew {
		t.Fatal("first insert should be new")
	}

	isNew, err = st.SaveNew(ctx, job)
	if err != nil {
		t.Fatalf("second SaveNew: %v", err)
	}
	if isNew {
		t.Fatal("second insert of same job_id should NOT be new (dedup)")
	}
}

func TestMarkNotified(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	job := jsearch.Job{JobID: "x", Title: "t"}
	if _, err := st.SaveNew(ctx, job); err != nil {
		t.Fatalf("SaveNew: %v", err)
	}
	if err := st.MarkNotified(ctx, "x"); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}

	var notified int
	if err := st.db.QueryRowContext(ctx, `SELECT notified FROM jobs WHERE job_id = ?`, "x").Scan(&notified); err != nil {
		t.Fatalf("query: %v", err)
	}
	if notified != 1 {
		t.Fatalf("notified = %d, want 1", notified)
	}
}
