// Package web holds the checking website (index.html) for "serve"; the same
// page works with detect.php on a PHP webspace.
package web

import _ "embed"

// IndexHTML is the checking page.
//
//go:embed index.html
var IndexHTML []byte
