package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"syllabuddy/internal/models"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	d, err := Connect(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
	return d
}

func TestUserAndGoogleLifecycle(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	u, err := d.CreateUser(ctx, "alice", "Alice@Example.com", "hashed-pw")
	if err != nil {
		t.Fatalf("CreateUser() error: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Fatalf("expected lowercased email, got %q", u.Email)
	}

	if _, err := d.CreateUser(ctx, "alice", "other@example.com", "pw"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict for duplicate username, got %v", err)
	}

	linked, err := d.CreateOrLinkGoogleUser(ctx, "Alice", "alice@example.com", "google-sub-123")
	if err != nil {
		t.Fatalf("CreateOrLinkGoogleUser() error: %v", err)
	}
	if linked.ID != u.ID || linked.GoogleID != "google-sub-123" {
		t.Fatalf("expected linked existing user %d, got %+v", u.ID, linked)
	}

	fresh, err := d.CreateOrLinkGoogleUser(ctx, "Bob", "bob@example.com", "google-sub-456")
	if err != nil {
		t.Fatalf("CreateOrLinkGoogleUser() new user error: %v", err)
	}
	if fresh.ID == u.ID || fresh.Username != "Bob" {
		t.Fatalf("unexpected fresh google user: %+v", fresh)
	}
}

func TestSyllabusOwnershipAndEventUpdate(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	owner, _ := d.CreateUser(ctx, "owner", "owner@example.com", "pw")
	other, _ := d.CreateUser(ctx, "other", "other@example.com", "pw")

	out := &models.ParserOutput{
		Events: []models.SyllabusEvent{
			{Title: "Midterm 1", Type: "exam", Date: "2026-10-15"},
		},
	}
	syl, err := d.CreateSyllabus(ctx, owner.ID, "CS 4337", "/app/uploads/cs4337.pdf", out)
	if err != nil {
		t.Fatalf("CreateSyllabus() error: %v", err)
	}

	if _, err := d.GetSyllabusByID(ctx, syl.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner read, got %v", err)
	}
	if _, err := d.UpdateSyllabusEventDate(ctx, syl.ID, other.ID, "Midterm 1", "2026-10-20"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner event edit, got %v", err)
	}

	updated, err := d.UpdateSyllabusEventDate(ctx, syl.ID, owner.ID, "Midterm 1", "2026-10-20")
	if err != nil {
		t.Fatalf("UpdateSyllabusEventDate() error: %v", err)
	}
	if updated.ParserOutput.Events[0].Date != "2026-10-20" {
		t.Fatalf("expected updated date 2026-10-20, got %q", updated.ParserOutput.Events[0].Date)
	}

	if _, err := d.DeleteSyllabus(ctx, syl.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner delete, got %v", err)
	}
}
