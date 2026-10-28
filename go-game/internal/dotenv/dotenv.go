// Package dotenv loads a .env file into the process environment.
package dotenv

import (
	"bytes"
	"os"

	"github.com/joho/godotenv"
)

// Load reads the file at path and sets each variable that the environment
// doesn't already have, as godotenv.Load does. It also accepts a UTF-8 byte
// order mark, which Windows editors may add and godotenv rejects. A missing
// file is an error that satisfies os.IsNotExist.
func Load(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	vars, err := godotenv.Unmarshal(string(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	if err != nil {
		return err
	}
	for key, value := range vars {
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return nil
}
