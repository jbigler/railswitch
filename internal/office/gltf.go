package office

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
)

// isCol is kit.js's collider rule: <piece>_COL, <piece>_COL1, ...
var isCol = regexp.MustCompile(`_COL\d*$`)

type gltfDoc struct {
	ExtensionsRequired []string `json:"extensionsRequired"`
	Scene              *int     `json:"scene"`
	Scenes             []struct {
		Nodes []int `json:"nodes"`
	} `json:"scenes"`
	Nodes []struct {
		Name        string    `json:"name"`
		Mesh        *int      `json:"mesh"`
		Children    []int     `json:"children"`
		Matrix      []float64 `json:"matrix"`
		Translation []float64 `json:"translation"`
		Rotation    []float64 `json:"rotation"`
		Scale       []float64 `json:"scale"`
	} `json:"nodes"`
	Meshes []struct {
		Primitives []struct {
			Attributes map[string]int `json:"attributes"`
		} `json:"primitives"`
	} `json:"meshes"`
	Accessors []struct {
		Min []float64 `json:"min"`
		Max []float64 `json:"max"`
	} `json:"accessors"`
	Buffers []struct {
		URI string `json:"uri"`
	} `json:"buffers"`
	Images []struct {
		URI string `json:"uri"`
	} `json:"images"`
}

type mat4 [4][4]float64

var identity = mat4{{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}}

func (a mat4) mul(b mat4) mat4 {
	var m mat4
	for i := range 4 {
		for j := range 4 {
			for k := range 4 {
				m[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return m
}

// local is a node's transform: its matrix (column-major), else T·R·S.
func local(matrix, t, r, s []float64) (mat4, error) {
	if matrix != nil {
		if len(matrix) != 16 {
			return mat4{}, errors.New("a node matrix without 16 numbers")
		}
		var m mat4
		for c := range 4 {
			for r := range 4 {
				m[r][c] = matrix[c*4+r]
			}
		}
		return m, nil
	}
	tt, rr, ss := []float64{0, 0, 0}, []float64{0, 0, 0, 1}, []float64{1, 1, 1}
	for _, v := range []struct {
		got  []float64
		dst  *[]float64
		want int
	}{{t, &tt, 3}, {r, &rr, 4}, {s, &ss, 3}} {
		if v.got != nil {
			if len(v.got) != v.want {
				return mat4{}, errors.New("a node translation, rotation or scale of the wrong length")
			}
			*v.dst = v.got
		}
	}
	x, y, z, w := rr[0], rr[1], rr[2], rr[3]
	rot := [3][3]float64{
		{1 - 2*(y*y+z*z), 2 * (x*y - z*w), 2 * (x*z + y*w)},
		{2 * (x*y + z*w), 1 - 2*(x*x+z*z), 2 * (y*z - x*w)},
		{2 * (x*z - y*w), 2 * (y*z + x*w), 1 - 2*(x*x+y*y)},
	}
	var m mat4
	for i := range 3 {
		for j := range 3 {
			m[i][j] = rot[i][j] * ss[j]
		}
		m[i][3] = tt[i]
	}
	m[3][3] = 1
	return m, nil
}

// Bounds is the box of a glTF's meshes from its origin: every mesh
// primitive's POSITION min/max corners through its node's world transform,
// from the default scene's roots, colliders (and what hangs from them) left
// out, as tools/office-kit/pieces.py computes it.
func Bounds(data []byte) (lo, hi [3]float64, err error) {
	var g gltfDoc
	if err := json.Unmarshal(data, &g); err != nil {
		return lo, hi, fmt.Errorf("not a glTF: %w", err)
	}
	return g.bounds()
}

func (g *gltfDoc) bounds() (lo, hi [3]float64, err error) {
	for k := range 3 {
		lo[k], hi[k] = math.Inf(1), math.Inf(-1)
	}
	scene := 0
	if g.Scene != nil {
		scene = *g.Scene
	}
	if scene < 0 || scene >= len(g.Scenes) {
		return lo, hi, errors.New("no scene")
	}
	meshes, seen := 0, map[int]bool{}
	var walk func(i int, parent mat4) error
	walk = func(i int, parent mat4) error {
		if i < 0 || i >= len(g.Nodes) || seen[i] {
			return errors.New("a node index out of range or a node reached twice")
		}
		seen[i] = true
		n := g.Nodes[i]
		if isCol.MatchString(n.Name) {
			return nil
		}
		l, err := local(n.Matrix, n.Translation, n.Rotation, n.Scale)
		if err != nil {
			return err
		}
		m := parent.mul(l)
		if n.Mesh != nil {
			if *n.Mesh < 0 || *n.Mesh >= len(g.Meshes) {
				return errors.New("a mesh index out of range")
			}
			for _, p := range g.Meshes[*n.Mesh].Primitives {
				ai, ok := p.Attributes["POSITION"]
				if !ok || ai < 0 || ai >= len(g.Accessors) {
					return errors.New("a primitive without POSITION")
				}
				a := g.Accessors[ai]
				if len(a.Min) != 3 || len(a.Max) != 3 {
					return errors.New("a POSITION accessor without min and max")
				}
				meshes++
				for c := range 8 {
					v := [3]float64{a.Min[0], a.Min[1], a.Min[2]}
					for k := range 3 {
						if c>>k&1 == 1 {
							v[k] = a.Max[k]
						}
					}
					for r := range 3 {
						w := m[r][0]*v[0] + m[r][1]*v[1] + m[r][2]*v[2] + m[r][3]
						lo[r], hi[r] = math.Min(lo[r], w), math.Max(hi[r], w)
					}
				}
			}
		}
		for _, c := range n.Children {
			if err := walk(c, m); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range g.Scenes[scene].Nodes {
		if err := walk(r, identity); err != nil {
			return lo, hi, err
		}
	}
	if meshes == 0 {
		return lo, hi, errors.New("no mesh")
	}
	return lo, hi, nil
}
