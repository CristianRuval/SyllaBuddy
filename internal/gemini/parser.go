package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"

	"syllabuddy/internal/models"
)

const (
	// ModelName is the Gemini model used for syllabus PDF extraction.
	ModelName = "gemini-2.5-flash"
	// DefaultTimeout bounds each Gemini extraction call.
	DefaultTimeout = 60 * time.Second
)

var (
	ErrMissingAPIKey  = errors.New("gemini: API key is not configured")
	ErrEmptyPDFData   = errors.New("gemini: PDF data is empty")
	ErrEmptyResponse  = errors.New("gemini: model returned empty response")
	ErrInvalidPayload = errors.New("gemini: failed to parse model JSON response")
)

const syllabusExtractionPrompt = `
You are analyzing a course syllabus. Extract the following information from the document:

1. Important Events (extract ALL dates found):
- Course name and course code (e.g., CS 4337, MATH 2418)
- Instructor/Professor name
- Semester/Term (e.g., Fall 2026, Spring 2027, F26, S27)
- Assignments (homework, projects, papers) with due dates
- Exams (midterms, finals, quizzes) with dates
- Ignore events such as holidays and breaks unless explicitly listed as a course milestone
- Ignore routine lecture dates; focus on Exams and homework/assignments/projects

For each event, extract:
- Event title/name
- Event type (assignment, exam, holiday, or other)
- Date (in format YYYY-MM-DD if possible, or as written in the syllabus)
- Description (chapter name, topic name, chapter number)

IMPORTANT INSTRUCTIONS:
- Pay special attention to tables that might contain assignment schedules or exam dates
- For dates, try to convert them to YYYY-MM-DD format when possible
- If only a month and day are given (e.g., "September 15"), infer the year from the semester
- Extract ALL dates you find, even if you're not 100% certain what they're for
- Be thorough - syllabi often have many important dates scattered throughout

Provide your answer in the following JSON format (and ONLY JSON, no other text):
{
    "events": [
        {
            "name": "course name",
            "code": "course code",
            "instructor": "instructor name",
            "semester": "semester/term",
            "title": "event name",
            "type": "assignment|exam|holiday|other",
            "date": "YYYY-MM-DD",
            "description": "additional details"
        }
    ]
}

If you cannot find certain information, use "" or "null" for that field.
`

// Parser extracts structured syllabus events from PDF documents using Gemini.
type Parser struct {
	apiKey string
}

// NewParser constructs a Parser configured with the given Gemini API key.
func NewParser(apiKey string) *Parser {
	return &Parser{apiKey: strings.TrimSpace(apiKey)}
}

// ParsePDF sends pdfData to Gemini and returns normalized events plus a derived course name fallback.
func ParsePDF(ctx context.Context, apiKey string, pdfData []byte) (*models.ParserOutput, string, error) {
	return NewParser(apiKey).ParsePDF(ctx, pdfData)
}

// ParsePDF sends pdfData to Gemini and returns normalized events plus a derived course name fallback.
func (p *Parser) ParsePDF(ctx context.Context, pdfData []byte) (*models.ParserOutput, string, error) {
	if p == nil || p.apiKey == "" {
		return nil, "", ErrMissingAPIKey
	}
	if len(pdfData) == 0 {
		return nil, "", ErrEmptyPDFData
	}

	reqCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	client, err := genai.NewClient(reqCtx, &genai.ClientConfig{
		APIKey:  p.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, "", sanitizeError("gemini: client initialization failed", err, p.apiKey)
	}

	parts := []*genai.Part{
		genai.NewPartFromBytes(pdfData, "application/pdf"),
		genai.NewPartFromText(syllabusExtractionPrompt),
	}
	contents := []*genai.Content{
		genai.NewContentFromParts(parts, genai.RoleUser),
	}
	cfg := &genai.GenerateContentConfig{
		Temperature:      genai.Ptr[float32](0.0),
		ResponseMIMEType: "application/json",
	}

	resp, err := client.Models.GenerateContent(reqCtx, ModelName, contents, cfg)
	if err != nil {
		return nil, "", sanitizeError("gemini: generate content failed", err, p.apiKey)
	}
	if resp == nil {
		return nil, "", ErrEmptyResponse
	}

	raw := resp.Text()
	if strings.TrimSpace(raw) == "" {
		return nil, "", ErrEmptyResponse
	}

	return ParseJSONResponse(raw)
}

// ParseJSONResponse strips optional markdown fences, unmarshals into models.ParserOutput,
// normalizes fields, and returns a derived course name fallback.
func ParseJSONResponse(raw string) (*models.ParserOutput, string, error) {
	cleaned := stripCodeFences(raw)
	if cleaned == "" {
		return nil, "", ErrEmptyResponse
	}

	var out models.ParserOutput
	if err := json.Unmarshal([]byte(cleaned), &out); err != nil {
		// Fallback: attempt to extract the outermost JSON object if extra prose was emitted.
		start := strings.IndexByte(cleaned, '{')
		end := strings.LastIndexByte(cleaned, '}')
		if start < 0 || end <= start || json.Unmarshal([]byte(cleaned[start:end+1]), &out) != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidPayload, err)
		}
	}

	if out.Events == nil {
		out.Events = []models.SyllabusEvent{}
	}

	var firstCode, firstName string
	for i := range out.Events {
		ev := &out.Events[i]
		ev.Name = normalizeText(ev.Name)
		ev.Code = normalizeText(ev.Code)
		ev.Instructor = normalizeText(ev.Instructor)
		ev.Semester = normalizeText(ev.Semester)
		ev.Title = normalizeText(ev.Title)
		ev.Type = normalizeEventType(ev.Type)
		ev.Date = normalizeText(ev.Date)
		ev.Description = normalizeText(ev.Description)

		if ev.Title == "" {
			ev.Title = "Untitled Event"
		}
		if firstCode == "" && ev.Code != "" {
			firstCode = ev.Code
		}
		if firstName == "" && ev.Name != "" {
			firstName = ev.Name
		}
	}

	courseName := buildCourseNameFallback(firstCode, firstName)
	return &out, courseName, nil
}

func stripCodeFences(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "```") {
		return s
	}

	// Remove opening fence line (e.g., ```json or ```).
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[idx+1:]
	} else {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
	}

	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

func normalizeText(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "null", "none", "n/a", "na", "nil":
		return ""
	default:
		return s
	}
}

func normalizeEventType(raw string) string {
	t := strings.ToLower(normalizeText(raw))
	switch t {
	case "assignment", "homework", "hw", "project", "projects", "paper", "papers", "lab", "essay":
		return "assignment"
	case "exam", "exams", "midterm", "final", "quiz", "quizzes", "test":
		return "exam"
	case "holiday", "holidays", "break":
		return "holiday"
	case "other", "":
		return "other"
	default:
		return "other"
	}
}

func buildCourseNameFallback(code, name string) string {
	switch {
	case code != "" && name != "":
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(code)) {
			return name
		}
		return code + " - " + name
	case code != "":
		return code
	case name != "":
		return name
	default:
		return ""
	}
}

func sanitizeError(prefix string, err error, secret string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
	}
	return fmt.Errorf("%s: %s", prefix, msg)
}
