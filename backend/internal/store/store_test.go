package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesJobColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var n int
	err = s.db.Get(&n, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'speaker_split'`)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("speaker_split column missing, count=%d", n)
	}

	_, err = s.CreateJob(context.Background(), "source", nil, "auto")
	if err != nil {
		t.Fatal(err)
	}

	for _, col := range []string{"preferences_override", "progress_phase", "progress_step", "progress_total"} {
		err = s.db.Get(&n, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name=?`, col)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("column %s missing, count=%d", col, n)
		}
	}
}
