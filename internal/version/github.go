package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	githubReleasesURL = "https://api.github.com/repos/keywaysh/cli/releases/latest"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// FetchLatestVersion fetches the latest version from GitHub Releases
func FetchLatestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubReleasesURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "keyway-cli")

	client := &http.Client{Timeout: CheckTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return release.TagName, nil
}
