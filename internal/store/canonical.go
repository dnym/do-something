package store

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// ParseCanonical decodes the versioned typed digest representation. It bounds
// all lengths against the remaining input before allocating memory.
func ParseCanonical(data []byte) (Content, error) {
	c := EmptyContent()
	r := bytes.NewReader(data)
	str := func() (string, error) {
		var n uint64
		if e := binary.Read(r, binary.BigEndian, &n); e != nil {
			return "", e
		}
		if n > uint64(r.Len()) {
			return "", io.ErrUnexpectedEOF
		}
		b := make([]byte, int(n))
		_, e := io.ReadFull(r, b)
		return string(b), e
	}
	version, e := str()
	if e != nil || version != "do-something/content/1" {
		return c, fmt.Errorf("invalid canonical version")
	}
	for _, t := range c.Tables() {
		if r.Len() == 0 && t.Name == "events" {
			break
		}
		name, e := str()
		if e != nil || name != t.Name {
			return c, fmt.Errorf("invalid canonical table")
		}
		for _, col := range t.Columns {
			k, e := str()
			if e != nil || k != col {
				return c, fmt.Errorf("invalid canonical column")
			}
		}
		var n uint64
		if e = binary.Read(r, binary.BigEndian, &n); e != nil {
			return c, e
		}
		if n > uint64(r.Len()/len(t.Columns)) {
			return c, io.ErrUnexpectedEOF
		}
		rows := []Row{}
		last := ""
		for i := uint64(0); i < n; i++ {
			row := Row{}
			for _, k := range t.Columns {
				tag, e := r.ReadByte()
				if e != nil {
					return c, e
				}
				var v any
				switch tag {
				case 0:
				case 1:
					v, e = str()
				case 2:
					var n int64
					e = binary.Read(r, binary.BigEndian, &n)
					v = n
				case 3:
					var n float64
					e = binary.Read(r, binary.BigEndian, &n)
					if math.IsNaN(n) || math.IsInf(n, 0) {
						return c, fmt.Errorf("non-finite number")
					}
					v = n
				default:
					return c, fmt.Errorf("invalid canonical type")
				}
				if e != nil {
					return c, e
				}
				row[k] = v
			}
			key := RowKey(t, row)
			if i > 0 && key <= last {
				return c, fmt.Errorf("noncanonical row order")
			}
			last = key
			rows = append(rows, row)
		}
		c.SetTable(t.Name, rows)
	}
	if r.Len() != 0 {
		return c, fmt.Errorf("trailing canonical data")
	}
	return c, c.Validate()
}
