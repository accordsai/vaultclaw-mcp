package main

import (
  "context"
  "encoding/json"
  "fmt"
  "accords-mcp/internal/routing"
)

func main() {
  r, err := routing.NewDefaultResolver()
  if err != nil { panic(err) }
  tests := []string{
    "send an email to skl83@cornell.edu with a copy of my w-2. subject is hi",
    "send an email to skl83@cornell.edu with a copy of my passport. subject is hi",
    "send an email to skl83@cornell.edu with a copy of my w2. subject is hi",
  }
  for _, t := range tests {
    res := r.Resolve(context.Background(), routing.ResolveRequest{RequestText:t, Options:routing.ResolveOptions{AllowSearchFallback:true}}, func(_ context.Context, _ routing.SearchFilter) ([]routing.SearchCandidate, error) { return nil, nil })
    b,_ := json.MarshalIndent(res, "", "  ")
    fmt.Printf("\n=== %s\n%s\n", t, string(b))
  }
}
