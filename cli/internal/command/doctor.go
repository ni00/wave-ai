package command

import (
	"fmt"
	"net/url"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

func doctorCommand(s *settings) *cobra.Command {
	var offline bool
	cmd := &cobra.Command{Use: "doctor", Short: "Check connection configuration, database health and API authentication", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		connection, err := s.connection()
		if err != nil {
			return err
		}
		result := publicConnection(connection)
		if offline {
			result["network_checked"] = false
			return emit(cmd, result)
		}
		c, err := wave.New(connection.URL, connection.Key)
		if err != nil {
			return err
		}
		ctx, cancel := s.context(cmd)
		defer cancel()
		if _, err = c.Call(ctx, "health", wave.Options{}); err != nil {
			return fmt.Errorf("health check: %w", err)
		}
		result["health"] = "ok"
		if connection.Key == "" {
			_ = emit(cmd, result)
			return &exitError{code: 3, err: fmt.Errorf("no Wave API key configured"), hint: "Set WAVE_API_KEY or run config set NAME --key-stdin."}
		}
		if _, err = c.Call(ctx, "agentsList", wave.Options{Query: url.Values{"limit": []string{"1"}}}); err != nil {
			return fmt.Errorf("API authentication check: %w", err)
		}
		result["api_authentication"] = "ok"
		result["network_checked"] = true
		return emit(cmd, result)
	}}
	cmd.Flags().BoolVar(&offline, "offline", false, "validate configuration without sending any HTTP request")
	return cmd
}
