// Copyright 2023 Christopher Briscoe.  All rights reserved.

package auth

import (
	"net/mail"

	"github.com/cwbriscoe/goutil/str"
)

const (
	minUsernameLen = 4
	maxUsernameLen = 20
	minPasswordLen = 10
	maxPasswordLen = 32
	maxEmailLen    = 320
)

func (a *Auth) validateRegistration(reg *register) []byte {
	if !emailValid(reg.Email) {
		return []byte("{\"error\":\"invalid email address\"}")
	}

	if reason := checkUsername(reg.User); reason != nil {
		return reason
	}

	if reason := checkPassword(reg.Pass); reason != nil {
		return reason
	}

	userExists, emailExists, err := a.checkAlreadyExists(reg)
	if userExists {
		return []byte("{\"error\":\"user name already exists\"}")
	}
	if emailExists {
		return []byte("{\"error\":\"email address already exists\"}")
	}
	if err != nil {
		a.log.Err(err).Msg("validateRegistration: error validating data with the db")
		return []byte("{\"error\":\"internal server error\"}")
	}

	return nil
}

func emailValid(email string) bool {
	if len(email) > maxEmailLen {
		return false
	}
	_, err := mail.ParseAddress(email)
	return err == nil
}

func checkUsername(user string) []byte {
	invalidLength := []byte("{\"error\":\"Invalid user name.  Must have a length >= 4 and <= 20.\"}")
	invalidUsername := []byte("{\"error\":\"Invalid user name.  Must only contain characters: [a-z][A-Z][0-9].\"}")

	if len(user) < minUsernameLen || len(user) > maxUsernameLen {
		return invalidLength
	}

	if user != str.ToASCII(user) {
		return invalidUsername
	}

	firstChar := true
	for _, char := range user {
		if firstChar && !str.IsLower(char) && !str.IsUpper(char) {
			return []byte("{\"error\":\"Invalid user name.  First character has to be alphabetic: [a-z][A-Z].\"}")
		}

		if !str.IsLower(char) && !str.IsUpper(char) && !str.IsDigit(char) {
			return invalidUsername
		}

		firstChar = false
	}

	return nil
}

func checkPassword(pass string) []byte {
	invalidLength := []byte("{\"error\":\"Invalid password.  Must have a length >= 10 and <= 32.\"}")
	invalidPassword := []byte("{\"error\":\"Invalid password.  Must only contain characters: [a-z][A-Z][0-9][ !#$%&()*+,-./:;<=>?@^_{|}~]\"}")

	if len(pass) < minPasswordLen || len(pass) > maxPasswordLen {
		return invalidLength
	}

	if pass != str.ToASCII(pass) {
		return invalidPassword
	}

	var lwr, upr, num, spl bool
	for _, char := range pass {
		switch {
		case str.IsLower(char):
			lwr = true
		case str.IsUpper(char):
			upr = true
		case str.IsDigit(char):
			num = true
		case str.IsSpecial(char):
			spl = true
		case str.IsSpace(char):
			continue
		default:
			return invalidPassword
		}
	}

	if !lwr || !upr || !num || !spl {
		return []byte("{\"error\":\"Invalid password.  Must contain at least one character from each category: [a-z][A-Z][0-9][!#$%&()*+,-./:;<=>?@^_{|}~]\"}")
	}

	return nil
}
