//go:build !darwin || !arm64 || !cgo || !uwdt_rust

package core

import "crypto/cipher"

func newSessionAEAD(key []byte) (cipher.AEAD, func(), error) {
	aead, err := getAEAD(key)
	return aead, nil, err
}
