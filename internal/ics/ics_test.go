package ics

import (
	"strings"
	"testing"
	"time"

	"syllabuddy/internal/models"
)

func TestGenerateICS(t *testing.T) {
	syllabi := []models.Syllabus{
		{
			ID:         1,
			CourseName: "CS 4337",
			ParserOutput: &models.ParserOutput{
				Events: []models.SyllabusEvent{
					{
						Name:        "Programming Language Paradigms",
						Code:        "CS 4337",
						Instructor:  "Dr. Jane Doe, PhD; Dept",
						Semester:    "Fall 2026",
						Title:       "Midterm Exam, Part 1",
						Type:        "exam",
						Date:        "2026-10-15",
						Description: "Chapters 1-5; closed book\\notes\nBring ID",
					},
					{
						Title: "Undated Item",
						Type:  "other",
						Date:  "null",
					},
				},
			},
		},
		{
			ID:           2,
			CourseName:   "Unparsed Course",
			ParserOutput: nil,
		},
	}

	fixedNow := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	outBytes, err := generateICSAt(syllabi, fixedNow)
	if err != nil {
		t.Fatalf("generateICSAt failed: %v", err)
	}
	out := string(outBytes)

	if !strings.Contains(out, "\r\n") {
		t.Fatal("expected CRLF line endings")
	}
	// Ensure no bare LF exists without preceding CR.
	withoutCRLF := strings.ReplaceAll(out, "\r\n", "")
	if strings.Contains(withoutCRLF, "\n") || strings.Contains(withoutCRLF, "\r") {
		t.Fatal("found unescaped bare CR or LF in ICS output")
	}

	if strings.Count(out, "BEGIN:VEVENT") != 1 {
		t.Fatalf("expected 1 VEVENT (skipping undated/nil), got %d", strings.Count(out, "BEGIN:VEVENT"))
	}
	if !strings.Contains(out, "DTSTAMP:20260901T120000Z\r\n") {
		t.Fatalf("missing expected DTSTAMP in:\n%s", out)
	}
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20261015\r\n") {
		t.Fatalf("missing expected DTSTART in:\n%s", out)
	}

	// Unfold lines for content assertions.
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(unfolded, `SUMMARY:[CS 4337] Midterm Exam\, Part 1`) {
		t.Fatalf("unexpected SUMMARY in:\n%s", unfolded)
	}
	if !strings.Contains(unfolded, `Instructor: Dr. Jane Doe\, PhD\; Dept`) {
		t.Fatalf("expected escaped comma/semicolon in DESCRIPTION:\n%s", unfolded)
	}
	if !strings.Contains(unfolded, `Details: Chapters 1-5\; closed book\\notes\nBring ID`) {
		t.Fatalf("expected escaped backslash/semicolon/newline in DESCRIPTION:\n%s", unfolded)
	}
	if !strings.HasSuffix(out, "END:VCALENDAR\r\n") {
		t.Fatalf("expected trailing END:VCALENDAR CRLF")
	}
}

func TestGenerateICSEmpty(t *testing.T) {
	outBytes, err := GenerateICS(nil)
	if err != nil {
		t.Fatalf("GenerateICS(nil) error: %v", err)
	}
	out := string(outBytes)
	if !strings.Contains(out, "BEGIN:VCALENDAR\r\n") || !strings.Contains(out, "END:VCALENDAR\r\n") {
		t.Fatalf("expected valid empty calendar envelope, got:\n%s", out)
	}
	if strings.Contains(out, "BEGIN:VEVENT") {
		t.Fatal("did not expect any VEVENT for empty input")
	}
}
