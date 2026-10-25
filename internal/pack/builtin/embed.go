// Package builtin holds the packages that ship inside railswitch (D12).
package builtin

import "embed"

// FS is the built-in catalog: packs/<name>/, options/<name>/, tools/<name>/.
//
//go:embed all:packs all:options all:tools
var FS embed.FS
