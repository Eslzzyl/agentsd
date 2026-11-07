package main

import (
	"flag"
	"testing"
)

func TestParseFlagsAcceptsOptionsAfterPositionals(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	namespace := flags.String("n", "", "namespace")
	jsonOutput := flags.Bool("json", false, "print JSON")

	if err := parseFlags(flags, []string{"session-id", "--json", "-n", "agentsd"}); err != nil {
		t.Fatal(err)
	}
	if flags.NArg() != 1 || flags.Arg(0) != "session-id" {
		t.Fatalf("positionals = %#v", flags.Args())
	}
	if !*jsonOutput || *namespace != "agentsd" {
		t.Fatalf("parsed flags = json:%t namespace:%q", *jsonOutput, *namespace)
	}
}

func TestParseFlagsPreservesDoubleDashPositionals(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "print JSON")

	if err := parseFlags(flags, []string{"--", "--json"}); err != nil {
		t.Fatal(err)
	}
	if flags.NArg() != 1 || flags.Arg(0) != "--json" {
		t.Fatalf("positionals = %#v", flags.Args())
	}
	if *jsonOutput {
		t.Fatal("--json after -- was parsed as a flag")
	}
}
