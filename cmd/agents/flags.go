package main

import (
	"flag"
	"strings"
)

func parseFlags(flags *flag.FlagSet, args []string) error {
	return flags.Parse(orderFlagArgs(flags, args))
}

func orderFlagArgs(flags *flag.FlagSet, args []string) []string {
	flagArgs := make([]string, 0, len(args))
	positionalArgs := make([]string, 0, len(args))
	endOfFlags := false

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if endOfFlags {
			positionalArgs = append(positionalArgs, arg)
			continue
		}
		if arg == "--" {
			endOfFlags = true
			positionalArgs = append(positionalArgs, arg)
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positionalArgs = append(positionalArgs, arg)
			continue
		}

		name := strings.TrimPrefix(arg, "--")
		if name == arg {
			name = strings.TrimPrefix(arg, "-")
		}
		name = strings.SplitN(name, "=", 2)[0]
		definition := flags.Lookup(name)
		if definition == nil {
			flagArgs = append(flagArgs, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") || isBoolFlag(definition) || index+1 >= len(args) {
			continue
		}
		index++
		flagArgs = append(flagArgs, args[index])
	}

	return append(flagArgs, positionalArgs...)
}

func isBoolFlag(definition *flag.Flag) bool {
	boolFlag, ok := definition.Value.(interface{ IsBoolFlag() bool })
	return ok && boolFlag.IsBoolFlag()
}
