// Copyright 2023 Christopher Briscoe.  All rights reserved.

package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	mrand "math/rand"
	"strings"
	"time"

	"github.com/cwbriscoe/goutil/str"
	"golang.org/x/crypto/bcrypt"
)

// bcrypt format
// $2a$04$g2tinxYv91PKj./q9ZyVdew2mKToygQrYb6gMReolejVgMaGcD2Ny
// 2a = algorithm
// 04 = cost
// rest = salt + hash

const (
	hashVersion string = "1"
	hashCost    int    = 4
)

func (a *Auth) generate(pass string) (string, error) {
	pass += "." + a.pepper
	start := time.Now()
	hashedPass, err := bcrypt.GenerateFromPassword(str.UnsafeStringToByte(pass), hashCost)
	if err != nil {
		return "", err
	}

	a.log.Debug().Msgf("original pass %s", string(hashedPass))

	elapsed := time.Since(start)
	a.log.Debug().Msgf("GenerateFromPassword %s", elapsed.String())
	start = time.Now()

	hashedPass = alter(string(hashedPass))
	a.log.Debug().Msgf("altered pass %s", string(hashedPass))

	encodedPass, err := encrypt(hashedPass, a.key)
	if err != nil {
		return "", err
	}

	slowDown()

	elapsed = time.Since(start)
	a.log.Debug().Msgf("encrypt %s", elapsed.String())

	return encodedPass, nil
}

func (a *Auth) compare(hash, pass string) (bool, error) {
	pass += "." + a.pepper
	start := time.Now()
	decodedPass, err := decrypt(hash, a.key)
	a.log.Debug().Msgf("pass %s", string(decodedPass))
	if err != nil {
		return false, err
	}

	elapsed := time.Since(start)
	a.log.Debug().Msgf("decrypt %s", elapsed.String())
	start = time.Now()

	decodedPass = unalter(string(decodedPass))
	a.log.Debug().Msgf("unaltered pass %s", string(decodedPass))

	if err := bcrypt.CompareHashAndPassword(decodedPass, str.UnsafeStringToByte(pass)); err != nil {
		return false, err
	}

	slowDown()

	elapsed = time.Since(start)
	a.log.Debug().Msgf("CompareHashAndPassword %s", elapsed.String())

	return true, nil
}

func encrypt(secret, key []byte) (string, error) {
	// create a new cipher block from the key.
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// create a new GCM - https://en.wikipedia.org/wiki/Galois/Counter_Mode
	// https://golang.org/pkg/crypto/cipher/#NewGCM
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// create a nonce. nonce should be from GCM.
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	// encrypt the data using aesGCM.Seal.
	// since we don't want to save the nonce somewhere else in this case, we add it as a prefix to the encrypted data.
	// the first nonce argument in Seal is the prefix.
	ciphertext := aesGCM.Seal(nonce, nonce, secret, nil)

	// convert to base64 string
	encoded := base64.URLEncoding.EncodeToString(ciphertext)
	return encoded, nil
}

func decrypt(secret string, key []byte) ([]byte, error) {
	// convert from base64 to []byte
	unencoded, err := base64.URLEncoding.DecodeString(secret)
	if err != nil {
		return nil, err
	}

	// create a new cipher block from the key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// create a new GCM
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// get the nonce size
	nonceSize := aesGCM.NonceSize()

	// extract the nonce from the encrypted data
	nonce, ciphertext := unencoded[:nonceSize], unencoded[nonceSize:]

	// decrypt the data
	plaintext, err := aesGCM.Open(nil, []byte(nonce), []byte(ciphertext), nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

func alter(hash string) []byte {
	pieces := strings.Split(hash, "$")
	pieces = pieces[1:]
	pieces[1] = "12"
	pieces[2] = strings.Map(rot13, pieces[2])
	result := "$" + hashVersion + "$" + strings.Join(pieces, "$")
	return []byte(result)
}

func unalter(hash string) []byte {
	pieces := strings.Split(hash, "$")
	pieces = pieces[2:]
	pieces[1] = "04"
	pieces[2] = strings.Map(rot13, pieces[2])
	result := "$" + strings.Join(pieces, "$")
	return []byte(result)
}

func rotLower(r rune) rune {
	if r > 'm' {
		return r - 13
	}
	return r + 13
}

func rotUpper(r rune) rune {
	if r > 'M' {
		return r - 13
	}
	return r + 13
}

func rotDigit(r rune) rune {
	if r >= '5' {
		return r - 5
	}
	return r + 5
}

func rot13(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return rotLower(r)
	} else if r >= 'A' && r <= 'Z' {
		return rotUpper(r)
	} else if r >= '0' && r <= '9' {
		return rotDigit(r)
	}
	return r
}

func slowDown() {
	num := 200 + mrand.Intn(50)
	time.Sleep(time.Duration(num) * time.Millisecond)
}
