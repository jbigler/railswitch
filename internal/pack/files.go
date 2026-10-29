package pack

import (
	"fmt"
	"regexp"
	"strings"
)

// fileNameRe is a file name in entrypoints/ or initdb/, and the <name> of
// Containerfile.<name> and <name>.sh (D93).
var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name is a package name (a directory under
// packs/, options/ or agents/).
func ValidName(name string) bool { return nameRe.MatchString(name) }

// CheckFilePath checks rel, a slash path in a package whose manifest is
// manifest, against the package format's files (D93): at the top the
// manifest, Containerfile.<name>, deps.allow, README.md and <name>.sh; in
// entrypoints/ and initdb/ any <file>. Nothing else, so no "..", no
// hidden file and no deeper directory.
func CheckFilePath(rel, manifest string) error {
	top, file, nested := strings.Cut(rel, "/")
	ok := false
	if manifest == "agent.toml" {
		// An agent's package also holds the files its mounts name
		// (agent.toml mounts), at the top: any plain name.
		if nested {
			ok = top == "entrypoints" && fileNameRe.MatchString(file)
		} else {
			ok = fileNameRe.MatchString(top)
		}
	} else if nested {
		ok = (top == "entrypoints" || top == "initdb") && fileNameRe.MatchString(file)
	} else {
		cf, isCF := strings.CutPrefix(top, "Containerfile.")
		stem, isSh := strings.CutSuffix(top, ".sh")
		switch {
		case top == manifest, top == "deps.allow", top == "app.allow", top == "README.md":
			ok = true
		case isCF:
			ok = fileNameRe.MatchString(cf)
		case isSh:
			ok = fileNameRe.MatchString(stem)
		}
	}
	if !ok {
		if manifest == "agent.toml" {
			return fmt.Errorf("%q is not a package file: an agent's are a <file> at the top or entrypoints/<file>", rel)
		}
		return fmt.Errorf("%q is not a package file: the manifest (%s), Containerfile.<name>, deps.allow, app.allow, README.md or <name>.sh at the top, or entrypoints/<file> or initdb/<file>", rel, manifest)
	}
	return nil
}
