package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Stat is one running container's resource use, as `podman stats` reports
// it. Memory is podman's human-readable figure turned back into bytes, so
// it is exact to its 4 significant figures.
type Stat struct {
	CPUPercent float64
	MemUsed    int64 // bytes
	MemLimit   int64 // bytes: the cgroup's limit (Memory=, D34), or the host's memory without one
	MemPercent float64
	PIDs       int
}

// Stats samples the named running containers once, in one `podman stats
// --no-stream`, keyed by container name. podman fails the whole call when
// a named container is not running, so callers pass only running ones.
func (p Podman) Stats(ctx context.Context, names []string) (map[string]Stat, error) {
	out := map[string]Stat{}
	if len(names) == 0 {
		return out, nil
	}
	r, err := p.run(ctx, 0, append([]string{"stats", "--no-stream", "--no-reset", "--format", "json"}, names...)...)
	if err != nil {
		return nil, err
	}
	return parseStats(r.Stdout)
}

// parseStats reads `podman stats --format json`: an array of objects whose
// values podman 5.3 and later print as strings ("1.52%", "12.3MB / 2.147GB",
// "7", "--" when unknown). It also takes numbers, and keys in any case.
func parseStats(b []byte) (map[string]Stat, error) {
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("podman stats: %w", err)
	}
	out := map[string]Stat{}
	for _, row := range rows {
		v := map[string]any{}
		for k, x := range row {
			v[strings.ToLower(k)] = x
		}
		name, _ := v["name"].(string)
		if name == "" {
			continue
		}
		s := Stat{CPUPercent: number(v["cpu_percent"]), MemPercent: number(v["mem_percent"]), PIDs: int(number(v["pids"]))}
		if mem, ok := v["mem_usage"].(string); ok {
			used, limit, _ := strings.Cut(mem, "/")
			s.MemUsed, s.MemLimit = bytesOf(used), bytesOf(limit)
		}
		if s.MemPercent == 0 && s.MemLimit > 0 {
			s.MemPercent = 100 * float64(s.MemUsed) / float64(s.MemLimit)
		}
		out[name] = s
	}
	return out, nil
}

// number is a JSON number, or a string such as "1.5%" or "7"; 0 for "--"
// or anything else.
func number(x any) float64 {
	switch x := x.(type) {
	case float64:
		return x
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(x), "%")), 64)
		if err == nil {
			return f
		}
	}
	return 0
}

// sizeUnits are the suffixes of podman's sizes: decimal (go-units
// HumanSize, what podman prints) and binary, in case.
var sizeUnits = map[string]float64{
	"": 1, "b": 1,
	"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "pib": 1 << 50,
}

// bytesOf is a size such as "12.3MB" or "512 MiB" in bytes; 0 when unknown.
func bytesOf(s string) int64 {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i < 0 {
		i = len(s)
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	mul, ok := sizeUnits[strings.ToLower(strings.TrimSpace(s[i:]))]
	if err != nil || !ok {
		return 0
	}
	return int64(math.Round(f * mul))
}
