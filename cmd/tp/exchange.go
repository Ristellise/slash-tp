package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func publishCode(code, codeFile, kind string) error {
	if codeFile == "" {
		fmt.Println(code)
		return nil
	}
	return os.WriteFile(codeFile+"."+kind, []byte(code), 0o600)
}

func readCode(codeFile, kind, prompt string) (string, error) {
	if codeFile != "" {
		path := codeFile + "." + kind
		for {
			if b, err := os.ReadFile(path); err == nil {
				if m := codeRe.Find(b); m != nil {
					return string(m), nil
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	fmt.Fprint(os.Stderr, prompt)
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return line, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}
