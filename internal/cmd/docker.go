package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/keywaysh/cli/internal/api"
	"github.com/keywaysh/cli/internal/env"
	"github.com/spf13/cobra"
)

var dockerCmd = &cobra.Command{
	Use:   "docker [flags] <subcommand> [docker-args...]",
	Short: "Run Docker commands with injected secrets",
	Long: `Run Docker or Docker Compose commands with secrets injected from the vault.

For 'docker run': Secrets are injected as -e KEY=VALUE flags before the image name.
For 'docker compose': Secrets are exported to the environment before running.

User-provided -e flags take precedence over vault secrets.`,
	Example: `  keyway docker --env production run -p 8080:8080 myapp:latest
  keyway docker --env staging compose up -d
  keyway docker run --rm alpine env  # Uses default 'development' environment`,
	RunE: runDockerCmd,
}

func init() {
	dockerCmd.Flags().StringP("env", "e", "development", "Environment name")
	// Stop parsing flags after first positional arg so docker flags like --rm pass through
	dockerCmd.Flags().SetInterspersed(false)
}

// DockerOptions contains the parsed flags for the docker command
type DockerOptions struct {
	EnvName       string
	EnvFlagSet    bool
	DockerCommand string   // "run", "compose", etc.
	DockerArgs    []string // Arguments to pass to docker subcommand
}

// runDockerCmd is the entry point for the docker command (uses default dependencies)
func runDockerCmd(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("docker subcommand required (e.g., 'run' or 'compose')")
	}

	opts := DockerOptions{
		EnvFlagSet:    cmd.Flags().Changed("env"),
		DockerCommand: args[0],
		DockerArgs:    args[1:],
	}
	opts.EnvName, _ = cmd.Flags().GetString("env")

	return runDockerWithDeps(opts, defaultDeps)
}

// runDockerWithDeps is the testable version of runDocker
func runDockerWithDeps(opts DockerOptions, deps *Dependencies) error {
	// 1. Detect Repo
	repo, err := deps.Git.DetectRepo()
	if err != nil {
		deps.UI.Error("Not in a git repository with GitHub remote")
		return err
	}

	// 2. Ensure Login
	token, err := deps.Auth.EnsureLogin()
	if err != nil {
		deps.UI.Error(err.Error())
		return err
	}

	// 3. Setup Client
	client := deps.APIFactory.NewClient(token)
	ctx := context.Background()

	// 4. Determine Environment
	envName := opts.EnvName

	if !opts.EnvFlagSet && deps.UI.IsInteractive() {
		// Fetch available environments
		vaultEnvs, err := client.GetVaultEnvironments(ctx, repo)
		if err != nil || len(vaultEnvs) == 0 {
			vaultEnvs = []string{"development", "staging", "production"}
		}

		// Find default index (development)
		defaultIdx := 0
		for i, e := range vaultEnvs {
			if e == "development" {
				defaultIdx = i
				break
			}
		}

		// Reorder to put default first
		if defaultIdx > 0 {
			vaultEnvs[0], vaultEnvs[defaultIdx] = vaultEnvs[defaultIdx], vaultEnvs[0]
		}

		selected, err := deps.UI.Select("Environment:", vaultEnvs)
		if err != nil {
			return err
		}
		envName = selected
	}

	deps.UI.Step(fmt.Sprintf("Environment: %s", deps.UI.Value(envName)))

	// 5. Fetch Secrets
	var vaultContent string
	err = deps.UI.Spin("Fetching secrets...", func() error {
		resp, err := client.PullSecrets(ctx, repo, envName)
		if err != nil {
			return err
		}
		vaultContent = resp.Content
		return nil
	})

	if err != nil {
		if apiErr, ok := err.(*api.APIError); ok {
			deps.UI.Error(apiErr.Error())
		} else {
			deps.UI.Error(err.Error())
		}
		return err
	}

	// 6. Parse Secrets
	secrets := env.Parse(vaultContent)
	deps.UI.Success(fmt.Sprintf("Injecting %d secrets", len(secrets)))

	// 7. Execute Docker Command
	switch opts.DockerCommand {
	case "compose":
		return runDockerCompose(opts, secrets, deps)
	default:
		return runDockerRun(opts, secrets, deps)
	}
}

// runDockerRun handles docker run commands by injecting -e flags
func runDockerRun(opts DockerOptions, secrets map[string]string, deps *Dependencies) error {
	args := opts.DockerArgs

	// Extract user's -e flags to ensure they take precedence
	userEnvVars := extractUserEnvVars(args)

	// Find where to inject -e flags (before the image name)
	imagePos := findImagePosition(args)

	// Build new args with injected -e flags
	var newArgs []string

	// Add docker subcommand (e.g., "run")
	newArgs = append(newArgs, opts.DockerCommand)

	if imagePos >= 0 {
		// Add args before image
		newArgs = append(newArgs, args[:imagePos]...)

		// Inject vault secrets (excluding those user explicitly set)
		for k, v := range secrets {
			if _, userSet := userEnvVars[k]; !userSet {
				newArgs = append(newArgs, "-e", fmt.Sprintf("%s=%s", k, v))
			}
		}

		// Add image and remaining args
		newArgs = append(newArgs, args[imagePos:]...)
	} else {
		// No image found, inject secrets at the end of options
		for k, v := range secrets {
			if _, userSet := userEnvVars[k]; !userSet {
				newArgs = append(newArgs, "-e", fmt.Sprintf("%s=%s", k, v))
			}
		}
		newArgs = append(newArgs, args...)
	}

	// Execute docker with secrets as -e flags (not in environment)
	return deps.CmdRunner.RunCommand("docker", newArgs, nil)
}

// runDockerCompose handles docker compose commands by injecting secrets via -e flags
func runDockerCompose(opts DockerOptions, secrets map[string]string, deps *Dependencies) error {
	args := []string{"compose"}
	args = append(args, opts.DockerArgs...)

	// For "run" subcommand, inject -e flags (similar to docker run)
	// For other subcommands like "up", we need --env-file approach
	if len(opts.DockerArgs) > 0 && opts.DockerArgs[0] == "run" {
		// Find position after "run" to inject -e flags
		newArgs := []string{"compose", "run"}
		for k, v := range secrets {
			newArgs = append(newArgs, "-e", fmt.Sprintf("%s=%s", k, v))
		}
		// Append remaining args after "run"
		if len(opts.DockerArgs) > 1 {
			newArgs = append(newArgs, opts.DockerArgs[1:]...)
		}
		return deps.CmdRunner.RunCommand("docker", newArgs, nil)
	}

	// For "up" and other commands, use --env-file
	if len(secrets) > 0 {
		envFile, err := os.CreateTemp("", "keyway-env-*.env")
		if err != nil {
			return fmt.Errorf("failed to create temp env file: %w", err)
		}
		defer os.Remove(envFile.Name())

		for k, v := range secrets {
			fmt.Fprintf(envFile, "%s=%s\n", k, v)
		}
		envFile.Close()

		// Insert --env-file after "compose"
		args = []string{"compose", "--env-file", envFile.Name()}
		args = append(args, opts.DockerArgs...)
	}

	return deps.CmdRunner.RunCommand("docker", args, nil)
}

// findImagePosition finds the index where the image name starts in docker run args.
// Docker run syntax: docker run [OPTIONS] IMAGE [COMMAND] [ARG...]
// Returns -1 if no image position found.
func findImagePosition(args []string) int {
	// Flags that take a value (require skipping next arg)
	flagsWithValue := map[string]bool{
		"-a": true, "--attach": true,
		"--add-host": true,
		"--blkio-weight": true,
		"--blkio-weight-device": true,
		"--cap-add": true,
		"--cap-drop": true,
		"--cgroup-parent": true,
		"--cgroupns": true,
		"--cidfile": true,
		"--cpu-count": true,
		"--cpu-percent": true,
		"--cpu-period": true,
		"--cpu-quota": true,
		"--cpu-rt-period": true,
		"--cpu-rt-runtime": true,
		"--cpu-shares": true, "-c": true,
		"--cpus": true,
		"--cpuset-cpus": true,
		"--cpuset-mems": true,
		"--device": true,
		"--device-cgroup-rule": true,
		"--device-read-bps": true,
		"--device-read-iops": true,
		"--device-write-bps": true,
		"--device-write-iops": true,
		"--dns": true,
		"--dns-option": true,
		"--dns-search": true,
		"--domainname": true,
		"--entrypoint": true,
		"-e": true, "--env": true,
		"--env-file": true,
		"--expose": true,
		"--gpus": true,
		"--group-add": true,
		"--health-cmd": true,
		"--health-interval": true,
		"--health-retries": true,
		"--health-start-period": true,
		"--health-timeout": true,
		"-h": true, "--hostname": true,
		"--ip": true,
		"--ip6": true,
		"--ipc": true,
		"--isolation": true,
		"--kernel-memory": true,
		"-l": true, "--label": true,
		"--label-file": true,
		"--link": true,
		"--link-local-ip": true,
		"--log-driver": true,
		"--log-opt": true,
		"--mac-address": true,
		"-m": true, "--memory": true,
		"--memory-reservation": true,
		"--memory-swap": true,
		"--memory-swappiness": true,
		"--mount": true,
		"--name": true,
		"--network": true, "--net": true,
		"--network-alias": true, "--net-alias": true,
		"--oom-score-adj": true,
		"--pid": true,
		"--pids-limit": true,
		"--platform": true,
		"-p": true, "--publish": true,
		"--pull": true,
		"--restart": true,
		"--runtime": true,
		"--security-opt": true,
		"--shm-size": true,
		"--stop-signal": true,
		"--stop-timeout": true,
		"--storage-opt": true,
		"--sysctl": true,
		"--tmpfs": true,
		"--ulimit": true,
		"-u": true, "--user": true,
		"--userns": true,
		"--uts": true,
		"-v": true, "--volume": true,
		"--volume-driver": true,
		"--volumes-from": true,
		"-w": true, "--workdir": true,
	}

	i := 0
	for i < len(args) {
		arg := args[i]

		// Not a flag - this is the image
		if !strings.HasPrefix(arg, "-") {
			return i
		}

		// Check for --flag=value format
		if strings.Contains(arg, "=") {
			i++
			continue
		}

		// Check if this flag takes a value
		if flagsWithValue[arg] {
			// Skip the flag and its value
			i += 2
			continue
		}

		// Boolean flag, just skip it
		i++
	}

	return -1
}

// extractUserEnvVars parses -e and --env flags from docker args
func extractUserEnvVars(args []string) map[string]string {
	result := make(map[string]string)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		var envVal string
		if arg == "-e" || arg == "--env" {
			if i+1 < len(args) {
				envVal = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "-e=") {
			envVal = strings.TrimPrefix(arg, "-e=")
		} else if strings.HasPrefix(arg, "--env=") {
			envVal = strings.TrimPrefix(arg, "--env=")
		} else {
			continue
		}

		if envVal != "" {
			parts := strings.SplitN(envVal, "=", 2)
			if len(parts) >= 1 {
				key := parts[0]
				value := ""
				if len(parts) == 2 {
					value = parts[1]
				}
				result[key] = value
			}
		}
	}

	return result
}
