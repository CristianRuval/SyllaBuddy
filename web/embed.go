package web

import "embed"

// TemplatesFS holds all embedded HTML templates under templates/.
//
//go:embed templates/*.html
var TemplatesFS embed.FS

// StaticFS holds all embedded static assets (CSS, JS, SVG) under static/.
//
//go:embed static/* static/**/*
var StaticFS embed.FS
