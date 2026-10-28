package review

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/textdiff"
)

// manifest is what the checks read from a package manifest.
type manifest struct {
	allow     []string // agent allow
	firewall  []string // option and tool firewall profiles
	pull      string   // option [image] pull
	imageFile string   // pack [image] file
	mounts    []pack.Mount
	ports     []pack.Port
	caps      []string // [unit] add_capability
	err       error    // the manifest does not load
}

// decode reads a kind's manifest strictly into the package format's types,
// so a reordering or reformatting changes nothing.
func decode(kind string, data []byte) manifest {
	var (
		v any
		p pack.Pack
		o pack.Option
		t pack.Tool
		a pack.Agent
	)
	switch kind {
	case "pack":
		v = &p
	case "option":
		v = &o
	case "tool":
		v = &t
	case "agent":
		v = &a
	default:
		return manifest{err: fmt.Errorf("unknown package kind %q", kind)}
	}
	md, err := toml.Decode(string(data), v)
	if err == nil && len(md.Undecoded()) > 0 {
		err = fmt.Errorf("unknown key %s", md.Undecoded()[0])
	}
	if err != nil {
		return manifest{err: err}
	}
	var m manifest
	switch kind {
	case "pack":
		m.imageFile, m.ports, m.caps = p.Image.File, p.Ports, p.Unit.AddCapability
	case "option":
		m.firewall, m.pull, m.mounts, m.ports, m.caps = o.Firewall, o.Image.Pull, o.Mounts, o.Ports, o.Unit.AddCapability
	case "tool":
		m.firewall, m.ports, m.caps = t.Firewall, t.Ports, t.Unit.AddCapability
	case "agent":
		m.allow, m.mounts, m.caps = a.Allow, a.Mounts, a.Unit.AddCapability
	}
	return m
}

// manifestFlags are the flagged field changes from old to new.
func manifestFlags(old, new manifest) []Flag {
	var out []Flag
	add := func(check, format string, args ...any) {
		out = append(out, Flag{Check: check, Text: fmt.Sprintf(format, args...)})
	}
	if new.err != nil {
		add(CheckRunsHere, "does not load: %v", new.err)
		return out
	}
	for _, h := range new.allow {
		if !slices.Contains(old.allow, h) {
			add(CheckFirewall, "allow: adds %s", h)
		}
	}
	if !slices.Equal(old.firewall, new.firewall) {
		add(CheckFirewall, "firewall: %s -> %s", profiles(old.firewall), profiles(new.firewall))
	}
	if new.pull != "" && new.pull != old.pull {
		add(CheckImage, "image.pull: %s", new.pull)
	}
	if new.imageFile != "" && new.imageFile != old.imageFile {
		add(CheckImage, "image.file: %s", new.imageFile)
	}
	for _, m := range new.mounts {
		if hostSource(m.Source) && !slices.Contains(old.mounts, m) {
			add(CheckHostMount, "mounts: %s at %s", m.Source, m.Target)
		}
	}
	for _, p := range new.ports {
		i := slices.IndexFunc(old.ports, func(q pack.Port) bool { return q.Name == p.Name })
		if i < 0 {
			add(CheckPort, "ports: adds %s (base %d, container %d, share %v)", p.Name, p.Base, p.Container, p.Share)
			continue
		}
		if q := old.ports[i]; q.Base != p.Base || q.Container != p.Container || q.Share != p.Share {
			add(CheckPort, "ports.%s: base %d -> %d, container %d -> %d, share %v -> %v", p.Name, q.Base, p.Base, q.Container, p.Container, q.Share, p.Share)
		}
	}
	for _, c := range new.caps {
		if !slices.Contains(old.caps, c) {
			add(CheckPrivilege, "unit.add_capability: adds %s", c)
		}
	}
	return out
}

// hostSource reports whether a mount source names the host (an absolute
// path or a placeholder), not a package file.
func hostSource(s string) bool {
	return strings.HasPrefix(s, "/") || strings.Contains(s, "{")
}

func profiles(p []string) string {
	if len(p) == 0 {
		return "(default)"
	}
	return strings.Join(p, ", ")
}

var (
	// pipeRe is a download piped into an interpreter, through sudo or env
	// or by path allowed; substRe a download run by process or command
	// substitution.
	pipeRe  = regexp.MustCompile(`(?i)\b(curl|wget)\b.*\|\s*(sudo\s+(-\S+\s+)*)?(env\s+(-\S+\s+)*)?(\S*/)?(sh|bash|zsh|dash|ash|ksh|fish|python|python3|perl|ruby|node|php)\b`)
	substRe = regexp.MustCompile("(?i)(<\\(|\\$\\(|`)\\s*(\\S*/)?(curl|wget)\\b")
	addRe   = regexp.MustCompile(`(?i)^\s*ADD\s+(--\S+\s+)*https?://`)
	unsafe  = regexp.MustCompile(`--security=insecure|--network=host`)
	fromRe  = regexp.MustCompile(`(?i)^\s*FROM\s`)
	// heredocRe is a heredoc's start (<<EOF, <<-"EOF"); group 1 the word that ends it.
	heredocRe = regexp.MustCompile(`<<-?\s*["']?([A-Za-z_][A-Za-z0-9_]*)`)
)

// lineFlags are the flagged lines file p adds: in a Containerfile, what it
// runs and the images it builds from; in deps.allow, the hosts it adds.
func lineFlags(p, old, new string) []Flag {
	lines := strings.SplitAfter(new, "\n")
	text := func(n int) string { return "+ " + strings.TrimRight(lines[n-1], "\r\n") }
	var out []Flag
	switch {
	case strings.HasPrefix(path.Base(p), "Containerfile"):
		added := textdiff.Added(old, new)
		for _, in := range instructions(lines) {
			// An instruction is judged whole, at its first added line.
			i := slices.IndexFunc(added, func(n int) bool { return n >= in.first && n <= in.last })
			if i < 0 {
				continue
			}
			n := added[i]
			if pipeRe.MatchString(in.text) || substRe.MatchString(in.text) || addRe.MatchString(in.text) || unsafe.MatchString(in.text) {
				out = append(out, Flag{Check: CheckRunsHere, Line: n, Text: text(n)})
			}
			if fromRe.MatchString(in.text) {
				out = append(out, Flag{Check: CheckImage, Line: n, Text: text(n)})
			}
		}
	case p == "deps.allow":
		had := map[string]bool{}
		for _, l := range strings.Split(old, "\n") {
			had[host(l)] = true
		}
		for i, l := range strings.Split(new, "\n") {
			if h := host(l); h != "" && !had[h] {
				had[h] = true
				out = append(out, Flag{Check: CheckFirewall, Line: i + 1, Text: text(i + 1)})
			}
		}
	}
	return out
}

// instruction is one logical Containerfile instruction: lines first to
// last (1-based), joined into text with continuations and heredoc bodies.
type instruction struct {
	first, last int
	text        string
}

// instructions splits a Containerfile's lines into instructions: a line
// ending in \ continues, and a heredoc runs to the line of its word (or the end).
// Any line counts, a comment too: a doubt joins more, never less.
func instructions(lines []string) []instruction {
	var out []instruction
	for i := 0; i < len(lines); {
		in := instruction{first: i + 1}
		var parts, ends []string
		for i < len(lines) {
			l := strings.TrimRight(lines[i], "\r\n")
			i++
			if len(ends) > 0 {
				parts = append(parts, l)
				if strings.TrimSpace(l) == ends[0] {
					ends = ends[1:]
				}
				if len(ends) == 0 {
					break
				}
				continue
			}
			for _, m := range heredocRe.FindAllStringSubmatch(l, -1) {
				ends = append(ends, m[1])
			}
			body, cont := strings.CutSuffix(strings.TrimRight(l, " \t"), `\`)
			parts = append(parts, body)
			if !cont && len(ends) == 0 {
				break
			}
		}
		in.last, in.text = i, strings.Join(parts, " ")
		out = append(out, in)
	}
	return out
}

// host is an allow file line's host: the line without its # comment.
func host(line string) string {
	h, _, _ := strings.Cut(line, "#")
	return strings.TrimSpace(h)
}

// scriptFlags flags a top-level script of a package that adds
// capabilities: it may run with them (an option's firewall script does).
func scriptFlags(p string, caps []string) []Flag {
	if len(caps) == 0 || strings.Contains(p, "/") || path.Ext(p) != ".sh" {
		return nil
	}
	return []Flag{{Check: CheckPrivilege, Text: fmt.Sprintf("%s: a script of a package with add_capability %s", p, strings.Join(caps, ", "))}}
}
