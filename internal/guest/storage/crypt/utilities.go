//go:build linux
// +build linux

package crypt

import (
	"crypto/rand"
	"io/ioutil"
	"regexp"

	"github.com/pkg/errors"
)

func getUniqueName(path string) (name string, err error) {
	// Make a Regex to say we only want letters and numbers
	reg, err := regexp.Compile("[^a-zA-Z0-9]+")
	if err != nil {
		return "", err
	}
	// Replace all non-alphanumeric characters by dashes
	return reg.ReplaceAllString(path, "-"), nil
}

// generateKeyFile generates a file with random values.
func generateKeyFile(path string, size int64) error {
	// The crypto.rand interface generates random numbers using /dev/urandom
	keyArray := make([]byte, size)
	_, err := rand.Read(keyArray[:])
	if err != nil {
		return errors.Wrap(err, "failed to generate key array")
	}

	if err := ioutil.WriteFile(path, keyArray[:], 0644); err != nil {
		return errors.Wrap(err, "failed to save key to file")
	}

	return nil
}
