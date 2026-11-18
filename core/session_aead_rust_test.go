//go:build darwin && arm64 && cgo && uwdt_rust

package core

import (
	"bytes"
	"crypto/cipher"
	"testing"
)

func TestRustSessionAEADInteroperability(t *testing.T) {
	key := bytes.Repeat([]byte{42}, wrapKeyLen)
	goAEAD, err := getAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	rustAEAD, release, err := newSessionAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	for _, mode := range []string{"audio", "video"} {
		t.Run(mode, func(t *testing.T) {
			cfg, state := NewObfsConfig(mode), NewObfsState()
			var wire []byte
			for _, size := range []int{1, 48, 1280, 2048} {
				payload := bytes.Repeat([]byte{byte(size%251 + 1)}, size)
				for _, codecs := range [][2]cipher.AEAD{{goAEAD, rustAEAD}, {rustAEAD, goAEAD}} {
					wire, err = obfsSealPacket(codecs[0], payload, cfg, state, wire)
					if err != nil {
						t.Fatal(err)
					}
					dst := make([]byte, size)
					n, err := obfsOpenPacket(codecs[1], wire, dst)
					if err != nil || n != size || !bytes.Equal(dst, payload) {
						t.Fatalf("packet of %d bytes: deciphered %d bytes, error %v", size, n, err)
					}
					wire[12] ^= 1
					if _, err := obfsOpenPacket(codecs[1], wire, dst); err == nil {
						t.Fatal("accepted altered ciphertext")
					}
				}
			}
		})
	}
}

func BenchmarkSessionAEAD(b *testing.B) {
	key := bytes.Repeat([]byte{42}, wrapKeyLen)
	goAEAD, err := getAEAD(key)
	if err != nil {
		b.Fatal(err)
	}
	rustAEAD, release, err := newSessionAEAD(key)
	if err != nil {
		b.Fatal(err)
	}
	defer release()

	for _, implementation := range []struct {
		name string
		aead cipher.AEAD
	}{{"Go", goAEAD}, {"Rust", rustAEAD}} {
		b.Run(implementation.name, func(b *testing.B) {
			cfg, state := NewObfsConfig("audio"), NewObfsState()
			payload, dst := make([]byte, 1280), make([]byte, 1280)
			wire := make([]byte, 0, 1400)
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				wire, err = obfsSealPacket(implementation.aead, payload, cfg, state, wire)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := obfsOpenPacket(implementation.aead, wire, dst); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
