// Package frontend embeds the built web UI. Run "npm run build" here
// first; without it the app serves a placeholder.
package frontend

import "embed"

//go:embed all:dist
var Dist embed.FS
