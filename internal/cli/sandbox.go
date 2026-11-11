package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/jeefy/convergence/internal/config"
	"github.com/jeefy/convergence/internal/sandbox"
)

var (
	sandboxJSON   bool
	sandboxMounts []string
)

var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Inspect and manage agent sandboxing runtime",
	Long: `Inspect and manage agent execution sandboxes.

Supported runtimes:
  none        No sandboxing (default)
  openshell   NVIDIA OpenShell gateway container
  nono        nono kernel-enforced process sandbox (Landlock/Seatbelt)
  docker-sbx  Docker Sandboxes (sbx)
  k8s-agent   Kubernetes agents.x-k8s.io Sandbox`,
}

var sandboxStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status and reachability of the configured sandboxing runtime",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		driver, err := sandbox.GetDriver(cfg.Sandbox, cfg)
		if err != nil {
			return err
		}
		status, err := driver.Status(cmd.Context())
		if err != nil {
			return err
		}
		if sandboxJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(status)
		}
		fmt.Printf("Sandbox runtime: %s\n", status.Type)
		if status.Type == config.SandboxNone {
			fmt.Println("No sandboxing runtime is configured (enable with 'convergence init --sandbox openshell').")
			return nil
		}
		fmt.Printf("Available:       %v\n", status.Available)
		fmt.Printf("Message:         %s\n", status.Message)
		if status.Endpoint != "" {
			fmt.Printf("Endpoint:        %s\n", status.Endpoint)
		}
		if driver.InferenceURL() != "" {
			fmt.Printf("Inference URL:   %s\n", driver.InferenceURL())
		}
		return nil
	},
}

var sandboxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List supported sandbox runtimes",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Supported sandboxing runtimes:")
		fmt.Println("  none        No sandboxing (default)")
		fmt.Println("  openshell   NVIDIA OpenShell container gateway and supervisor")
		fmt.Println("  nono        nono kernel-enforced process sandbox (Landlock/Seatbelt)")
		fmt.Println("  docker-sbx  Docker Sandboxes (sbx CLI / microVM)")
		fmt.Println("  k8s-agent   Kubernetes agents.x-k8s.io sandbox controller")
	},
}

var sandboxRunCmd = &cobra.Command{
	Use:   "run <agent> [args...]",
	Short: "Run an AI agent inside the configured sandbox runtime",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return err
		}
		driver, err := sandbox.GetDriver(cfg.Sandbox, cfg)
		if err != nil {
			return err
		}
		if cfg.Sandbox == config.SandboxNone {
			return fmt.Errorf("no sandbox runtime configured; configure with 'convergence init --sandbox nono' (or openshell)")
		}

		if err := preflightSkills(cmd.Context(), cfg, cmd.ErrOrStderr()); err != nil {
			return err
		}

		agent := args[0]
		agentArgs := args[1:]

		execCmd, envVars, err := driver.RunCommand(agent, sandbox.AgentArgs(agent, agentArgs, driver.InferenceURL()), sandboxMounts...)
		if err != nil {
			return err
		}

		execPath, err := exec.LookPath(execCmd[0])
		if err != nil {
			return fmt.Errorf("command %q not found on PATH: %w", execCmd[0], err)
		}

		c := exec.CommandContext(cmd.Context(), execPath, execCmd[1:]...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Env = append(os.Environ(), envVars...)
		return c.Run()
	},
}

func init() {
	sandboxStatusCmd.Flags().BoolVar(&sandboxJSON, "json", false, "Output status as JSON")
	sandboxRunCmd.Flags().StringSliceVarP(&sandboxMounts, "mount", "v", nil, "Mount directory or path into the sandbox runtime (e.g. -v /path/to/dir)")
	sandboxCmd.AddCommand(sandboxStatusCmd)
	sandboxCmd.AddCommand(sandboxListCmd)
	sandboxCmd.AddCommand(sandboxRunCmd)
	rootCmd.AddCommand(sandboxCmd)
}
