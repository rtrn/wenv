// +build windows

// Helper application for rtrn.io/cmd/wenv.
//
// The first argument is the name of the file containing the gob-encoded
// environment variables.  The rest of the arguments specify the
// command to run and its arguments.
package main // import "rtrn.io/cmd/wenv/wenvhelper"

import (
	"encoding/gob"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const fileprefix = "wenv"

func main() {
	log.SetPrefix("wenvhelper: ")
	log.SetFlags(0)

	os.Args = os.Args[1:]
	if len(os.Args) < 3 {
		log.Fatal("too few arguments")
	}
	if !strings.HasPrefix(filepath.Base(os.Args[0]), fileprefix) {
		log.Fatalf("%s: invalid first argument", os.Args[0])
	}

	file, err := os.Open(os.Args[0])
	if err != nil {
		log.Fatal(err)
	}
	os.Args = os.Args[1:]
	var vars map[string]string
	enc := gob.NewDecoder(file)
	if err := enc.Decode(&vars); err != nil {
		log.Fatalf("gob decode: %v", err)
	}
	if err := file.Close(); err != nil {
		log.Fatalf("closing temp file: %v", err)
	}
	if err := os.Remove(file.Name()); err != nil {
		log.Fatalf("removing temp file: %v", err)
	}

	for k, v := range vars {
		if err := os.Setenv(k, v); err != nil {
			log.Fatalf("setenv: %v", err)
		}
	}

	cmd := exec.Cmd{
		Path:   os.Args[0],
		Args:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if err := cmd.Start(); err != nil {
		log.Print(err)
		os.Exit(126)
	}
	code := 0
	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				code = status.ExitStatus()
			}
		} else {
			log.Print(err)
			os.Exit(126)
		}
	}
	os.Exit(code)
}
