// Command cartledger is the entry point. With no subcommand it runs the HTTP
// server (preserving the pre-cobra behavior); `backup`, `restore`, `serve`,
// and `version` subcommands are wired up in cli.go.
package main

func main() {
	Execute()
}
