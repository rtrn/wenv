// +build linux

// Wenv passes environment variables to Windows applications
// started from Windows Subsystem for Linux (WSL).
//
// Usage:
//
//	wenv 'var=x' ... command.exe [arg ...]
//	[var=x ...] wenv command.exe [arg ...]
//
// The first form will only pass the variables specified on the command line.
// The second form takes the whole environment and passes it to
// the command while following the rules stated below.
//
// Note that environment variables are case sensitive inside WSL and case insensitive
// on the Windows side.
//
// Default Rules
//
// The following environment variables are ignored: home, path, ifs, IFS, SHELL,
// prompt, EDITOR, PAGER, BROWSER.
// And the following variables are converted to Windows paths: HOME, GOBIN.
// Finally, ``PATH'' and ``GOPATH'' are converted such that they match their Windows equivalent.
// Every other variable is passed as-is.
//
// These can be changed and new rules can be added using the ``WENV'' environment
// variable, which is a comma-separated list of variables optionally prefixed by a modifier:
//
//	WENV='var1, !var2, @var3, #var4, $var5'
//
// Variables prefixed by ``!'' are ignored.  Those prefixed by ``@'' are converted to Windows
// paths.  ``#'' denotes a path variable.  ``$'' is the same as no prefix and can be used to
// pass a variable whose name would otherwise be interpreted as a modifier.
//
// Wrapper Scripts
//
// Consider creating wrapper scripts for commands you run often:
//
//	#!/bin/sh
//	exec wenv command.exe "$@"
//
// Or if you use rc:
//
//	#!/usr/local/plan9/bin/rc
//	exec wenv command.exe $*
//
package main // import "rtrn.io/cmd/wenv"

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
)

const helper = "wenvhelper.exe"

type varopt int

const (
	varIgnore varopt = iota
	varPass
	varConvert
	varPath
)

var varopts = map[string]varopt{
	"home":    varIgnore,
	"path":    varIgnore,
	"ifs":     varIgnore,
	"IFS":     varIgnore,
	"SHELL":   varIgnore,
	"prompt":  varIgnore,
	"EDITOR":  varIgnore,
	"PAGER":   varIgnore,
	"BROWSER": varIgnore,

	"HOME":  varConvert,
	"GOBIN": varConvert,

	"PATH":   varPath,
	"GOPATH": varPath,
}

func main() {
	log.SetPrefix("wenv: ")
	log.SetFlags(0)
	os.Exit(wenv())
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wenv 'var=x' ... command.exe [arg ...]")
	fmt.Fprintln(os.Stderr, "       [var=x ...] wenv command.exe [arg ...]")
	os.Exit(2)
}

func wenv() int {
	os.Args = os.Args[1:]
	if len(os.Args) == 0 {
		usage()
	}

	vars := make(map[string]string)
	for len(os.Args) > 0 {
		v := strings.SplitN(os.Args[0], "=", 2)
		if len(v) != 2 {
			break
		}
		vars[v[0]] = v[1]
		os.Args = os.Args[1:]
	}
	if len(os.Args) == 0 {
		usage()
	}
	if len(vars) == 0 {
		if err := getvaropts(); err != nil {
			log.Print(err)
			return 1
		}
		for _, e := range os.Environ() {
			v := strings.SplitN(e, "=", 2)
			if opt, ok := varopts[v[0]]; ok {
				switch opt {
				case varIgnore:
					continue
				case varPass:
				case varConvert:
					var err error
					v[1], err = winpath(v[1])
					if err != nil {
						continue
					}
				case varPath:
					var p []string
					for _, e := range strings.Split(v[1], ":") {
						e, err := winpath(e)
						if err != nil {
							continue
						}
						p = append(p, e)
					}
					v[1] = strings.Join(p, ";")
				default:
					log.Print("invalid varopt: ", opt)
					return 1
				}
			}
			vars[v[0]] = v[1]
		}
	}

	path, err := exec.LookPath(os.Args[0])
	if err != nil {
		log.Print(err)
		return 127
	}
	path, err = winpath(path)
	if err != nil {
		log.Printf("%s: could not convert to Windows path", os.Args[0])
		return 126
	}

	cmdout, err := exec.Command("cmd.exe", "/c", "echo %TEMP%").Output()
	if err != nil {
		log.Printf("exec cmd.exe: %v", err)
		return 1
	}
	tempdir := wslpath(strings.TrimSpace(string(cmdout)))
	file, err := ioutil.TempFile(tempdir, "wenv")
	if err != nil {
		log.Printf("creating temp file: %v", err)
		return 1
	}

	enc := gob.NewEncoder(file)
	if err := enc.Encode(vars); err != nil {
		log.Printf("gob encoding: %v", err)
		return 1
	}
	if err := file.Close(); err != nil {
		log.Printf("closing temp file: %v", err)
		return 1
	}
	defer os.Remove(file.Name())

	cmd, err := exec.LookPath(helper)
	if err != nil {
		log.Print(err)
		return 1
	}
	winfile, err := winpath(file.Name())
	if err != nil {
		log.Printf("%s: could not convert to Windows path", file.Name())
		return 1
	}
	args := append([]string{helper}, winfile, path)
	args = append(args, os.Args...)
	if err := syscall.Exec(cmd, args, os.Environ()); err != nil {
		log.Printf("exec: %v", err)
		return 1
	}
	panic("not reached")
}

func getvaropts() error {
	wenv := os.Getenv("WENV")
	if wenv == "" {
		return nil
	}

	for _, e := range strings.Split(wenv, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}

		opt := varPass
		switch e[0] {
		case '!':
			opt = varIgnore
			e = e[1:]
		case '@':
			opt = varConvert
			e = e[1:]
		case '#':
			opt = varPath
			e = e[1:]
		case '$':
			e = e[1:]
		}

		if e == "" {
			return errors.New("empty var name in WENV")
		}
		varopts[e] = opt
	}
	return nil
}

var wslRoot string

// convert WSL path to Windows path
func winpath(path string) (string, error) {
	if wslRoot == "" {
		getWSLRoot()
	}
	re := regexp.MustCompile("^" + wslRoot + "([a-z])(/|$)")
	match := re.FindStringSubmatch(path)
	if match != nil {
		repl := strings.ToUpper(match[1]) + ":"
		re = regexp.MustCompile("^" + wslRoot + "[a-z]")
		path = re.ReplaceAllString(path, repl)
	}
	path = strings.Replace(path, "/", "\\", -1)

	if strings.HasPrefix(path, "\\") {
		return "", errors.New("could not convert path")
	}
	return path, nil
}

// convert Windows path to WSL path
func wslpath(path string) string {
	if wslRoot == "" {
		getWSLRoot()
	}
	path = strings.Replace(path, "\\", "/", -1)
	re := regexp.MustCompile("^([A-Za-z]):")
	match := re.FindStringSubmatch(path)
	if match != nil {
		repl := wslRoot + strings.ToLower(match[1])
		path = re.ReplaceAllString(path, repl)
	}
	return path
}

func getWSLRoot() {
	wslRoot = "/mnt/"
	b, err := ioutil.ReadFile("/etc/wsl.conf")
	if err != nil {
		return
	}
	split := strings.Split(string(b), "\n")
	for _, s := range split {
		if !strings.Contains(s, "root") {
			continue
		}
		split := strings.Split(s, "=")
		if len(split) != 2 {
			return
		}
		s = strings.TrimSpace(split[1])
		if s[0] == '"' {
			s = s[1 : len(s)-1]
		}
		wslRoot = s
		return
	}
}
