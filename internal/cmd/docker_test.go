package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/keywaysh/cli/internal/api"
)

func TestRunDockerWithDeps_DockerRun_Success(t *testing.T) {
	deps, _, _, _, cmdRunner, apiClient := NewTestDepsWithRunner()

	apiClient.PullResponse = &api.PullSecretsResponse{
		Content: "API_KEY=secret123\nDB_URL=postgres://localhost",
	}

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		DockerArgs:    []string{"-p", "8080:8080", "myapp:latest"},
	}

	err := runDockerWithDeps(opts, deps)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify docker was called
	if cmdRunner.LastCommand != "docker" {
		t.Errorf("expected command 'docker', got %q", cmdRunner.LastCommand)
	}

	// Verify first arg is "run"
	if len(cmdRunner.LastArgs) == 0 || cmdRunner.LastArgs[0] != "run" {
		t.Errorf("expected first arg 'run', got %v", cmdRunner.LastArgs)
	}

	// Verify -e flags were injected
	argsStr := strings.Join(cmdRunner.LastArgs, " ")
	if !strings.Contains(argsStr, "-e API_KEY=secret123") {
		t.Errorf("expected API_KEY to be injected, got args: %v", cmdRunner.LastArgs)
	}
	if !strings.Contains(argsStr, "-e DB_URL=postgres://localhost") {
		t.Errorf("expected DB_URL to be injected, got args: %v", cmdRunner.LastArgs)
	}

	// Verify image is still at the end
	if cmdRunner.LastArgs[len(cmdRunner.LastArgs)-1] != "myapp:latest" {
		t.Errorf("expected image at end, got args: %v", cmdRunner.LastArgs)
	}

	// Verify secrets were NOT passed via environment (docker run uses -e flags)
	if cmdRunner.LastSecrets != nil && len(cmdRunner.LastSecrets) > 0 {
		t.Errorf("expected no secrets in environment for docker run, got %v", cmdRunner.LastSecrets)
	}
}

func TestRunDockerWithDeps_DockerCompose_Success(t *testing.T) {
	deps, _, _, _, cmdRunner, apiClient := NewTestDepsWithRunner()

	apiClient.PullResponse = &api.PullSecretsResponse{
		Content: "API_KEY=secret123",
	}

	opts := DockerOptions{
		EnvName:       "production",
		EnvFlagSet:    true,
		DockerCommand: "compose",
		DockerArgs:    []string{"up", "-d"},
	}

	err := runDockerWithDeps(opts, deps)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify docker was called
	if cmdRunner.LastCommand != "docker" {
		t.Errorf("expected command 'docker', got %q", cmdRunner.LastCommand)
	}

	// Verify args are "compose up -d"
	expectedArgs := []string{"compose", "up", "-d"}
	if len(cmdRunner.LastArgs) != len(expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, cmdRunner.LastArgs)
	}
	for i, expected := range expectedArgs {
		if i < len(cmdRunner.LastArgs) && cmdRunner.LastArgs[i] != expected {
			t.Errorf("expected arg[%d] = %q, got %q", i, expected, cmdRunner.LastArgs[i])
		}
	}

	// Verify secrets were passed via environment (compose uses env injection)
	if cmdRunner.LastSecrets["API_KEY"] != "secret123" {
		t.Errorf("expected API_KEY in secrets, got %v", cmdRunner.LastSecrets)
	}
}

func TestRunDockerWithDeps_UserEnvTakesPrecedence(t *testing.T) {
	deps, _, _, _, cmdRunner, apiClient := NewTestDepsWithRunner()

	// Vault has API_KEY=vault_secret
	apiClient.PullResponse = &api.PullSecretsResponse{
		Content: "API_KEY=vault_secret\nOTHER=other_value",
	}

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		// User explicitly sets API_KEY
		DockerArgs: []string{"-e", "API_KEY=user_override", "alpine"},
	}

	err := runDockerWithDeps(opts, deps)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that vault API_KEY was NOT injected (user provided their own)
	apiKeyCount := 0
	for i, arg := range cmdRunner.LastArgs {
		if arg == "-e" && i+1 < len(cmdRunner.LastArgs) && strings.HasPrefix(cmdRunner.LastArgs[i+1], "API_KEY=") {
			apiKeyCount++
			// Should be user's value, not vault's
			if cmdRunner.LastArgs[i+1] != "API_KEY=user_override" {
				t.Errorf("expected user's API_KEY, got %q", cmdRunner.LastArgs[i+1])
			}
		}
	}

	if apiKeyCount != 1 {
		t.Errorf("expected exactly 1 API_KEY, found %d in args: %v", apiKeyCount, cmdRunner.LastArgs)
	}

	// OTHER should still be injected from vault
	argsStr := strings.Join(cmdRunner.LastArgs, " ")
	if !strings.Contains(argsStr, "-e OTHER=other_value") {
		t.Errorf("expected OTHER to be injected, got args: %v", cmdRunner.LastArgs)
	}
}

func TestRunDockerWithDeps_GitError(t *testing.T) {
	deps, gitMock, _, uiMock, _, _ := NewTestDepsWithRunner()
	gitMock.RepoError = errors.New("not a git repo")

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		DockerArgs:    []string{"alpine"},
	}

	err := runDockerWithDeps(opts, deps)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(uiMock.ErrorCalls) == 0 {
		t.Error("expected UI.Error to be called")
	}
}

func TestRunDockerWithDeps_AuthError(t *testing.T) {
	deps, _, authMock, uiMock, _, _ := NewTestDepsWithRunner()
	authMock.Error = errors.New("not logged in")

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		DockerArgs:    []string{"alpine"},
	}

	err := runDockerWithDeps(opts, deps)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(uiMock.ErrorCalls) == 0 {
		t.Error("expected UI.Error to be called")
	}
}

func TestRunDockerWithDeps_APIError(t *testing.T) {
	deps, _, _, uiMock, _, apiClient := NewTestDepsWithRunner()
	apiClient.PullError = &api.APIError{
		StatusCode: 404,
		Detail:     "Vault not found",
	}

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		DockerArgs:    []string{"alpine"},
	}

	err := runDockerWithDeps(opts, deps)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(uiMock.ErrorCalls) == 0 {
		t.Error("expected UI.Error to be called")
	}
}

func TestFindImagePosition(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected int
	}{
		{
			name:     "simple image",
			args:     []string{"alpine"},
			expected: 0,
		},
		{
			name:     "with port flag",
			args:     []string{"-p", "8080:8080", "myapp:latest"},
			expected: 2,
		},
		{
			name:     "with multiple flags",
			args:     []string{"-d", "--rm", "-p", "80:80", "-v", "/data:/data", "nginx"},
			expected: 6,
		},
		{
			name:     "with env flag",
			args:     []string{"-e", "FOO=bar", "alpine"},
			expected: 2,
		},
		{
			name:     "with --name flag",
			args:     []string{"--name", "mycontainer", "alpine"},
			expected: 2,
		},
		{
			name:     "with equals syntax",
			args:     []string{"--name=mycontainer", "alpine"},
			expected: 1,
		},
		{
			name:     "image with command",
			args:     []string{"alpine", "echo", "hello"},
			expected: 0,
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: -1,
		},
		{
			name:     "only flags no image",
			args:     []string{"-d", "--rm"},
			expected: -1,
		},
		{
			name:     "complex real-world example",
			args:     []string{"-d", "--name", "web", "-p", "80:80", "-v", "/var/www:/www", "--restart", "always", "nginx:alpine"},
			expected: 9,
		},
		{
			name:     "with env equals syntax",
			args:     []string{"-e=FOO=bar", "alpine"},
			expected: 1,
		},
		{
			name:     "with multiple env vars",
			args:     []string{"-e", "A=1", "-e", "B=2", "-e", "C=3", "myimage"},
			expected: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findImagePosition(tt.args)
			if got != tt.expected {
				t.Errorf("findImagePosition(%v) = %d, want %d", tt.args, got, tt.expected)
			}
		})
	}
}

func TestExtractUserEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected map[string]string
	}{
		{
			name:     "short flag with space",
			args:     []string{"-e", "FOO=bar"},
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:     "long flag with space",
			args:     []string{"--env", "FOO=bar"},
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:     "short flag with equals",
			args:     []string{"-e=FOO=bar"},
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:     "long flag with equals",
			args:     []string{"--env=FOO=bar"},
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:     "multiple env vars",
			args:     []string{"-e", "A=1", "-e", "B=2"},
			expected: map[string]string{"A": "1", "B": "2"},
		},
		{
			name:     "env var inherit syntax (no value)",
			args:     []string{"-e", "PATH"},
			expected: map[string]string{"PATH": ""},
		},
		{
			name:     "mixed with other flags",
			args:     []string{"-p", "8080:8080", "-e", "FOO=bar", "-d"},
			expected: map[string]string{"FOO": "bar"},
		},
		{
			name:     "value with equals sign",
			args:     []string{"-e", "URL=http://example.com?foo=bar"},
			expected: map[string]string{"URL": "http://example.com?foo=bar"},
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: map[string]string{},
		},
		{
			name:     "no env vars",
			args:     []string{"-p", "8080:8080", "-d", "alpine"},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUserEnvVars(tt.args)
			if len(got) != len(tt.expected) {
				t.Errorf("extractUserEnvVars(%v) = %v, want %v", tt.args, got, tt.expected)
				return
			}
			for k, v := range tt.expected {
				if got[k] != v {
					t.Errorf("extractUserEnvVars(%v)[%q] = %q, want %q", tt.args, k, got[k], v)
				}
			}
		})
	}
}

func TestRunDockerWithDeps_EmptySecrets(t *testing.T) {
	deps, _, _, _, cmdRunner, apiClient := NewTestDepsWithRunner()

	// Vault returns empty content
	apiClient.PullResponse = &api.PullSecretsResponse{
		Content: "",
	}

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		DockerArgs:    []string{"alpine", "echo", "hello"},
	}

	err := runDockerWithDeps(opts, deps)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify docker was still called with original args
	expectedArgs := []string{"run", "alpine", "echo", "hello"}
	if len(cmdRunner.LastArgs) != len(expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, cmdRunner.LastArgs)
	}
}

func TestRunDockerRun_SecretsBeforeImage(t *testing.T) {
	deps, _, _, _, cmdRunner, apiClient := NewTestDepsWithRunner()

	apiClient.PullResponse = &api.PullSecretsResponse{
		Content: "SECRET=value",
	}

	opts := DockerOptions{
		EnvName:       "development",
		EnvFlagSet:    true,
		DockerCommand: "run",
		DockerArgs:    []string{"-d", "--name", "test", "myimage", "cmd", "arg"},
	}

	err := runDockerWithDeps(opts, deps)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find position of -e SECRET=value and myimage
	secretPos := -1
	imagePos := -1
	for i, arg := range cmdRunner.LastArgs {
		if arg == "-e" && i+1 < len(cmdRunner.LastArgs) && cmdRunner.LastArgs[i+1] == "SECRET=value" {
			secretPos = i
		}
		if arg == "myimage" {
			imagePos = i
		}
	}

	if secretPos == -1 {
		t.Errorf("SECRET not found in args: %v", cmdRunner.LastArgs)
	}
	if imagePos == -1 {
		t.Errorf("myimage not found in args: %v", cmdRunner.LastArgs)
	}
	if secretPos >= imagePos {
		t.Errorf("SECRET (-e at pos %d) should come before image (at pos %d), args: %v", secretPos, imagePos, cmdRunner.LastArgs)
	}
}
