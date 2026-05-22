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
	loginCmd.Flags().String("host", "localhost", "DS3 SQL Server host")
	loginCmd.Flags().Int("port", 8080, "DS3 SQL Server port")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Cubbit IAM",
	RunE: func(cmd *cobra.Command, args []string) error {
		host, _ := cmd.Flags().GetString("host")
		port, _ := cmd.Flags().GetInt("port")

		fmt.Print("Email: ")
		var email string
		fmt.Scanln(&email)

		fmt.Print("Password: ")
		var password string
		fmt.Scanln(&password)

		body, _ := json.Marshal(map[string]string{
			"email":    email,
			"password": password,
		})

		resp, err := http.Post(fmt.Sprintf("http://%s:%d/auth/login", host, port), "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("login request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("login failed (%d): %s", resp.StatusCode, string(b))
		}

		var result struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}

		cfg := &CLIConfig{
			Host:  host,
			Port:  port,
			Token: result.Token,
		}

		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Println("Logged in successfully")
		return nil
	},
}
