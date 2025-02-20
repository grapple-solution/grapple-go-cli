/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cli/go-gh"
	"github.com/cli/go-gh/pkg/api"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// UpdateCmd represents the update command
var UpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a Grapple application from template",
	Long: `Update a Grapple application by syncing differences from the template repository.
This command checks for and applies updates to configuration files and documentation.`,
	RunE: updateApplication,
}

func init() {
	UpdateCmd.Flags().StringVarP(&grappleTemplate, "grapple-template", "", "grapple-solutions/grapple-template", "Template repository to use")
	UpdateCmd.Flags().StringVarP(&githubToken, "github-token", "", "", "GitHub token for authentication")

}

func updateApplication(cmd *cobra.Command, args []string) error {
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_app_update.log")
	defer func() {
		logFile.Sync()
		logFile.Close()
	}()

	logOnCliAndFileStart()

	// Check if inside a grapple-template directory
	if err := validateGrappleTemplate(); err != nil {
		return err
	}

	// get github auth token
	err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("failed to get GitHub token: %w", err)
	}

	// Setup GitHub client
	_, err = gh.RESTClient(&api.ClientOptions{
		AuthToken: githubToken,
	})
	if err != nil {
		return fmt.Errorf("failed to create GitHub client: %w", err)
	}

	// Add and fetch template repository
	if err := setupTemplateRepo(); err != nil {
		return err
	}

	// Sync differences
	return syncDifferences()
}

func validateGrappleTemplate() error {
	chartPath := "./chart/Chart.yaml"
	if _, err := os.Stat(chartPath); os.IsNotExist(err) {
		return fmt.Errorf("this is not a grapple template. Either move into an existing grapple-template dir or run 'grpl app init' to create a new grapple-template")
	}

	content, err := os.ReadFile(chartPath)
	if err != nil {
		return fmt.Errorf("error reading Chart.yaml: %w", err)
	}

	if !strings.Contains(string(content), "dependencies:") || !strings.Contains(string(content), "name: gras-deploy") {
		return fmt.Errorf("this is not a grapple template. Either move into an existing grapple-template dir or run 'grpl app init' to create a new grapple-template")
	}

	utils.InfoMessage("Inside a grapple-template directory")
	return nil
}

func setupTemplateRepo() error {
	templateRepoPath := "./template"

	// Check if template repo already exists
	if _, err := os.Stat(templateRepoPath); os.IsNotExist(err) {
		utils.InfoMessage("Cloning template repository...")
		_, err := git.PlainClone(templateRepoPath, false, &git.CloneOptions{
			URL:      fmt.Sprintf("https://github.com/%s.git", grappleTemplate),
			Progress: os.Stdout,
		})
		if err != nil {
			return fmt.Errorf("failed to clone template repository: %w", err)
		}
	} else {
		utils.InfoMessage("Template repository already exists. Fetching updates...")
		repo, err := git.PlainOpen(templateRepoPath)
		if err != nil {
			return fmt.Errorf("failed to open existing template repository: %w", err)
		}
		w, err := repo.Worktree()
		if err != nil {
			return fmt.Errorf("failed to get worktree: %w", err)
		}
		err = w.Pull(&git.PullOptions{RemoteName: "origin"})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("failed to pull updates: %w", err)
		}
	}

	return nil
}

func syncDifferences() error {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return fmt.Errorf("failed to open current repository: %w", err)
	}

	// Get current HEAD reference
	_, err = repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	// Get working tree
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Fetch latest changes from remote
	err = repo.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Progress:   os.Stdout,
		Force:      true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to fetch updates: %w", err)
	}

	// Compare working tree against the latest commit
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("failed to get git status: %w", err)
	}

	files := []string{}
	patterns := []string{
		"devspace.yaml",
		"devspace_start.sh",
		"bitbucket-pipelines.yaml",
		"Dockerfile",
		"nginx.conf.template",
		"*.sh",
		"Taskfile.yaml",
		"grapi/README.md",
		"grapi/*/README.md",
		"gruim/README.md",
		"gruim/*/README.md",
	}

	// Identify files with real changes
	for filePath, fileStatus := range status {
		if fileStatus.Worktree == git.Modified || fileStatus.Worktree == git.Added || fileStatus.Worktree == git.Deleted {
			for _, pattern := range patterns {
				if matched, _ := filepath.Match(pattern, filePath); matched {
					files = append(files, filePath)
					break
				}
			}
		}
	}

	if len(files) == 0 {
		utils.InfoMessage("No differences found between the current branch and grapple-template")
		return nil
	}

	// Let user choose files to update
	choices := append([]string{"Exit", "Apply All"}, files...)
	selected, err := utils.PromptSelect("Select a file to view and apply the differences", choices)
	if err != nil {
		return err
	}

	switch selected {
	case "Exit":
		utils.InfoMessage("Exiting without applying further changes")
		return nil
	case "Apply All":
		for _, file := range files {
			utils.InfoMessage(fmt.Sprintf("Applying differences for %s...", file))
			err := applyGitChanges(".")
			if err != nil {
				return fmt.Errorf("failed to apply changes to %s: %w", file, err)
			}
		}
		utils.SuccessMessage("All differences applied")
	default:
		err := applyGitChanges(".")
		if err != nil {
			return fmt.Errorf("failed to apply changes to %s: %w", selected, err)
		}
		utils.SuccessMessage(fmt.Sprintf("%s updated", selected))
	}

	return nil
}

func applyGitChanges(repoPath string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	err = w.Checkout(&git.CheckoutOptions{
		Branch: plumbing.ReferenceName("refs/remotes/origin/main"),
		Force:  true,
	})

	if err != nil {
		return fmt.Errorf("failed to checkout file: %w", err)
	}

	return nil
}
