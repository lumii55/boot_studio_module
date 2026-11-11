package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

func secureEqual(a, b string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func linkOrCopyFile(source, destination string, mode os.FileMode) error {
	_ = os.Remove(destination)
	if err := os.Link(source, destination); err == nil {
		return os.Chmod(destination, mode)
	}
	return copyFile(source, destination, mode)
}

func saveMultipartFile(file io.Reader, destination string, limit int64) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	limited := io.LimitReader(file, limit+1)
	written, copyErr := io.Copy(output, limited)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		_ = os.Remove(destination)
		return errors.New("file too large")
	}
	return nil
}

func moveFileOrCopy(source, destination string, mode os.FileMode) error {
	if err := os.Rename(source, destination); err == nil {
		return os.Chmod(destination, mode)
	}
	if err := copyFile(source, destination, mode); err != nil {
		return err
	}
	return os.Remove(source)
}
