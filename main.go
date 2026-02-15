package main

import (
	"log"
	"os"

	"rfs/cli"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "up", "ls", "down", "logs":
			cli.RunCLI()
			return
		case "daemon":
			d := cli.NewDaemon()
			if err := d.Start(); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	cli.PrintUsage()
	os.Exit(1)
}
