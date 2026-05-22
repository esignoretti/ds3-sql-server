package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show connection status and server health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		resp, err := http.Get(serverURL(cfg) + "/health")
		if err != nil {
			return fmt.Errorf("server unreachable: %w", err)
		}
		defer resp.Body.Close()

		var health struct {
			Status string `json:"status"`
		}
		json.NewDecoder(resp.Body).Decode(&health)

		fmt.Printf("Server:   %s\n", serverURL(cfg))
		fmt.Printf("Health:   %s\n", health.Status)
		fmt.Printf("Token:    %s…\n", cfg.Token[:min(8, len(cfg.Token))])

		meResp, err := authedGet(cfg, "/auth/me")
		if err == nil {
			var me struct {
				Email string `json:"email"`
			}
			json.Unmarshal(meResp, &me)
			fmt.Printf("User:     %s\n", me.Email)
		}

		return nil
	},
}

func authedGet(cfg *CLIConfig, path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", serverURL(cfg)+path, nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func authedPost(cfg *CLIConfig, path string, body []byte) ([]byte, error) {
	req, _ := http.NewRequest("POST", serverURL(cfg)+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
