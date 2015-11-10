package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"

	"github.com/aclements/go-misc/internal/loganal"
)

// TODO: Search a set of log files or saved builder logs. If searching
// saved builder logs, optionally print to builder URLs instead of
// local file names.

// TODO: Optionally extract failures and show only those.

// TODO: Optionally classify matched logs by failure (and show either
// file name or extracted failure).

// TODO: Option to print Markdown-friendly output for GitHub.

// TODO: Option to print failure summary versus full failure message.

var (
	// TODO: Allow mulitple -e's like grep.
	flagRegexp = flag.String("-e", "", "show files matching `regexp`")
	re         *regexp.Regexp
)

func main() {
	// XXX What I want right now is just to point it at a bunch of
	// logs and have it extract the failures.
	flag.Parse()

	if *flagRegexp != "" {
		var err error
		re, err = regexp.Compile(*flagRegexp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad regexp: %v\n", err)
			os.Exit(2)
		}
	}

	// Process files
	status := 1
	for _, path := range flag.Args() {
		found, err := process(path)
		if err != nil {
			status = 2
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		} else if found && status == 1 {
			status = 0
		}
	}
	os.Exit(status)
}

func process(path string) (found bool, err error) {
	// TODO: Use streaming if possible.
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return false, err
	}

	// Check regexp match.
	if re != nil && !re.Match(data) {
		return false, nil
	}

	// Extract failures.
	failures, err := loganal.Extract(string(data), "", "")
	if err != nil {
		return false, err
	}

	// Print failures.
	for _, failure := range failures {
		msg := failure.FullMessage
		if msg == "" {
			msg = failure.Message
		}
		fmt.Printf("%s:\n%s\n\n", path, msg)
		continue
		lines := strings.Split(msg, "\n")
		for _, line := range lines {
			fmt.Printf("%s: %s\n", path, line)
		}
		fmt.Println()
	}
	return true, nil
}
