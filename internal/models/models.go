package models

import "time"

// User represents an authenticated SyllaBuddy account in PostgreSQL.
type User struct {
	ID           int       `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	GoogleID     string    `json:"google_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// SyllabusEvent represents a single parsed course deadline, exam, or holiday.
type SyllabusEvent struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Instructor  string `json:"instructor"`
	Semester    string `json:"semester"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Date        string `json:"date"`
	Description string `json:"description"`
}

// ParserOutput holds the structured events extracted from a syllabus PDF.
type ParserOutput struct {
	Events []SyllabusEvent `json:"events"`
	Error  string          `json:"error,omitempty"`
}

// Syllabus represents an uploaded course syllabus and its parsed schedule data.
type Syllabus struct {
	ID           int           `json:"id"`
	CourseName   string        `json:"course_name"`
	UserID       int           `json:"user_id"`
	UploadDate   time.Time     `json:"upload_date"`
	FilePath     string        `json:"file_path"`
	ParserOutput *ParserOutput `json:"parser_output"`
}

// UserContext holds the authenticated user's identity extracted from JWT/session.
type UserContext struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}
