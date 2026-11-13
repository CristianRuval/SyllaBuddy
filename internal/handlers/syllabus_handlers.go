package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"syllabuddy/internal/auth"
	"syllabuddy/internal/db"
	"syllabuddy/internal/ics"
	"syllabuddy/internal/models"
	"syllabuddy/internal/pdfutil"
)

var googleCalendarEventsImportURL = "https://www.googleapis.com/calendar/v3/calendars/primary/events/import"

type courseEventsGroup struct {
	SyllabusID int                    `json:"syllabus_id"`
	CourseName string                 `json:"course_name"`
	Events     []models.SyllabusEvent `json:"events"`
}

type updateEventDateRequest struct {
	Title   string `json:"title"`
	OldDate string `json:"old_date"`
	NewDate string `json:"new_date"`
	Date    string `json:"date"`
}

type syncGoogleCalendarRequest struct {
	GoogleToken string `json:"google_token"`
	SyllabusID  int    `json:"syllabus_id,omitempty"`
}

func (s *Server) handleListSyllabi(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	syllabi, err := s.DB.ListSyllabiByUser(r.Context(), user.ID)
	if err != nil {
		s.Logger.Error("list syllabi failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load syllabi")
		return
	}

	writeJSON(w, http.StatusOK, syllabi)
}

func (s *Server) handleUploadSyllabus(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	maxMultipart := s.Config.MaxUploadBytes + (1 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipart)
	if err := r.ParseMultipartForm(s.Config.MaxUploadBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "uploaded PDF exceeds maximum allowed size")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart upload payload")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing PDF file field")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, s.Config.MaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file")
		return
	}

	if err := pdfutil.ValidatePDFWithLimit(data, s.Config.MaxUploadBytes); err != nil {
		switch {
		case errors.Is(err, pdfutil.ErrPDFTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "PDF exceeds maximum upload size")
		case errors.Is(err, pdfutil.ErrEmptyPDF), errors.Is(err, pdfutil.ErrInvalidPDF):
			writeError(w, http.StatusBadRequest, "file must be a valid non-empty PDF")
		default:
			writeError(w, http.StatusBadRequest, "invalid PDF upload")
		}
		return
	}

	if err := os.MkdirAll(s.Config.UploadDir, 0o750); err != nil {
		s.Logger.Error("ensure upload directory failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to store uploaded file")
		return
	}

	uniqueName := fmt.Sprintf("%s_%s", randomHex(4), header.Filename)
	safeName := pdfutil.SafeFilename(user.ID, uniqueName)
	destPath := filepath.Join(s.Config.UploadDir, safeName)
	safeDest, err := s.resolveUploadPath(destPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload filename")
		return
	}

	if err := os.WriteFile(safeDest, data, 0o640); err != nil {
		s.Logger.Error("write uploaded pdf failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to save uploaded PDF")
		return
	}

	courseName := deriveCourseNameFromFilename(header.Filename)
	syl, err := s.DB.CreateSyllabus(r.Context(), user.ID, courseName, safeDest, nil)
	if err != nil {
		_ = os.Remove(safeDest)
		s.Logger.Error("create syllabus db row failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to record uploaded syllabus")
		return
	}

	queued := s.enqueueParseJob(parseJob{
		SyllabusID: syl.ID,
		UserID:     user.ID,
		FilePath:   safeDest,
		Fallback:   courseName,
	})
	if !queued {
		s.Logger.Warn("parse queue full or closed; storing fallback parser error", slog.Int("syllabus_id", syl.ID))
		s.storeFailedParserOutput(r.Context(), parseJob{
			SyllabusID: syl.ID,
			UserID:     user.ID,
			Fallback:   courseName,
		}, "Parse queue is busy; click Retry Parse shortly.")
	}

	writeJSON(w, http.StatusCreated, syl)
}

func (s *Server) handleReparseSyllabus(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	syl, err := s.DB.GetSyllabusByID(r.Context(), sylID, user.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "syllabus not found")
			return
		}
		s.Logger.Error("load syllabus for reparse failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load syllabus")
		return
	}

	safePath, err := s.resolveUploadPath(syl.FilePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "syllabus PDF not found")
		return
	}
	if _, statErr := os.Stat(safePath); statErr != nil {
		writeError(w, http.StatusNotFound, "syllabus PDF missing from storage")
		return
	}

	if err := s.DB.UpdateSyllabusParserOutput(r.Context(), syl.ID, user.ID, syl.CourseName, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset syllabus status")
		return
	}

	if !s.enqueueParseJob(parseJob{
		SyllabusID: syl.ID,
		UserID:     user.ID,
		FilePath:   safePath,
		Fallback:   syl.CourseName,
	}) {
		s.storeFailedParserOutput(r.Context(), parseJob{
			SyllabusID: syl.ID,
			UserID:     user.ID,
			Fallback:   syl.CourseName,
		}, "Parse queue is busy; click Retry Parse shortly.")
		writeError(w, http.StatusServiceUnavailable, "parse queue is busy")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reprocessing"})
}

func (s *Server) handleDeleteSyllabus(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	deleted, err := s.DB.DeleteSyllabus(r.Context(), sylID, user.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "syllabus not found")
			return
		}
		s.Logger.Error("delete syllabus failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to delete syllabus")
		return
	}

	s.removeSyllabusFiles(deleted)
	writeJSON(w, http.StatusOK, map[string]string{"message": "syllabus deleted"})
}

func (s *Server) handleGetThumbnail(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	syl, err := s.DB.GetSyllabusByID(r.Context(), sylID, user.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "syllabus not found")
			return
		}
		s.Logger.Error("get syllabus for thumbnail failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load thumbnail")
		return
	}

	safePath, err := s.resolveUploadPath(syl.FilePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "syllabus PDF not found")
		return
	}

	imgBytes, contentType, err := pdfutil.RenderThumbnail(r.Context(), safePath, s.thumbPathFor(syl.ID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "syllabus PDF not found")
			return
		}
		s.Logger.Error("render thumbnail failed", slog.Int("syllabus_id", syl.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to generate thumbnail")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(imgBytes)
}

func (s *Server) handleViewPDF(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	syl, err := s.DB.GetSyllabusByID(r.Context(), sylID, user.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "syllabus not found")
			return
		}
		s.Logger.Error("get syllabus for pdf view failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load PDF")
		return
	}

	safePath, err := s.resolveUploadPath(syl.FilePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "syllabus PDF not found")
		return
	}
	if _, statErr := os.Stat(safePath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "syllabus PDF not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to access PDF")
		return
	}

	dispName := sanitizeAttachmentName(syl.CourseName, "syllabus") + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, dispName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, safePath)
}

func (s *Server) handleGetAllEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	syllabi, err := s.DB.ListSyllabiByUser(r.Context(), user.ID)
	if err != nil {
		s.Logger.Error("list syllabi for events failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load events")
		return
	}

	groups := make([]courseEventsGroup, 0, len(syllabi))
	for _, syl := range syllabi {
		events := []models.SyllabusEvent{}
		if syl.ParserOutput != nil && syl.ParserOutput.Events != nil {
			events = syl.ParserOutput.Events
		}
		groups = append(groups, courseEventsGroup{
			SyllabusID: syl.ID,
			CourseName: syl.CourseName,
			Events:     events,
		})
	}

	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleGetSyllabusEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	syl, err := s.DB.GetSyllabusByID(r.Context(), sylID, user.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "syllabus not found")
			return
		}
		s.Logger.Error("get syllabus events failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load syllabus events")
		return
	}

	events := []models.SyllabusEvent{}
	if syl.ParserOutput != nil && syl.ParserOutput.Events != nil {
		events = syl.ParserOutput.Events
	}

	writeJSON(w, http.StatusOK, courseEventsGroup{
		SyllabusID: syl.ID,
		CourseName: syl.CourseName,
		Events:     events,
	})
}

func (s *Server) handleUpdateSyllabusEvent(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	var req updateEventDateRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "event title is required")
		return
	}

	newDate := strings.TrimSpace(req.NewDate)
	if newDate == "" {
		newDate = strings.TrimSpace(req.Date)
	}
	if _, ok := parseYYYYMMDD(newDate); !ok {
		writeError(w, http.StatusBadRequest, "date must be in YYYY-MM-DD format")
		return
	}

	updated, err := s.DB.UpdateSyllabusEventDate(r.Context(), sylID, user.ID, title, newDate)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, db.ErrEventNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.Logger.Error("update syllabus event failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to update event date")
		return
	}

	events := []models.SyllabusEvent{}
	if updated.ParserOutput != nil && updated.ParserOutput.Events != nil {
		events = updated.ParserOutput.Events
	}

	writeJSON(w, http.StatusOK, courseEventsGroup{
		SyllabusID: updated.ID,
		CourseName: updated.CourseName,
		Events:     events,
	})
}

func (s *Server) handleDownloadAllICS(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	syllabi, err := s.DB.ListSyllabiByUser(r.Context(), user.ID)
	if err != nil {
		s.Logger.Error("list syllabi for ics failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to generate calendar")
		return
	}

	s.serveICS(w, syllabi, "Semester_Schedule.ics")
}

func (s *Server) handleDownloadSyllabusICS(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sylID, err := parsePositiveIntParam(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid syllabus id")
		return
	}

	syl, err := s.DB.GetSyllabusByID(r.Context(), sylID, user.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "syllabus not found")
			return
		}
		s.Logger.Error("get syllabus for ics failed", slog.Int("user_id", user.ID), slog.Int("syllabus_id", sylID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to generate calendar")
		return
	}

	filename := sanitizeAttachmentName(syl.CourseName, "Course_Schedule") + ".ics"
	s.serveICS(w, []models.Syllabus{*syl}, filename)
}

func (s *Server) serveICS(w http.ResponseWriter, syllabi []models.Syllabus, filename string) {
	icsBytes, err := ics.GenerateICS(syllabi)
	if err != nil {
		s.Logger.Error("generate ics failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to generate calendar")
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(icsBytes)
}

func (s *Server) handleSyncGoogleCalendar(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req syncGoogleCalendarRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	token := strings.TrimSpace(req.GoogleToken)
	if token == "" {
		writeError(w, http.StatusBadRequest, "google_token is required")
		return
	}

	var syllabi []models.Syllabus
	if req.SyllabusID > 0 {
		syl, err := s.DB.GetSyllabusByID(r.Context(), req.SyllabusID, user.ID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				writeError(w, http.StatusNotFound, "syllabus not found")
				return
			}
			s.Logger.Error("load syllabus for google sync failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "failed to load syllabus events")
			return
		}
		syllabi = []models.Syllabus{*syl}
	} else {
		list, err := s.DB.ListSyllabiByUser(r.Context(), user.ID)
		if err != nil {
			s.Logger.Error("list syllabi for google sync failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "failed to load syllabus events")
			return
		}
		syllabi = list
	}

	type googleCalPayload struct {
		ICalUID     string            `json:"iCalUID"`
		Summary     string            `json:"summary"`
		Description string            `json:"description,omitempty"`
		Start       map[string]string `json:"start"`
		End         map[string]string `json:"end"`
	}

	var toSync []googleCalPayload
	for _, syl := range syllabi {
		if syl.ParserOutput == nil {
			continue
		}
		occCounts := make(map[string]int)
		for _, ev := range syl.ParserOutput.Events {
			startTime, valid := parseYYYYMMDD(ev.Date)
			if !valid {
				continue
			}
			endDate := startTime.AddDate(0, 0, 1).Format("2006-01-02")

			title := strings.TrimSpace(ev.Title)
			if title == "" {
				title = "Course Event"
			}
			occKey := strings.ToLower(title) + "|" + strings.ToLower(strings.TrimSpace(ev.Type))
			occIdx := occCounts[occKey]
			occCounts[occKey] = occIdx + 1

			code := strings.TrimSpace(ev.Code)
			if code == "" {
				code = strings.TrimSpace(syl.CourseName)
			}
			summary := title
			if code != "" {
				summary = fmt.Sprintf("[%s] %s", code, title)
			}

			var descParts []string
			if syl.CourseName != "" {
				descParts = append(descParts, "Course: "+syl.CourseName)
			}
			if ev.Instructor != "" {
				descParts = append(descParts, "Instructor: "+ev.Instructor)
			}
			if ev.Type != "" {
				descParts = append(descParts, "Type: "+ev.Type)
			}
			if ev.Description != "" {
				descParts = append(descParts, "Details: "+ev.Description)
			}

			toSync = append(toSync, googleCalPayload{
				ICalUID:     googleEventICalUID(user.ID, syl.ID, title, ev.Type, occIdx),
				Summary:     summary,
				Description: strings.Join(descParts, "\n"),
				Start:       map[string]string{"date": startTime.Format("2006-01-02")},
				End:         map[string]string{"date": endDate},
			})
		}
	}

	if len(toSync) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"synced":  0,
			"failed":  0,
			"message": "No dated events found to sync.",
		})
		return
	}

	synced := 0
	failed := 0
	for _, item := range toSync {
		statusCode, err := s.pushGoogleCalendarEvent(r.Context(), token, item)
		if err != nil {
			failed++
			continue
		}
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			writeError(w, http.StatusUnauthorized, "Google Calendar authorization rejected or expired; please re-authorize")
			return
		}
		if statusCode >= 200 && statusCode < 300 {
			synced++
		} else {
			failed++
		}
	}

	if synced == 0 && failed > 0 {
		writeError(w, http.StatusBadGateway, "failed to push events to Google Calendar")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"synced":  synced,
		"failed":  failed,
		"message": fmt.Sprintf("Synced %d event(s) to Google Calendar.", synced),
	})
}

func (s *Server) pushGoogleCalendarEvent(ctx context.Context, bearerToken string, payload any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, googleCalendarEventsImportURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.googleClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

func googleEventICalUID(userID, syllabusID int, title, eventType string, occIdx int) string {
	raw := fmt.Sprintf("u%d-s%d-%s-%s-%d",
		userID,
		syllabusID,
		strings.ToLower(strings.TrimSpace(title)),
		strings.ToLower(strings.TrimSpace(eventType)),
		occIdx,
	)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("sb-%x@syllabuddy.me", sum[:16])
}

func parseYYYYMMDD(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) != 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func deriveCourseNameFromFilename(filename string) string {
	norm := strings.ReplaceAll(filename, "\\", "/")
	base := filepath.Base(norm)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	stem = strings.ReplaceAll(stem, "_", " ")
	stem = strings.ReplaceAll(stem, "-", " ")
	stem = strings.Join(strings.Fields(stem), " ")
	if stem == "" || stem == "." || stem == ".." {
		return "Uploaded Syllabus"
	}
	if len(stem) > 100 {
		stem = stem[:100]
	}
	return stem
}

func sanitizeAttachmentName(raw, fallback string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('_')
		}
	}
	res := strings.Trim(b.String(), "_")
	if res == "" {
		return fallback
	}
	if len(res) > 64 {
		res = res[:64]
	}
	return res
}
