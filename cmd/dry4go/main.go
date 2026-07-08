package main

import (
	"fmt"
	"os"

	"github.com/mdw-tools/dry4go/internal/dry"
)

func main() {
	options, err := dry.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, dry.Usage)
		os.Exit(2)
	}
	if options.Help {
		fmt.Println(dry.Usage)
		return
	}
	candidates, err := dry.FindDuplicates(options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch options.Format {
	case "text":
		fmt.Print(dry.FormatText(candidates))
	case "json":
		text, err := dry.FormatJSON(candidates)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(text)
	default:
		fmt.Fprintln(os.Stderr, "unknown format:", options.Format)
		os.Exit(2)
	}
}
