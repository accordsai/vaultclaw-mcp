package main

import (
  "context"
  "encoding/json"
  "fmt"
  "accords-mcp/internal/routing"
)

func check(text string) {
  r, err := routing.NewDefaultResolver()
  if err != nil { panic(err) }
  req := routing.ResolveRequest{RequestText: text, Options: routing.ResolveOptions{AllowSearchFallback: true}}
  res := r.Resolve(context.Background(), req, func(_ context.Context, _ routing.SearchFilter) ([]routing.SearchCandidate, error) { return nil, nil })
  b, _ := json.MarshalIndent(res, "", "  ")
  fmt.Printf("\n---\n%s\n%s\n", text, string(b))
}

func main() {
  check("send a copy of my w2 to skl83@cornell.edu")
  check("send an email with my w2 to skl83@cornell.edu")
  check("send an email with my passport to skl83@cornell.edu")
}
