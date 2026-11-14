package update

import (
	"encoding/binary"
	"encoding/hex"
	"math/bits"
)

// A small SHA-256. The standard library's crypto/sha256 drags the whole FIPS
// 140 module into the executable (about 1 MB); the update check needs only
// this. The tests compare it with the standard one.

var k256 = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

type sum256 struct {
	h   [8]uint32
	buf [64]byte
	n   int
	len uint64
}

func newSum256() *sum256 {
	return &sum256{h: [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}}
}

func (s *sum256) Write(p []byte) (int, error) {
	total := len(p)
	s.len += uint64(total)
	if s.n > 0 {
		c := copy(s.buf[s.n:], p)
		s.n += c
		p = p[c:]
		if s.n == 64 {
			s.block(s.buf[:])
			s.n = 0
		}
	}
	for len(p) >= 64 {
		s.block(p[:64])
		p = p[64:]
	}
	if len(p) > 0 {
		s.n = copy(s.buf[:], p)
	}
	return total, nil
}

func (s *sum256) block(p []byte) {
	var w [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(p[i*4:])
	}
	for i := 16; i < 64; i++ {
		s0 := bits.RotateLeft32(w[i-15], -7) ^ bits.RotateLeft32(w[i-15], -18) ^ (w[i-15] >> 3)
		s1 := bits.RotateLeft32(w[i-2], -17) ^ bits.RotateLeft32(w[i-2], -19) ^ (w[i-2] >> 10)
		w[i] = w[i-16] + s0 + w[i-7] + s1
	}
	a, b, c, d, e, f, g, h := s.h[0], s.h[1], s.h[2], s.h[3], s.h[4], s.h[5], s.h[6], s.h[7]
	for i := 0; i < 64; i++ {
		t1 := h + (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11) ^ bits.RotateLeft32(e, -25)) + ((e & f) ^ (^e & g)) + k256[i] + w[i]
		t2 := (bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)) + ((a & b) ^ (a & c) ^ (b & c))
		h, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	s.h[0] += a
	s.h[1] += b
	s.h[2] += c
	s.h[3] += d
	s.h[4] += e
	s.h[5] += f
	s.h[6] += g
	s.h[7] += h
}

// Hex finishes the hash and returns it as 64 hex digits.
func (s *sum256) Hex() string {
	bitLen := s.len * 8
	pad := []byte{0x80}
	for (s.len+uint64(len(pad)))%64 != 56 {
		pad = append(pad, 0)
	}
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], bitLen)
	s.Write(append(pad, l[:]...))
	var out [32]byte
	for i, v := range s.h {
		binary.BigEndian.PutUint32(out[i*4:], v)
	}
	return hex.EncodeToString(out[:])
}
