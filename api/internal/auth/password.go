// Package auth — вход по email и паролю, JWT и refresh-сессии (ТЗ §9.1, §10.3).
package auth

import (
	"errors"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost — ТЗ §3.1: bcrypt, cost 12.
const bcryptCost = 12

// MinPasswordLength — ТЗ §7.1: пароль не короче 10 символов.
const MinPasswordLength = 10

var ErrPasswordTooShort = errors.New("пароль короче 10 символов")

// HashPassword проверяет длину и хеширует пароль bcrypt с cost 12.
func HashPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword сравнивает пароль с хешем за постоянное время.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
