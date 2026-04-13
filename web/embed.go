package web

import "embed"

// Dist contains the built React static files.
// In development, this will contain a placeholder index.html.
//
//go:embed all:dist
var Dist embed.FS
