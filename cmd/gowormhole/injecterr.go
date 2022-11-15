package main

import (
	"errors"
	"fmt"
	"os"
)

func InjectError(pos string) error {
	if p := os.Getenv("INJECT_ERR_POS"); p != pos {
		return nil
	}
	if p := os.Getenv("INJECT_ERR"); p != "" {
		return errors.New(p)
	}

	return fmt.Errorf("inject error at pos %s", pos)
}
