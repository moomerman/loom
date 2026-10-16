package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// A QR code encoder (ISO/IEC 18004), enough for the otpauth:// address
// an authenticator app scans: byte mode, error correction level M,
// versions 1 to 20 (up to 666 bytes). It is here, rather than a
// dependency, because Loom's runtime is the standard library only, and
// the setup page needs nothing more. qr_test.go checks it against
// python-qrcode's output.

// qrVersion is a version's error correction at level M: the error
// correction codewords per block, and the blocks of each of two sizes.
type qrVersion struct {
	ec             int
	blocks1, data1 int
	blocks2, data2 int
	align          []int // alignment pattern centres, rows and columns
}

var qrVersions = [...]qrVersion{
	1:  {10, 1, 16, 0, 0, nil},
	2:  {16, 1, 28, 0, 0, []int{6, 18}},
	3:  {26, 1, 44, 0, 0, []int{6, 22}},
	4:  {18, 2, 32, 0, 0, []int{6, 26}},
	5:  {24, 2, 43, 0, 0, []int{6, 30}},
	6:  {16, 4, 27, 0, 0, []int{6, 34}},
	7:  {18, 4, 31, 0, 0, []int{6, 22, 38}},
	8:  {22, 2, 38, 2, 39, []int{6, 24, 42}},
	9:  {22, 3, 36, 2, 37, []int{6, 26, 46}},
	10: {26, 4, 43, 1, 44, []int{6, 28, 50}},
	11: {30, 1, 50, 4, 51, []int{6, 30, 54}},
	12: {22, 6, 36, 2, 37, []int{6, 32, 58}},
	13: {22, 8, 37, 1, 38, []int{6, 34, 62}},
	14: {24, 4, 40, 5, 41, []int{6, 26, 46, 66}},
	15: {24, 5, 41, 5, 42, []int{6, 26, 48, 70}},
	16: {28, 7, 45, 3, 46, []int{6, 26, 50, 74}},
	17: {28, 10, 46, 1, 47, []int{6, 30, 54, 78}},
	18: {26, 9, 43, 4, 44, []int{6, 30, 56, 82}},
	19: {26, 3, 44, 11, 45, []int{6, 30, 58, 86}},
	20: {26, 3, 41, 13, 42, []int{6, 34, 62, 90}},
}

func (v qrVersion) dataBytes() int { return v.blocks1*v.data1 + v.blocks2*v.data2 }

// errQRTooLong is returned for text longer than version 20 holds.
var errQRTooLong = errors.New("too long for a QR code")

// qrCode is a QR code's modules, true for dark, [row][column], without
// the quiet zone around it.
type qrCode [][]bool

// encodeQR encodes text in the smallest version that holds it, with the
// mask that scores best.
func encodeQR(text string) (qrCode, error) {
	return encodeQRMask(text, -1)
}

// encodeQRMask encodes text with mask 0 to 7, or the best one for -1.
func encodeQRMask(text string, mask int) (qrCode, error) {
	ver := 0
	for v := 1; v < len(qrVersions); v++ {
		count := 8
		if v >= 10 {
			count = 16
		}
		if 4+count+8*len(text) <= 8*qrVersions[v].dataBytes() {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, errQRTooLong
	}
	codewords := qrCodewords(ver, text)
	if mask >= 0 {
		return qrPlace(ver, codewords, mask), nil
	}
	var best qrCode
	bestScore := 0
	for m := 0; m < 8; m++ {
		c := qrPlace(ver, codewords, m)
		if s := c.penalty(); best == nil || s < bestScore {
			best, bestScore = c, s
		}
	}
	return best, nil
}

// qrCodewords is text in byte mode, padded, split into blocks with their
// error correction, and interleaved.
func qrCodewords(ver int, text string) []byte {
	v := qrVersions[ver]
	var bits qrBits
	bits.put(0b0100, 4) // byte mode
	if ver >= 10 {
		bits.put(len(text), 16)
	} else {
		bits.put(len(text), 8)
	}
	for i := 0; i < len(text); i++ {
		bits.put(int(text[i]), 8)
	}
	capacity := 8 * v.dataBytes()
	for i := 0; i < 4 && bits.n < capacity; i++ { // the terminator
		bits.put(0, 1)
	}
	for bits.n%8 != 0 {
		bits.put(0, 1)
	}
	for pad := 0; bits.n < capacity; pad++ {
		bits.put([]int{0xec, 0x11}[pad%2], 8)
	}

	var data, ecc [][]byte
	off := 0
	for i := 0; i < v.blocks1+v.blocks2; i++ {
		n := v.data1
		if i >= v.blocks1 {
			n = v.data2
		}
		block := bits.b[off : off+n]
		off += n
		data = append(data, block)
		ecc = append(ecc, reedSolomon(block, v.ec))
	}
	var out []byte
	for _, blocks := range [][][]byte{data, ecc} {
		for i := 0; ; i++ {
			more := false
			for _, b := range blocks {
				if i < len(b) {
					out = append(out, b[i])
					more = true
				}
			}
			if !more {
				break
			}
		}
	}
	return out
}

// qrBits is a bit stream, most significant bit first.
type qrBits struct {
	b []byte
	n int // bits written
}

func (q *qrBits) put(v, width int) {
	for i := width - 1; i >= 0; i-- {
		if q.n%8 == 0 {
			q.b = append(q.b, 0)
		}
		if v>>i&1 == 1 {
			q.b[q.n/8] |= 0x80 >> (q.n % 8)
		}
		q.n++
	}
}

// GF(256) with the polynomial QR codes use, x^8+x^4+x^3+x^2+1.
var gfExp, gfLog = func() (exp [512]byte, log [256]byte) {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}
	return
}()

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// reedSolomon returns n error correction codewords for data.
func reedSolomon(data []byte, n int) []byte {
	gen := []byte{1} // the product of (x - a^i), i < n, highest power first
	for i := 0; i < n; i++ {
		next := make([]byte, len(gen)+1)
		for j, g := range gen {
			next[j] ^= g
			next[j+1] ^= gfMul(g, gfExp[i])
		}
		gen = next
	}
	rem := make([]byte, n)
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for j := 0; j < n; j++ {
			rem[j] ^= gfMul(gen[j+1], factor)
		}
	}
	return rem
}

// qrPlace draws the function patterns and the codewords under mask.
func qrPlace(ver int, codewords []byte, mask int) qrCode {
	size := 17 + 4*ver
	c := make(qrCode, size)
	fixed := make([][]bool, size) // function modules, which data skips
	for i := range c {
		c[i], fixed[i] = make([]bool, size), make([]bool, size)
	}
	set := func(r, col int, dark bool) { c[r][col], fixed[r][col] = dark, true }

	// Finder patterns, with their light separators.
	for _, o := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		for r := -1; r <= 7; r++ {
			for col := -1; col <= 7; col++ {
				y, x := o[0]+r, o[1]+col
				if y < 0 || y >= size || x < 0 || x >= size {
					continue
				}
				ring := max(abs(r-3), abs(col-3))
				set(y, x, ring != 2 && ring != 4)
			}
		}
	}
	// Alignment patterns, except where a finder is. The timing patterns
	// run through some; their modules agree.
	al := qrVersions[ver].align
	for _, y := range al {
		for _, x := range al {
			if fixed[y][x] {
				continue
			}
			for r := -2; r <= 2; r++ {
				for col := -2; col <= 2; col++ {
					set(y+r, x+col, max(abs(r), abs(col)) != 1)
				}
			}
		}
	}
	// Timing patterns.
	for i := 8; i < size-8; i++ {
		set(6, i, i%2 == 0)
		set(i, 6, i%2 == 0)
	}
	// The dark module, and room for the format and version information.
	set(size-8, 8, true)
	for i := 0; i < 9; i++ {
		if !fixed[8][i] {
			set(8, i, false)
		}
		if !fixed[i][8] {
			set(i, 8, false)
		}
	}
	for i := 0; i < 8; i++ {
		set(8, size-1-i, false)
		if !fixed[size-1-i][8] {
			set(size-1-i, 8, false)
		}
	}
	if ver >= 7 {
		bits := qrBCH(ver, 12, 0x1f25)
		for i := 0; i < 18; i++ {
			dark := bits>>i&1 == 1
			set(i/3, size-11+i%3, dark)
			set(size-11+i%3, i/3, dark)
		}
	}

	// The codewords, in two-module columns zigzagging up and down from
	// the bottom right, skipping the vertical timing pattern.
	bit := 0
	up := true
	for right := size - 1; right > 0; right -= 2 {
		if right == 6 {
			right = 5
		}
		for k := 0; k < size; k++ {
			r := k
			if up {
				r = size - 1 - k
			}
			for _, col := range []int{right, right - 1} {
				if fixed[r][col] {
					continue
				}
				dark := false
				if bit/8 < len(codewords) {
					dark = codewords[bit/8]>>(7-bit%8)&1 == 1
				}
				bit++
				c[r][col] = dark != qrMask(mask, r, col)
			}
		}
		up = !up
	}

	// The format information: level M (00) and the mask, twice.
	format := qrBCH(mask, 10, 0x537) ^ 0x5412
	for i := 0; i < 15; i++ {
		dark := format>>i&1 == 1
		switch {
		case i < 6:
			c[i][8] = dark
		case i < 8:
			c[i+1][8] = dark
		default:
			c[size-15+i][8] = dark
		}
		switch {
		case i < 8:
			c[8][size-1-i] = dark
		case i < 9:
			c[8][15-i] = dark
		default:
			c[8][14-i] = dark
		}
	}
	return c
}

// qrBCH appends the BCH remainder of data (shifted by n bits) under the
// generator polynomial gen.
func qrBCH(data, n, gen int) int {
	d := data << n
	genLen := bitLen(gen)
	for bitLen(d) >= genLen {
		d ^= gen << (bitLen(d) - genLen)
	}
	return data<<n | d
}

func bitLen(x int) int {
	n := 0
	for ; x > 0; x >>= 1 {
		n++
	}
	return n
}

func qrMask(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return r*c%2+r*c%3 == 0
	case 6:
		return (r*c%2+r*c%3)%2 == 0
	default:
		return ((r+c)%2+r*c%3)%2 == 0
	}
}

// penalty scores how hard a code is to read, as the standard defines
// it, to choose a mask: long runs, 2x2 blocks, finder-like patterns and
// an imbalance of dark and light all cost.
func (c qrCode) penalty() int {
	size := len(c)
	at := func(r, col int, vertical bool) bool {
		if vertical {
			return c[col][r]
		}
		return c[r][col]
	}
	score, dark := 0, 0
	for _, vertical := range []bool{false, true} {
		for r := 0; r < size; r++ {
			run := 1
			for col := 1; col <= size; col++ {
				if col < size && at(r, col, vertical) == at(r, col-1, vertical) {
					run++
					continue
				}
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
			for col := 0; col+11 <= size; col++ {
				var line [11]bool
				for k := range line {
					line[k] = at(r, col+k, vertical)
				}
				finder := [11]bool{true, false, true, true, true, false, true}
				if line == finder || line == [11]bool{false, false, false, false, true, false, true, true, true, false, true} {
					score += 40
				}
			}
		}
	}
	for r := 0; r < size; r++ {
		for col := 0; col < size; col++ {
			if c[r][col] {
				dark++
			}
			if r+1 < size && col+1 < size && c[r][col] == c[r][col+1] && c[r][col] == c[r+1][col] && c[r][col] == c[r+1][col+1] {
				score += 3
			}
		}
	}
	pct := dark * 100 / (size * size)
	return score + abs(pct-50)/5*10
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// svgDataURL draws the code as an SVG image, black on white with the
// standard four-module quiet zone, as a data: URL for an <img src>.
func (c qrCode) svgDataURL() string {
	size := len(c) + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, size, size, size, size)
	for r, row := range c {
		for col, dark := range row {
			if dark {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", col+4, r+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(b.String()))
}
