package workspace

import (
	"crypto/sha256"
	"fmt"

	"github.com/jbigler/railswitch/internal/buildinfo"
)

// EjectedContainerfile is the workspace copy of package ref's default
// Containerfile.<name>: def under a header naming where it came from (D32),
// which containerfile show compares against the default.
func EjectedContainerfile(ref, name string, def []byte) []byte {
	header := fmt.Sprintf("# Ejected from %s Containerfile.%s (sha256 %x) by railswitch %s. Edit freely: railswitch builds this file.\n", ref, name, sha256.Sum256(def), buildinfo.Version)
	return append([]byte(header), def...)
}
