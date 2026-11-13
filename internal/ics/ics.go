package ics

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"syllabuddy/internal/models"
)

// GenerateICS builds an RFC 5545 iCalendar payload for one or more syllabi.
func GenerateICS(syllabi []models.Syllabus) ([]byte, error) {
	return generateICSAt(syllabi, time.Now().UTC())
}

func generateICSAt(syllabi []models.Syllabus, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	writeLine(&buf, "BEGIN:VCALENDAR")
	writeLine(&buf, "VERSION:2.0")
	writeLine(&buf, "PRODID:-//SyllaBuddy//Academic Calendar 2.0//EN")
	writeLine(&buf, "CALSCALE:GREGORIAN")
	writeLine(&buf, "METHOD:PUBLISH")

	dtstamp := now.UTC().Format("20060102T150405Z")

	for _, syl := range syllabi {
		if syl.ParserOutput == nil || len(syl.ParserOutput.Events) == 0 {
			continue
		}
		for _, ev := range syl.ParserOutput.Events {
			dtStart, ok := normalizeICSDate(ev.Date)
			if !ok {
				continue
			}

			uid, err := newUUIDv4()
			if err != nil {
				return nil, fmt.Errorf("ics: generate UID: %w", err)
			}

			summary := formatSummary(syl.CourseName, ev)
			description := formatDescription(syl.CourseName, ev)

			writeLine(&buf, "BEGIN:VEVENT")
			writeLine(&buf, "UID:"+uid)
			writeLine(&buf, "DTSTAMP:"+dtstamp)
			writeLine(&buf, "DTSTART;VALUE=DATE:"+dtStart)
			writeLine(&buf, "SUMMARY:"+escapeText(summary))
			writeLine(&buf, "DESCRIPTION:"+escapeText(description))
			writeLine(&buf, "END:VEVENT")
		}
	}

	writeLine(&buf, "END:VCALENDAR")
	return buf.Bytes(), nil
}

func formatSummary(courseName string, ev models.SyllabusEvent) string {
	title := cleanField(ev.Title)
	if title == "" {
		title = "Untitled Event"
	}
	code := cleanField(ev.Code)
	if code == "" {
		code = cleanField(ev.Name)
	}
	if code == "" {
		code = cleanField(courseName)
	}
	if code != "" {
		return fmt.Sprintf("[%s] %s", code, title)
	}
	return title
}

func formatDescription(courseName string, ev models.SyllabusEvent) string {
	var parts []string

	course := cleanField(ev.Name)
	if course == "" {
		course = cleanField(courseName)
	}
	if course != "" {
		parts = append(parts, "Course: "+course)
	}

	if instructor := cleanField(ev.Instructor); instructor != "" {
		parts = append(parts, "Instructor: "+instructor)
	} else {
		parts = append(parts, "Instructor: Unknown")
	}

	evType := cleanField(ev.Type)
	if evType == "" {
		evType = "other"
	}
	parts = append(parts, "Type: "+evType)

	if desc := cleanField(ev.Description); desc != "" {
		parts = append(parts, "Details: "+desc)
	}

	return strings.Join(parts, "\n")
}

func cleanField(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "null") || strings.EqualFold(s, "none") || strings.EqualFold(s, "n/a") {
		return ""
	}
	return s
}

func normalizeICSDate(raw string) (string, bool) {
	raw = cleanField(raw)
	if raw == "" {
		return "", false
	}

	layouts := []string{"2006-01-02", "20060102", "2006/01/02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("20060102"), true
		}
	}
	return "", false
}

// escapeText escapes characters according to RFC 5545 section 3.3.11.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// writeLine folds lines at 75 octets per RFC 5545 section 3.1 and terminates with CRLF.
func writeLine(buf *bytes.Buffer, line string) {
	const maxOctets = 75
	if len(line) <= maxOctets {
		buf.WriteString(line)
		buf.WriteString("\r\n")
		return
	}

	first := true
	for len(line) > 0 {
		limit := maxOctets
		if !first {
			limit = maxOctets - 1 // Account for leading folding space.
		}
		if len(line) <= limit {
			if !first {
				buf.WriteByte(' ')
			}
			buf.WriteString(line)
			buf.WriteString("\r\n")
			return
		}

		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(line)
			cut = size
		}

		if !first {
			buf.WriteByte(' ')
		}
		buf.WriteString(line[:cut])
		buf.WriteString("\r\n")
		line = line[cut:]
		first = false
	}
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
