package pdfutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePDF(t *testing.T) {
	if err := ValidatePDF(nil); !errors.Is(err, ErrEmptyPDF) {
		t.Fatalf("expected ErrEmptyPDF for nil, got %v", err)
	}
	if err := ValidatePDF([]byte{}); !errors.Is(err, ErrEmptyPDF) {
		t.Fatalf("expected ErrEmptyPDF for empty slice, got %v", err)
	}
	if err := ValidatePDF([]byte("not a pdf")); !errors.Is(err, ErrInvalidPDF) {
		t.Fatalf("expected ErrInvalidPDF, got %v", err)
	}
	if err := ValidatePDFWithLimit([]byte("%PDF-1.7\ncontent"), 8); !errors.Is(err, ErrPDFTooLarge) {
		t.Fatalf("expected ErrPDFTooLarge, got %v", err)
	}
	if err := ValidatePDF([]byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")); err != nil {
		t.Fatalf("expected valid PDF to pass, got %v", err)
	}
}

func TestSafeFilename(t *testing.T) {
	cases := []struct {
		userID int
		input  string
		want   string
	}{
		{42, "../../etc/passwd", "user_42_passwd.pdf"},
		{7, `C:\Users\alice\Downloads\CS 4337 Syllabus (Fall 2026).pdf`, "user_7_CS_4337_Syllabus_Fall_2026_.pdf"},
		{3, "../../../evil..name.PDF", "user_3_evil.name.PDF"},
		{9, "...", "user_9_syllabus.pdf"},
		{1, "", "user_1_syllabus.pdf"},
		{5, "phys-2325_syllabus.pdf", "user_5_phys-2325_syllabus.pdf"},
	}

	for _, tc := range cases {
		got := SafeFilename(tc.userID, tc.input)
		if strings.Contains(got, "..") || strings.ContainsAny(got, `/\`) {
			t.Fatalf("unsafe path chars in %q for input %q", got, tc.input)
		}
		if !strings.HasPrefix(got, "user_") {
			t.Fatalf("expected user_ prefix, got %q", got)
		}
		if !strings.HasSuffix(strings.ToLower(got), ".pdf") {
			t.Fatalf("expected .pdf suffix, got %q", got)
		}
		if got != tc.want {
			t.Errorf("SafeFilename(%d, %q) = %q, want %q", tc.userID, tc.input, got, tc.want)
		}
	}
}

func TestRenderThumbnailCachedAndFallback(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "sample.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Missing PDF path should return an error.
	if _, _, err := RenderThumbnail(context.Background(), filepath.Join(dir, "missing.pdf"), ""); err == nil {
		t.Fatal("expected error for missing PDF path")
	}

	// Cached PNG should be returned immediately without invoking pdftoppm.
	cachedPath := filepath.Join(dir, "cached.png")
	fakePNG := append(append([]byte(nil), pngMagic...), []byte("testpngdata")...)
	if err := os.WriteFile(cachedPath, fakePNG, 0o600); err != nil {
		t.Fatal(err)
	}
	data, contentType, err := RenderThumbnail(context.Background(), pdfPath, cachedPath)
	if err != nil {
		t.Fatalf("unexpected error reading cached thumbnail: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", contentType)
	}
	if string(data) != string(fakePNG) {
		t.Fatalf("cached PNG mismatch")
	}

	// Uncached invalid/minimal PDF (or missing pdftoppm) should cleanly fall back to SVG.
	uncachedPath := filepath.Join(dir, "uncached.png")
	fbData, fbType, err := RenderThumbnail(context.Background(), pdfPath, uncachedPath)
	if err != nil {
		t.Fatalf("expected fallback instead of error, got %v", err)
	}
	if fbType != "image/svg+xml" && fbType != "image/png" {
		t.Fatalf("unexpected fallback content type: %q", fbType)
	}
	if len(fbData) == 0 {
		t.Fatal("expected non-empty thumbnail data")
	}
	if fbType == "image/svg+xml" && !strings.Contains(string(fbData), "<svg") {
		t.Fatalf("expected SVG markup in fallback")
	}
}
