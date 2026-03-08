package main

import (
	"fmt"
	"os"

	"accords-mcp/internal/mcp"
)

func main() {
	s := mcp.NewServer(os.Stdin, os.Stdout)
	if err := s.Serve(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "accords-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
