package main

import "errors"

// Rijndael with 256-bit blocks, matching the game save format.
type ourNotesRijndael256 struct {
	words   [120][4]byte
	inverse [256]byte
}

func ourNotesMul(a, b byte) byte {
	var result byte
	for i := 0; i < 8; i++ {
		if b&1 != 0 {
			result ^= a
		}
		hi := a&0x80 != 0
		a <<= 1
		if hi {
			a ^= 0x1b
		}
		b >>= 1
	}
	return result
}

func newOurNotesRijndael256(key [32]byte) ourNotesRijndael256 {
	var sbox, inverse [256]byte
	for i := range sbox {
		x := byte(1)
		for j := 0; j < 254; j++ {
			x = ourNotesMul(x, byte(i))
		}
		s := x ^ bitsRotateLeft8(x, 1) ^ bitsRotateLeft8(x, 2) ^ bitsRotateLeft8(x, 3) ^ bitsRotateLeft8(x, 4) ^ 0x63
		sbox[i], inverse[s] = s, byte(i)
	}
	var d ourNotesRijndael256
	d.inverse = inverse
	for i := 0; i < 8; i++ {
		copy(d.words[i][:], key[i*4:i*4+4])
	}
	rcon := byte(1)
	for i := 8; i < 120; i++ {
		t := d.words[i-1]
		if i%8 == 0 {
			t = [4]byte{t[1], t[2], t[3], t[0]}
			for j := range t {
				t[j] = sbox[t[j]]
			}
			t[0] ^= rcon
			rcon = ourNotesMul(rcon, 2)
		} else if i%8 == 4 {
			for j := range t {
				t[j] = sbox[t[j]]
			}
		}
		for j := 0; j < 4; j++ {
			d.words[i][j] = d.words[i-8][j] ^ t[j]
		}
	}
	return d
}

func bitsRotateLeft8(x byte, n uint) byte { return x<<n | x>>(8-n) }

func (d ourNotesRijndael256) decrypt(encrypted []byte, iv [32]byte) ([]byte, error) {
	if len(encrypted) == 0 || len(encrypted)%32 != 0 {
		return nil, errors.New("invalid Rijndael ciphertext")
	}
	out := make([]byte, 0, len(encrypted))
	previous := iv[:]
	for offset := 0; offset < len(encrypted); offset += 32 {
		plain := d.block(encrypted[offset : offset+32])
		for i := range plain {
			out = append(out, plain[i]^previous[i])
		}
		previous = encrypted[offset : offset+32]
	}
	padding := int(out[len(out)-1])
	if padding < 1 || padding > 32 || len(out) < padding {
		return nil, errors.New("invalid Rijndael padding")
	}
	for _, b := range out[len(out)-padding:] {
		if int(b) != padding {
			return nil, errors.New("invalid Rijndael padding")
		}
	}
	return out[:len(out)-padding], nil
}

func (d ourNotesRijndael256) block(input []byte) [32]byte {
	var state [32]byte
	copy(state[:], input)
	addKey := func(round int) {
		for i := range state {
			state[i] ^= d.words[round*8+i/4][i%4]
		}
	}
	addKey(14)
	for round := 13; round >= 0; round-- {
		previous := state
		for column := 0; column < 8; column++ {
			for row, shift := range []int{0, 1, 3, 4} {
				state[column*4+row] = d.inverse[previous[((column+8-shift)%8)*4+row]]
			}
		}
		addKey(round)
		if round != 0 {
			for column := 0; column < 8; column++ {
				a := [4]byte{state[column*4], state[column*4+1], state[column*4+2], state[column*4+3]}
				for row := 0; row < 4; row++ {
					state[column*4+row] = ourNotesMul(a[row], 14) ^ ourNotesMul(a[(row+1)%4], 11) ^ ourNotesMul(a[(row+2)%4], 13) ^ ourNotesMul(a[(row+3)%4], 9)
				}
			}
		}
	}
	return state
}
