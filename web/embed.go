package webassets

import "embed"

// Dist contains the production Vite build. The frontend is compiled before the
// Go binary, so the deployed service is a single self-contained executable.
//
//go:embed all:dist
var Dist embed.FS
