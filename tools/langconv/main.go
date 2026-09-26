// Command langconv converts Pure Blog's lang/*.php files into the JSON
// files embedded by the Go port (web/lang/*.json), renaming the product to
// PureBlog-Go in the translated strings.
//
//	go run ./tools/langconv -src ../pureblog/lang -dst web/lang
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/johanvanrhyn/pureblog-go/internal/phparray"
)

// productName matches the upstream name as it appears in translations,
// including hyphenated compounds such as "Pure-Blog-Verwaltung".
var productName = regexp.MustCompile(`Pure[ -]Blog`)

func main() {
	src := flag.String("src", "../pureblog/lang", "directory containing lang/*.php")
	dst := flag.String("dst", "web/lang", "output directory for *.json")
	flag.Parse()

	files, err := filepath.Glob(filepath.Join(*src, "*.php"))
	if err != nil || len(files) == 0 {
		log.Fatalf("no lang files found in %s", *src)
	}
	if err := os.MkdirAll(*dst, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		v, err := phparray.Parse(string(raw))
		if err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		out = productName.ReplaceAll(out, []byte("PureBlog-Go"))
		name := strings.TrimSuffix(filepath.Base(f), ".php") + ".json"
		if err := os.WriteFile(filepath.Join(*dst, name), append(out, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", name)
	}
}
