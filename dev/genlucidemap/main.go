//go:build ignore

// Command genlucidemap is a DEV tool that fetches lucide-static/tags.json from
// the CDN, computes TF-IDF tag weights, and emits an alias→concept JSON map
// for embedding in the iconlabel package.
//
// Run: go run dev/genlucidemap/main.go > internal/iconlabel/data/lucide_aliases.json
//
// The generated map is committed so building the tool needs no network access.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
)

const tagsURL = "https://cdn.jsdelivr.net/npm/lucide-static/tags.json"

// AliasEntry is a single alias→concept mapping with metadata.
type AliasEntry struct {
	Concept string  `json:"concept"`
	Weak    bool    `json:"weak"`
	IDF     float64 `json:"idf"`
}

const (
	kindName     = 3
	kindNameWord = 2
	kindTag      = 1
)

type candidate struct {
	concept string
	kind    int
	idf     float64
	weak    bool
}

func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(s, "-", " "))), " ")
}

func main() {
	// Fetch tags.json.
	resp, err := http.Get(tagsURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tags map[string][]string
	if err := json.Unmarshal(body, &tags); err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}
	N := len(tags)

	// Document frequency of each token across all icons.
	df := map[string]int{}
	for name, taglist := range tags {
		toks := map[string]bool{}
		for _, w := range strings.Fields(norm(name)) {
			toks[w] = true
		}
		for _, t := range taglist {
			for _, w := range strings.Fields(norm(t)) {
				toks[w] = true
			}
		}
		for w := range toks {
			df[w]++
		}
	}
	idf := func(w string) float64 {
		return math.Log(float64(N) / float64(1+df[w]))
	}
	weakDF := max(5, int(float64(N)*0.01))

	alias := map[string]*candidate{}
	add := func(a, concept string, kind int) {
		a = norm(a)
		if a == "" {
			return
		}
		w := idf(strings.Fields(a)[0])
		cur := alias[a]
		if cur == nil || kind > cur.kind || (kind == cur.kind && len(concept) < len(cur.concept)) {
			alias[a] = &candidate{
				concept: concept,
				kind:    kind,
				idf:     w,
				weak:    kind == kindTag && df[strings.Fields(a)[0]] > weakDF,
			}
		}
	}

	// Pass 1: icon names (highest priority).
	for name := range tags {
		c := norm(name)
		add(c, c, kindName)
	}
	// Pass 2: name words.
	for name := range tags {
		c := norm(name)
		for _, w := range strings.Fields(c) {
			add(w, c, kindNameWord)
		}
	}
	// Pass 3: tags and tag words (lowest priority).
	for name, taglist := range tags {
		c := norm(name)
		for _, t := range taglist {
			tn := norm(t)
			add(tn, c, kindTag)
			for _, w := range strings.Fields(tn) {
				add(w, c, kindTag)
			}
		}
	}

	// Emit as {alias: {concept, weak, idf}}.
	out := map[string]AliasEntry{}
	for a, c := range alias {
		out[a] = AliasEntry{Concept: c.concept, Weak: c.weak, IDF: math.Round(c.idf*1000) / 1000}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode error:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "generated %d aliases from %d icons (weak_df=%d)\n", len(out), N, weakDF)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
