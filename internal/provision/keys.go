package provision

import (
	"crypto/rand"
	"errors"
)

// KeyLength is not a preference. rdpgw requires SessionKey,
// SessionEncryptionKey, PAATokenSigningKey and PAATokenEncryptionKey to be
// exactly this long; anything else is replaced with a random value at startup,
// silently, so sessions die on every service restart.
const KeyLength = 32

const hexAlphabet = "0123456789abcdef"

// passwordAlphabet omits characters that are easy to misread when a user is
// copying a generated password off a screen: 0/O, 1/l/I.
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewKey32 returns a 32-character hex string from a cryptographic source.
func NewKey32() (string, error) {
	return randomString(KeyLength, hexAlphabet)
}

// NewPassword returns a password of n characters.
func NewPassword(n int) (string, error) {
	if n < 12 {
		return "", errors.New("refusing to generate a password shorter than 12 characters")
	}
	return randomString(n, passwordAlphabet)
}

// randomString draws uniformly from alphabet using rejection sampling, so the
// result is not biased towards the first 256%len(alphabet) characters the way
// a naive modulo would be.
func randomString(n int, alphabet string) (string, error) {
	if n <= 0 {
		return "", errors.New("length must be positive")
	}
	size := len(alphabet)
	// Largest multiple of size that fits in a byte; values at or above this
	// are discarded rather than folded, which is what removes the bias.
	//
	// This MUST stay an int. As a byte it overflows to 0 whenever size divides
	// 256 exactly - as the 16-character hex alphabet does - and then every
	// draw is rejected and this loop never terminates.
	limit := 256 - (256 % size)

	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, alphabet[int(b)%size])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// ValidateKeys checks the four rdpgw keys before anything is written, because
// the failure mode otherwise is invisible until a restart loses every session.
func ValidateKeys(c Config) error {
	for name, k := range map[string]string{
		"SessionKey":            c.SessionKey,
		"SessionEncryptionKey":  c.SessionEncryptionKey,
		"PAATokenSigningKey":    c.PAATokenSigningKey,
		"PAATokenEncryptionKey": c.PAATokenEncKey,
	} {
		if len(k) != KeyLength {
			return errors.New(name + " must be exactly 32 characters; rdpgw replaces anything else with a random value at boot")
		}
	}
	return nil
}
