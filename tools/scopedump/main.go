// Command scopedump prints the ESI scopes HANGAR's sync set requires,
// derived from the embedded spec snapshot rather than hand-listed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hangar-project/hangar/internal/sync/worker"
)

type spec struct {
	Paths map[string]map[string]struct {
		Security []map[string][]string `json:"security"`
	} `json:"paths"`
}

func main() {
	raw, err := os.ReadFile("internal/esi/catalogue/embedded/openapi.snapshot.json")
	if err != nil {
		panic(err)
	}
	var s spec
	if err := json.Unmarshal(raw, &s); err != nil {
		panic(err)
	}

	getScopes := map[string][]string{}
	otherScopes := map[string][]string{}
	var unmatched []string

	for path := range worker.SyncSet() {
		key := strings.TrimSuffix(path, "/")
		ops, ok := s.Paths[key]
		if !ok {
			ops, ok = s.Paths[key+"/"]
		}
		if !ok {
			unmatched = append(unmatched, path)
			continue
		}
		for method, op := range ops {
			for _, req := range op.Security {
				for _, list := range req {
					for _, scope := range list {
						if method == "get" {
							getScopes[scope] = append(getScopes[scope], path)
						} else {
							otherScopes[scope] = append(otherScopes[scope], method+" "+path)
						}
					}
				}
			}
		}
	}

	keys := func(m map[string][]string) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}

	get := keys(getScopes)
	fmt.Printf("GET-required scopes: %d\n", len(get))
	for _, s := range get {
		fmt.Println(s)
	}

	fmt.Printf("\nscopes required ONLY by non-GET operations on sync-set paths: \n")
	for _, s := range keys(otherScopes) {
		if _, alsoGet := getScopes[s]; !alsoGet {
			fmt.Printf("  %s   <- %v\n", s, otherScopes[s])
		}
	}
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		fmt.Printf("\nSYNC-SET PATHS ABSENT FROM THE SPEC (Principle 5 violation candidates):\n")
		for _, p := range unmatched {
			fmt.Println(" ", p)
		}
	}
}
