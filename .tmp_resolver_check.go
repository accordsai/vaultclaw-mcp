package main

import (
  "context"
  "encoding/json"
  "fmt"
  "os"

  "accords-mcp/internal/routing"
)

func main() {
  r, err := routing.NewDefaultResolver()
  if err != nil {
    panic(err)
  }
  reqText := "/vault send an email to skl83@cornell.edu with a copy of my passport and the passport fields in the body."
  if len(os.Args) > 1 {
    reqText = os.Args[1]
  }
  res := r.Resolve(context.Background(), routing.ResolveRequest{
    RequestText: reqText,
    Options: routing.ResolveOptions{AllowSearchFallback: true},
  }, nil)
  b, _ := json.MarshalIndent(res, "", "  ")
  fmt.Println(string(b))
}
