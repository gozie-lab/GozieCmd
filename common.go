package main

// Közös segédfüggvények a felülethez (Windows és Linux).

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func describe(items []*Entry) string {
	if len(items) == 1 {
		return fmt.Sprintf(T("„%s”"), items[0].Name)
	}
	return Nf(len(items), "%d elem")
}

func errText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		err = le.Err
	}
	if en, ok := err.(syscall.Errno); ok {
		if t := errnoText(en); t != "" {
			return t
		}
		return en.Error()
	}
	return err.Error()
}
