// Package buildinfo holds railswitch's build-time identity. It imports
// nothing, so every package can read it.
package buildinfo

// Version is set at build time with
// -ldflags "-X github.com/jbigler/railswitch/internal/buildinfo.Version=…".
var Version = "dev"
