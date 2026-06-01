package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newAPICommand(state *app) *cobra.Command {
	var body string

	cmd := &cobra.Command{
		Use:   "api <METHOD> <path>",
		Short: "Make a raw authenticated request to the API",
		Long:  "api sends a raw request to the Chief API and prints the JSON response. Authentication and content-type headers are applied by the chief.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]

			var reqBody any
			switch {
			case body == "":
			case strings.HasPrefix(body, "@"):
				raw, err := os.ReadFile(body[1:])
				if err != nil {
					return fmt.Errorf("read body file: %w", err)
				}
				reqBody = json.RawMessage(raw)
			default:
				reqBody = json.RawMessage(body)
			}

			var out json.RawMessage
			if _, err := state.chief.Do(cmd.Context(), method, path, reqBody, &out); err != nil {
				return err
			}
			return state.printer.writeRawJSON(out)
		},
	}

	cmd.Flags().StringVar(&body, "body", "", "request body as inline JSON or @file")
	return cmd
}
