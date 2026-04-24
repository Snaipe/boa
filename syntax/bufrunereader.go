// Copyright 2026 Franklin "Snaipe" Mathieu.
//
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package syntax

import (
	"io"
	"unicode/utf8"
)

// BufRuneReader is a minimal buffered RuneReader that reuses its internal
// buffer across Reset calls. Unlike bufio.Reader, Reset does not discard the
// buffer, making it safe to embed inside sync.Pool entries.
type BufRuneReader struct {
	buf [8192]byte
	r   int
	w   int
	rd  io.Reader
	err error
}

// Reset discards buffered data and switches the reader to rd.
// The underlying 4 KB buffer is retained for the next parse.
func (b *BufRuneReader) Reset(rd io.Reader) {
	b.r, b.w = 0, 0
	b.rd = rd
	b.err = nil
}

func (b *BufRuneReader) fill() {
	if b.r > 0 {
		b.w = copy(b.buf[:], b.buf[b.r:b.w])
		b.r = 0
	}
	for i := 0; i < 100; i++ {
		n, err := b.rd.Read(b.buf[b.w:])
		b.w += n
		if err != nil {
			b.err = err
			return
		}
		if n > 0 {
			return
		}
	}
	b.err = io.ErrNoProgress
}

// ReadRune implements io.RuneReader.
func (b *BufRuneReader) ReadRune() (rune, int, error) {
	for b.w-b.r < utf8.UTFMax && !utf8.FullRune(b.buf[b.r:b.w]) && b.err == nil {
		b.fill()
	}
	if b.r >= b.w {
		if b.err != nil {
			return 0, 0, b.err
		}
		return 0, 0, io.EOF
	}
	r, size := rune(b.buf[b.r]), 1
	if r >= utf8.RuneSelf {
		r, size = utf8.DecodeRune(b.buf[b.r:b.w])
	}
	b.r += size
	return r, size, nil
}
