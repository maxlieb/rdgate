// Package frontend embeds the window's HTML.
//
// It lives beside the file it embeds because go:embed cannot reach upward
// with "..", and the desktop app's main package sits two directories below.
package frontend

import "embed"

//go:embed index.html
var Assets embed.FS
