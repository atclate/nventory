package nvclient

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/howeyc/gopass"
)

func PromptUserConfirmation(message string, f *bufio.Reader) bool {
	fmt.Print(message)
	response, err := readLine(f)
	if err != nil {
		return false
	}
	r := []rune(response)
	if len(r) > 0 {
		if r[0] == 'y' || r[0] == 'Y' {
			return true
		} else {
			return false
		}
	}
	return PromptUserConfirmation(message, f)
}

func PromptUserLogin(user string, f *bufio.Reader) (u, p string, err error) {
	if user != "" && user == autoreg {
		print("Login: ")
		response, err := readLine(f)
		if err != nil {
			return "", "", errors.New("Failed reading user input.")
		}
		user = response
	}
	// User typed something in for username
	print("Password: ")
	passwdarr, err := gopass.GetPasswd()
	return user, string(passwdarr), err
}

func readLine(f *bufio.Reader) (string, error) {
	line, prefix, err := f.ReadLine()
	for prefix && err == nil {
		var l []byte
		l, prefix, err = f.ReadLine()
		line = append(line, l...)
	}
	return string(line), err
}

func serializeJSON(a interface{}) (string, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func deserializeJSONCookie(s string, m *http.Cookie) error {
	err := json.Unmarshal([]byte(s), m)
	return err
}
