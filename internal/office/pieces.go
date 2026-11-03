// Package office holds the Virtual Office's sets (D153): the piece list a
// set's models must match, glTF bounds, the checks a set folder gets before
// it is installed, and the store of installed sets.
package office

import (
	"encoding/json"
	"fmt"
	"io/fs"
)

// PiecesPath is the piece list in the dashboard's static files.
const PiecesPath = "office/kit/pieces.json"

// Piece is one entry of the piece list: its box from its origin (x width,
// y up, z front, colliders left out), the points the layout uses, and what
// the built-in piece has beyond its shape.
type Piece struct {
	Min    [3]float64            `json:"min"`
	Max    [3]float64            `json:"max"`
	Points map[string][3]float64 `json:"points,omitempty"`
	Extras []string              `json:"extras"`
	Parts  []string              `json:"parts,omitempty"`
}

// Pieces is kit/pieces.json, written by tools/office-kit/pieces.py.
type Pieces struct {
	Version int              `json:"version"`
	Pieces  map[string]Piece `json:"pieces"`
}

// LoadPieces reads the piece list from the dashboard's static files.
func LoadPieces(static fs.FS) (*Pieces, error) {
	data, err := fs.ReadFile(static, PiecesPath)
	if err != nil {
		return nil, err
	}
	var p Pieces
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", PiecesPath, err)
	}
	if p.Version < 1 || len(p.Pieces) == 0 {
		return nil, fmt.Errorf("%s: no version or no pieces", PiecesPath)
	}
	return &p, nil
}
