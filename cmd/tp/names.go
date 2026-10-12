package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
)

//go:embed data/names.json
var namesJSON []byte

var namesDict map[string][]string

var nameTemplates = []string{
	"n+n+dN", "adj-no+n+dN", "adj-i+n+dN", "n+vi+dN", "n+vt+dN",
	"vi+n+dN", "vt+n+dN", "adv+vi+dN", "surname+given+dN", "given+surname+dN",
	"place+n+dN", "n+place+dN", "adj-i+given+dN", "adj-no+surname+dN",
	"surname+n+dN", "n+given+dN", "place+surname+dN", "given+place+dN",
	"n+n+n+dN", "adj-i+n+n+dN",
}

func init() {
	if err := json.Unmarshal(namesJSON, &namesDict); err != nil {
		panic(err)
	}
}

func generateSessionName(rng *rand.Rand) string {
	tpl := nameTemplates[rng.IntN(len(nameTemplates))]
	parts := strings.Split(tpl, "+")
	for i, tag := range parts {
		switch tag {
		case "dN":
			parts[i] = fmt.Sprint(rng.IntN(101))
		default:
			words := namesDict[tag]
			parts[i] = words[rng.IntN(len(words))]
		}
	}
	return strings.Join(parts, "-")
}
