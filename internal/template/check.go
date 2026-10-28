package template

import (
	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
)

// Status is a project's template now against what its [template] records.
type Status struct {
	Ref, SHA256 string // as recorded
	// Current is the content hash Ref resolves to now, Template its
	// content; "" and nil when it doesn't resolve (Err says why).
	Current  string
	Template *Template
	Err      error
}

// Changed reports whether the template resolves to other content than
// the project recorded: an update, shown, never applied (D111).
func (s Status) Changed() bool { return s.Current != "" && s.Current != s.SHA256 }

// Check resolves the template rec records.
func Check(rec *config.TemplateRef, d machine.Dirs, libs pack.Libraries) Status {
	s := Status{Ref: rec.Ref, SHA256: rec.SHA256}
	s.Template, s.Current, s.Err = Resolve(rec.Ref, d, libs)
	return s
}
