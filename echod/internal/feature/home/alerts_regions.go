//go:build !dot

package home

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
)

// MeteoAlarm's warning regions, from its own geocode files (gitlab.com/meteoalarm-pm-group/documents,
// licensed under terms equivalent to CC BY 4.0): each region's EMMA_ID, country, name, the other codes
// some countries' warnings name it by, and its outline thinned to about a kilometer, for telling which
// regions home lies in. tools/meteoalarm/make_regions.py makes the file. Only a device with a screen
// carries them.

//go:embed meteoalarm_regions.bin.gz
var regionsGz []byte

var (
	regionsOnce sync.Once
	regions     []maRegion
)

func loadRegions() []maRegion {
	regionsOnce.Do(func() {
		rs, err := readRegions(regionsGz)
		if err != nil {
			slog.Error("alerts: reading MeteoAlarm's regions failed", "err", err)
			return
		}
		regions = rs
	})
	return regions
}

var errRegions = errors.New("not a region file")

// readRegions reads the file make_regions.py writes (its docstring has the format).
func readRegions(gz []byte) ([]maRegion, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(zr)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "MAR1" {
		return nil, errRegions
	}
	str := func() (string, error) {
		n, err := readUvarint(r)
		if err != nil || n > 1<<10 {
			return "", errRegions
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	n, err := readUvarint(r)
	if err != nil || n > 1<<16 {
		return nil, errRegions
	}
	out := make([]maRegion, 0, n)
	for range n {
		var g maRegion
		if g.code, err = str(); err != nil {
			return nil, err
		}
		if g.country, err = str(); err != nil {
			return nil, err
		}
		if g.name, err = str(); err != nil {
			return nil, err
		}
		na, err := readUvarint(r)
		if err != nil || na > 1<<10 {
			return nil, errRegions
		}
		for range na {
			t, err := str()
			if err != nil {
				return nil, err
			}
			v, err := str()
			if err != nil {
				return nil, err
			}
			g.aliases = append(g.aliases, [2]string{t, v})
		}
		nr, err := readUvarint(r)
		if err != nil || nr > 1<<12 {
			return nil, errRegions
		}
		g.box = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for range nr {
			np, err := readUvarint(r)
			if err != nil || np > 1<<20 {
				return nil, errRegions
			}
			ring := make([][2]float64, 0, np)
			var x, y int64
			for range np {
				dx, err1 := readUvarint(r)
				dy, err2 := readUvarint(r)
				if err1 != nil || err2 != nil {
					return nil, errRegions
				}
				x += unzigzag(dx)
				y += unzigzag(dy)
				p := [2]float64{float64(x) / 1e4, float64(y) / 1e4}
				ring = append(ring, p)
				g.box = [4]float64{min(g.box[0], p[0]), min(g.box[1], p[1]), max(g.box[2], p[0]), max(g.box[3], p[1])}
			}
			g.rings = append(g.rings, ring)
		}
		out = append(out, g)
	}
	return out, nil
}

func readUvarint(r *bufio.Reader) (uint64, error) {
	var v uint64
	for shift := 0; shift < 64; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, errRegions
}

func unzigzag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }
