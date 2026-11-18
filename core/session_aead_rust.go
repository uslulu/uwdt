//go:build darwin && arm64 && cgo && uwdt_rust

package core

/*
#cgo LDFLAGS: ${SRCDIR}/rust-aead/target/release/libuwdt_aead.a -framework Security -framework CoreFoundation
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
void *uwdt_aead_new(const uint8_t *);
void uwdt_aead_free(void *);
bool uwdt_aead_seal(const void *, const uint8_t *, const uint8_t *, uint8_t *, size_t);
bool uwdt_aead_open(const void *, const uint8_t *, const uint8_t *, uint8_t *, size_t, const uint8_t *);
*/
import "C"

import (
	"crypto/cipher"
	"errors"
	"unsafe"
)

type rustSessionAEAD struct {
	key unsafe.Pointer
}

func newSessionAEAD(key []byte) (cipher.AEAD, func(), error) {
	if len(key) != wrapKeyLen {
		return nil, nil, errors.New("invalid Rust AEAD key length")
	}
	handle := C.uwdt_aead_new(cBytes(key))
	if handle == nil {
		return nil, nil, errors.New("could not initialize Rust AEAD")
	}
	return rustSessionAEAD{key: handle}, func() { C.uwdt_aead_free(handle) }, nil
}

func cBytes(b []byte) *C.uint8_t {
	return (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(b)))
}

func (rustSessionAEAD) NonceSize() int { return 12 }
func (rustSessionAEAD) Overhead() int  { return 16 }

// The RTP codec supplies disjoint header, payload and output buffers.
func (a rustSessionAEAD) Seal(dst, nonce, plaintext, header []byte) []byte {
	start := len(dst)
	dst = append(dst, plaintext...)
	var tag [16]byte
	dst = append(dst, tag[:]...)
	if !C.uwdt_aead_seal(a.key, cBytes(nonce), cBytes(header), cBytes(dst[start:]), C.size_t(len(plaintext))) {
		panic("Rust AEAD failed to seal RTP packet")
	}
	return dst
}

func (a rustSessionAEAD) Open(dst, nonce, ciphertext, header []byte) ([]byte, error) {
	body := len(ciphertext) - a.Overhead()
	start := len(dst)
	dst = append(dst, ciphertext[:body]...)
	if !C.uwdt_aead_open(a.key, cBytes(nonce), cBytes(header), cBytes(dst[start:]), C.size_t(body), cBytes(ciphertext[body:])) {
		clear(dst[start:])
		return nil, errors.New("message authentication failed")
	}
	return dst, nil
}
