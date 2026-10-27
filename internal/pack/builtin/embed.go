// Package builtin holds the packages that ship inside railswitch (D12).
package builtin

import "embed"

// FS is the built-in catalog: packs/<name>/, options/<name>/, tools/<name>/,
// agents/<name>/, and agentbase/Containerfile.agent-base, the image every
// agent package builds on.
//
//go:embed all:packs all:options all:tools all:agents all:agentbase
var FS embed.FS
