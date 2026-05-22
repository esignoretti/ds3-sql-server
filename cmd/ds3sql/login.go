package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
)

func init() {
	loginCmd.Flags().String("host", "localhost", "DS3 SQL Server host")
	loginCmd.Flags().Int("port", 8080, "DS3 SQL Server port")
	loginCmd.Flags().String("tfa", "", "2FA code (optional)")
	loginCmd.Flags().String("tenant", "", "Tenant ID (optional)")
	loginCmd.Flags().String("api-url", "", "Custom IAM API URL (optional)")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Cubbit IAM",
	RunE: func(cmd *cobra.Command, args []string) error {
		host, _ := cmd.Flags().GetString("host")
		port, _ := cmd.Flags().GetInt("port")
		tfa, _ := cmd.Flags().GetString("tfa")
		tenant, _ := cmd.Flags().GetString("tenant")
		apiURL, _ := cmd.Flags().GetString("api-url")

		fmt.Print("Email: ")
		var email string
		fmt.Scanln(&email)

		fmt.Print("Password: ")
		var password string
		fmt.Scanln(&password)

		form := url.Values{
			"email":    {email},
			"password": {password},
		}
		if tfa != "" {
			form.Set("tfa_code", tfa)
		}
		if tenant != "" {
			form.Set("tenant_id", tenant)
		}
		if apiURL != "" {
			form.Set("api_url", apiURL)
		}

		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.PostForm(fmt.Sprintf("http://%s:%d/auth/login", host, port), form)
		if err != nil {
			return fmt.Errorf("login request failed: %w", err)
		}
		defer resp.Body.Close()

		// Extract token from Set-Cookie header
		var token string
		for _, c := range resp.Cookies() {
			if c.Name == "token" && c.Value != "" {
				token = c.Value
				break
			}
		}
		if token == "" {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("login failed: no token received\n%s", string(body))
		}

		cfg := &CLIConfig{
			Host:  host,
			Port:  port,
			Token: token,
		}
		if err := saveConfig(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Println("Logged in successfully")
		return nil
	},
}
