package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupExpiredJobImagesDeletesTaskFiles(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	output := t.TempDir()
	rel := "2026/05/17/512/0304/20260517_000854_512_0304.jpg"

	writeTestFile(t, filepath.Join(output, filepath.FromSlash(rel)))
	writeTestFile(t, filepath.Join(output, ".thumbnails", filepath.FromSlash(rel)))

	app := testCleanupApp(output)
	job := &downloadJob{
		ID:       "expired",
		Status:   "done",
		Created:  now.Add(-13 * time.Hour),
		Finished: now.Add(-12 * time.Hour),
		Logs:     []string{},
		Paths:    []string{rel},
	}
	app.jobs.add(job)

	app.cleanupExpiredJobImages(now)

	assertMissing(t, filepath.Join(output, filepath.FromSlash(rel)))
	assertMissing(t, filepath.Join(output, ".thumbnails", filepath.FromSlash(rel)))
	if job.CleanedAt.IsZero() {
		t.Fatal("expected job cleanup timestamp to be set")
	}
}

func TestCleanupExpiredJobImagesProtectsImagesReferencedByNewerJobs(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	output := t.TempDir()
	rel := "2026/05/17/512/0304/20260517_000854_512_0304.jpg"
	abs := filepath.Join(output, filepath.FromSlash(rel))
	writeTestFile(t, abs)

	app := testCleanupApp(output)
	expired := &downloadJob{
		ID:       "expired",
		Status:   "done",
		Created:  now.Add(-13 * time.Hour),
		Finished: now.Add(-12 * time.Hour),
		Logs:     []string{},
		Paths:    []string{rel},
	}
	newer := &downloadJob{
		ID:       "newer",
		Status:   "done",
		Created:  now.Add(-2 * time.Hour),
		Finished: now.Add(-time.Hour),
		Logs:     []string{},
		Paths:    []string{rel},
	}
	app.jobs.add(expired)
	app.jobs.add(newer)

	app.cleanupExpiredJobImages(now)

	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("expected shared image to be kept: %v", err)
	}
	if expired.CleanedAt.IsZero() {
		t.Fatal("expected expired job to be marked cleaned after deferring shared image")
	}
	if !newer.CleanedAt.IsZero() {
		t.Fatal("newer job should not be marked cleaned before retention window")
	}
}

func testCleanupApp(output string) *app {
	return &app{
		cfg: serverConfig{
			output:          output,
			imageRetention:  imageRetention,
			cleanupInterval: time.Second,
		},
		jobs: newJobStore(filepath.Join(output, ".lumara-jobs.json")),
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create test dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be removed, got %v", path, err)
	}
}
