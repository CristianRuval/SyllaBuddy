package gemini

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseJSONResponse(t *testing.T) {
	raw := "```json\n" + `{
  "events": [
    {
      "name": "Programming Language Paradigms",
      "code": "CS 4337",
      "instructor": "null",
      "semester": "Fall 2026",
      "title": "  Homework 1  ",
      "type": "Homework",
      "date": "2026-09-14",
      "description": "N/A"
    },
    {
      "name": "Programming Language Paradigms",
      "code": "CS 4337",
      "instructor": "Dr. Smith",
      "semester": "Fall 2026",
      "title": "Midterm 1",
      "type": "midterm",
      "date": "2026-10-15",
      "description": "Ch 1-4"
    }
  ]
}` + "\n```"

	out, courseName, err := ParseJSONResponse(raw)
	if err != nil {
		t.Fatalf("ParseJSONResponse error: %v", err)
	}
	if courseName != "CS 4337 - Programming Language Paradigms" {
		t.Fatalf("courseName = %q, want %q", courseName, "CS 4337 - Programming Language Paradigms")
	}
	if len(out.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(out.Events))
	}
	if out.Events[0].Instructor != "" {
		t.Fatalf("expected 'null' instructor normalized to empty, got %q", out.Events[0].Instructor)
	}
	if out.Events[0].Description != "" {
		t.Fatalf("expected 'N/A' description normalized to empty, got %q", out.Events[0].Description)
	}
	if out.Events[0].Title != "Homework 1" {
		t.Fatalf("expected trimmed title, got %q", out.Events[0].Title)
	}
	if out.Events[0].Type != "assignment" {
		t.Fatalf("expected Homework normalized to assignment, got %q", out.Events[0].Type)
	}
	if out.Events[1].Type != "exam" {
		t.Fatalf("expected midterm normalized to exam, got %q", out.Events[1].Type)
	}
}

func TestParseJSONResponseNullEventsAndInvalid(t *testing.T) {
	out, courseName, err := ParseJSONResponse(`{"events": null}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Events == nil || len(out.Events) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v", out.Events)
	}
	if courseName != "" {
		t.Fatalf("expected empty courseName, got %q", courseName)
	}

	if _, _, err := ParseJSONResponse(`not json at all`); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("expected ErrInvalidPayload, got %v", err)
	}
}

func TestParsePDFValidationAndKeyRedaction(t *testing.T) {
	if _, _, err := ParsePDF(context.Background(), "", []byte("%PDF-1.4")); !errors.Is(err, ErrMissingAPIKey) {
		t.Fatalf("expected ErrMissingAPIKey, got %v", err)
	}
	if _, _, err := ParsePDF(context.Background(), "AIzaFakeKey12345", nil); !errors.Is(err, ErrEmptyPDFData) {
		t.Fatalf("expected ErrEmptyPDFData, got %v", err)
	}

	sanitized := sanitizeError("gemini: failed", errors.New("bad request key=AIzaFakeKey12345"), "AIzaFakeKey12345")
	if strings.Contains(sanitized.Error(), "AIzaFakeKey12345") {
		t.Fatalf("API key leaked in sanitized error: %v", sanitized)
	}
	if !strings.Contains(sanitized.Error(), "[REDACTED]") {
		t.Fatalf("expected [REDACTED] marker in %v", sanitized)
	}
}
