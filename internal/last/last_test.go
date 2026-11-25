package last

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordGetLastAndSave(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	s := Load(dir)
	if _, _, ok := s.Last("/repo"); ok {
		t.Fatal("empty store has a last command")
	}
	s.Record("/repo", "global:deploy", map[string]string{"env": "prod", "token": "s3cret"}, []string{"token"}, now)
	s.Record("/repo", "maven:test", map[string]string{"module": "api"}, nil, now.Add(time.Minute))
	s.Record("/other", "global:deploy", map[string]string{"env": "dev"}, nil, now)
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	s = Load(dir)
	id, e, ok := s.Last("/repo")
	if !ok || id != "maven:test" || e.Values["module"] != "api" {
		t.Errorf("Last = %q %+v %v", id, e, ok)
	}
	e, ok = s.Get("/repo", "global:deploy")
	if !ok || e.Values["env"] != "prod" || !e.At.Equal(now) {
		t.Errorf("Get = %+v %v", e, ok)
	}
	if _, has := e.Values["token"]; has {
		t.Error("a secret was stored")
	}
	if e, _ := s.Get("/other", "global:deploy"); e.Values["env"] != "dev" {
		t.Errorf("repos mixed up: %+v", e)
	}
	if _, ok := s.Get("/nowhere", "x"); ok {
		t.Error("Get found an entry for an unknown repo")
	}
}

func TestSaveIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Load(dir)
	s.Record("/r", "x", map[string]string{"a": "1"}, nil, time.Now())
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 even over an existing 0644 file", info.Mode().Perm())
	}
}

func TestCorruptOrOddFileStartsEmpty(t *testing.T) {
	for _, body := range []string{"{not json", `{"repos":{"/r":null}}`, `{"repos":{"/r":{"last":"x"}}}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		s := Load(dir)
		if _, _, ok := s.Last("/r"); ok {
			t.Errorf("%q: Last found something", body)
		}
		s.Record("/r", "y", nil, nil, time.Now()) // must not panic
	}
}
