package main

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// In Bluetooth mode a direct message hops through other people's laptops,
// so it is encrypted for its one recipient: X25519 between the two
// laptops' keys, then AES-GCM. Keys are made fresh every time nook starts.
// It stops anyone reading along; it does not prove who is who.

func newDMKey() (*ecdh.PrivateKey, string, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return k, base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func dmCipher(priv *ecdh.PrivateKey, peerPub, a, b string) (cipher.AEAD, error) {
	raw, err := base64.StdEncoding.DecodeString(peerPub)
	if err != nil {
		return nil, err
	}
	pub, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	if a > b {
		a, b = b, a
	}
	key := sha256.Sum256(append([]byte("nook-dm|"+a+"|"+b+"|"), shared...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealDM encrypts text from one person to another (compressed first, so
// long messages still fit small Bluetooth links).
func sealDM(priv *ecdh.PrivateKey, peerPub, from, to, text string) (string, error) {
	g, err := dmCipher(priv, peerPub, from, to)
	if err != nil {
		return "", err
	}
	var z bytes.Buffer
	w, _ := flate.NewWriter(&z, flate.BestCompression)
	w.Write([]byte(text))
	w.Close()
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	box := g.Seal(nonce, nonce, z.Bytes(), []byte(from+">"+to))
	return base64.StdEncoding.EncodeToString(box), nil
}

func openDM(priv *ecdh.PrivateKey, peerPub, from, to, box string) (string, error) {
	g, err := dmCipher(priv, peerPub, from, to)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(box)
	if err != nil || len(raw) < g.NonceSize() {
		return "", errors.New("bad message")
	}
	z, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(from+">"+to))
	if err != nil {
		return "", err
	}
	r := flate.NewReader(bytes.NewReader(z))
	defer r.Close()
	text, err := io.ReadAll(io.LimitReader(r, 64<<10))
	return string(text), err
}
