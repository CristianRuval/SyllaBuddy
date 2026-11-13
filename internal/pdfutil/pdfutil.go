package pdfutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultMaxPDFBytes is the default maximum accepted PDF upload size (15 MiB).
	DefaultMaxPDFBytes int64 = 15 * 1024 * 1024

	pdftoppmTimeout = 15 * time.Second
)

var (
	ErrEmptyPDF       = errors.New("pdfutil: file is empty")
	ErrPDFTooLarge    = errors.New("pdfutil: file exceeds maximum allowed size")
	ErrInvalidPDF     = errors.New("pdfutil: invalid PDF header")
	ErrMissingPDFPath = errors.New("pdfutil: pdf path is required")

	// Semaphore limiting concurrent pdftoppm subprocesses to protect 1GB RAM hosts.
	thumbSem = make(chan struct{}, 2)

	pdfMagic = []byte("%PDF-")
	pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

	fallbackThumbnailSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 360 460" width="360" height="460" role="img" aria-label="PDF Syllabus">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#0f172a"/>
      <stop offset="100%" stop-color="#1e293b"/>
    </linearGradient>
    <linearGradient id="accent" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%" stop-color="#2563eb"/>
      <stop offset="100%" stop-color="#38bdf8"/>
    </linearGradient>
  </defs>
  <rect width="360" height="460" rx="18" fill="url(#bg)"/>
  <rect x="28" y="28" width="304" height="404" rx="12" fill="none" stroke="#38bdf8" stroke-opacity="0.25" stroke-width="1.5"/>
  <rect x="52" y="64" width="76" height="28" rx="14" fill="url(#accent)"/>
  <text x="90" y="83" text-anchor="middle" font-family="system-ui,-apple-system,sans-serif" font-size="13" font-weight="700" fill="#ffffff" letter-spacing="1.2">PDF</text>
  <rect x="52" y="124" width="216" height="12" rx="6" fill="#94a3b8" fill-opacity="0.45"/>
  <rect x="52" y="152" width="256" height="10" rx="5" fill="#94a3b8" fill-opacity="0.28"/>
  <rect x="52" y="176" width="232" height="10" rx="5" fill="#94a3b8" fill-opacity="0.28"/>
  <rect x="52" y="200" width="190" height="10" rx="5" fill="#94a3b8" fill-opacity="0.28"/>
  <rect x="52" y="248" width="256" height="1" fill="#38bdf8" fill-opacity="0.2"/>
  <rect x="52" y="276" width="168" height="10" rx="5" fill="#38bdf8" fill-opacity="0.35"/>
  <rect x="52" y="302" width="224" height="10" rx="5" fill="#94a3b8" fill-opacity="0.22"/>
  <rect x="52" y="328" width="196" height="10" rx="5" fill="#94a3b8" fill-opacity="0.22"/>
  <text x="52" y="396" font-family="system-ui,-apple-system,sans-serif" font-size="13" font-weight="600" fill="#cbd5e1">SyllaBuddy Document</text>
</svg>`)
)

// ValidatePDF checks that data is non-empty, within DefaultMaxPDFBytes, and starts with %PDF-.
func ValidatePDF(data []byte) error {
	return ValidatePDFWithLimit(data, DefaultMaxPDFBytes)
}

// ValidatePDFWithLimit checks that data is non-empty, within maxBytes (when > 0), and starts with %PDF-.
func ValidatePDFWithLimit(data []byte, maxBytes int64) error {
	if len(data) == 0 {
		return ErrEmptyPDF
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return fmt.Errorf("%w (%d > %d bytes)", ErrPDFTooLarge, len(data), maxBytes)
	}
	if !bytes.HasPrefix(data, pdfMagic) {
		return ErrInvalidPDF
	}
	return nil
}

// SafeFilename strips directory components from original, sanitizes to safe ASCII characters,
// prevents path traversal, and prefixes with user_<userID>_.
func SafeFilename(userID int, original string) string {
	// Normalize Windows and POSIX separators before extracting basename.
	normalized := strings.ReplaceAll(original, "\\", "/")
	base := path.Base(normalized)
	if base == "." || base == "/" || base == ".." {
		base = ""
	}

	var b strings.Builder
	b.Grow(len(base))
	lastUnderscore := false
	for i := 0; i < len(base); i++ {
		c := base[i]
		isSafe := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.'
		if isSafe {
			b.WriteByte(c)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	cleaned := b.String()
	// Collapse any dot runs to prevent ".." inside the filename.
	for strings.Contains(cleaned, "..") {
		cleaned = strings.ReplaceAll(cleaned, "..", ".")
	}
	cleaned = strings.Trim(cleaned, "._-")

	if cleaned == "" {
		cleaned = "syllabus.pdf"
	} else if !strings.HasSuffix(strings.ToLower(cleaned), ".pdf") {
		cleaned += ".pdf"
	}

	// Keep filenames bounded to avoid OS path-length limits.
	if len(cleaned) > 120 {
		stem := strings.TrimSuffix(cleaned, filepath.Ext(cleaned))
		if len(stem) > 116 {
			stem = stem[:116]
		}
		cleaned = strings.TrimRight(stem, "._-") + ".pdf"
	}

	return fmt.Sprintf("user_%d_%s", userID, cleaned)
}

// RenderThumbnail returns a cached PNG thumbnail if available, otherwise renders page 1 of pdfPath
// using pdftoppm under a memory-protecting semaphore. If pdftoppm is unavailable or fails, it
// returns a lightweight SVG fallback with content type "image/svg+xml".
func RenderThumbnail(ctx context.Context, pdfPath, thumbPath string) ([]byte, string, error) {
	if strings.TrimSpace(pdfPath) == "" {
		return nil, "", ErrMissingPDFPath
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, "", err
	}

	if thumbPath != "" {
		if cached, err := os.ReadFile(thumbPath); err == nil && bytes.HasPrefix(cached, pngMagic) {
			return cached, "image/png", nil
		}
	}

	select {
	case thumbSem <- struct{}{}:
		defer func() { <-thumbSem }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}

	// Re-check cache after acquiring semaphore in case a concurrent caller just generated it.
	if thumbPath != "" {
		if cached, err := os.ReadFile(thumbPath); err == nil && bytes.HasPrefix(cached, pngMagic) {
			return cached, "image/png", nil
		}
	}

	pngBytes, err := renderWithPdftoppm(ctx, pdfPath, thumbPath)
	if err != nil {
		return append([]byte(nil), fallbackThumbnailSVG...), "image/svg+xml", nil
	}
	return pngBytes, "image/png", nil
}

func renderWithPdftoppm(ctx context.Context, pdfPath, thumbPath string) ([]byte, error) {
	bin, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, err
	}

	var outBase string
	var outPNG string
	var cleanupDir string

	if thumbPath != "" {
		if mkErr := os.MkdirAll(filepath.Dir(thumbPath), 0o750); mkErr != nil {
			return nil, mkErr
		}
		outBase = strings.TrimSuffix(thumbPath, filepath.Ext(thumbPath))
		outPNG = outBase + ".png"
	} else {
		tmpDir, mkErr := os.MkdirTemp("", "syllabuddy-thumb-*")
		if mkErr != nil {
			return nil, mkErr
		}
		cleanupDir = tmpDir
		defer os.RemoveAll(cleanupDir)
		outBase = filepath.Join(tmpDir, "thumb")
		outPNG = outBase + ".png"
	}

	cmdCtx, cancel := context.WithTimeout(ctx, pdftoppmTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, bin, "-png", "-f", "1", "-l", "1", "-scale-to", "360", "-singlefile", pdfPath, outBase)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w (%s)", runErr, strings.TrimSpace(string(output)))
	}

	data, err := os.ReadFile(outPNG)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, pngMagic) {
		return nil, errors.New("pdftoppm produced invalid PNG output")
	}

	if thumbPath != "" && outPNG != thumbPath {
		_ = os.Rename(outPNG, thumbPath)
	}
	return data, nil
}
