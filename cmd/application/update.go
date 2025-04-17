/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package application

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/spf13/cobra"
)

// UpdateCmd represents the update command
var UpdateCmd = &cobra.Command{
	Use:     "update",
	Aliases: []string{"u"},
	Short:   "Update a Grapple application from template",
	Long: `Update a Grapple application by syncing differences from the template repository.
This command checks for and applies updates to configuration files and documentation.`,
	RunE: updateApplication,
}

func init() {
	UpdateCmd.Flags().StringVarP(&grappleTemplate, "grapple-template", "", "", "Template repository to use")
	UpdateCmd.Flags().StringVarP(&githubToken, "github-token", "", "", "GitHub token for authentication")
}

func updateApplication(cmd *cobra.Command, args []string) error {

	logFileName := "grpl_app_update.log"
	logFilePath := utils.GetLogFilePath(logFileName)
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters(logFilePath)

	var err error

	defer func() {
		logFile.Sync()
		logFile.Close()
		if err != nil {
			utils.ErrorMessage(fmt.Sprintf("Failed to update application, please run cat %s for more details", logFilePath))
		}
	}()

	logOnCliAndFileStart()

	// Check if inside a grapple-template directory
	if err := validateGrappleTemplate(); err != nil {
		return err
	}

	// get github auth token
	if err := getGitHubToken(); err != nil {
		return fmt.Errorf("failed to get GitHub token: %w", err)
	}

	// Setup GitHub client and authenticate
	if err := authenticateGitHub(); err != nil {
		return err
	}

	if err := getTemplateRepo(); err != nil {
		return err
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

func getTemplateRepo() error {
	// Check if gruim folder exists
	gruimPath := "./gruim"
	if _, err := os.Stat(gruimPath); os.IsNotExist(err) {
		return fmt.Errorf("gruim folder not found")
	}

	// Check for .svelte files in gruim folder
	hasSvelteFiles := false
	err := filepath.Walk(gruimPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".svelte") {
			hasSvelteFiles = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error checking gruim folder: %w", err)
	}

	if hasSvelteFiles {
		grappleTemplate = "grapple-solution/grapple-svelte-template"
	} else {
		grappleTemplate = "grapple-solution/grapple-react-template"
	}

	utils.InfoMessage(fmt.Sprintf("grappleTemplate: %s", grappleTemplate))

	return nil
}

func setupTemplateRepo() error {
	templateRepoPath := "./template"

	// Check if template repo already exists
	if _, err := os.Stat(templateRepoPath); os.IsNotExist(err) {
		utils.InfoMessage("Cloning template repository...")
		_, err := git.PlainClone(templateRepoPath, false, &git.CloneOptions{
			URL: fmt.Sprintf("https://github.com/%s.git", grappleTemplate),
			Auth: &http.BasicAuth{
				Username: "git",
				Password: githubToken,
			},
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
		err = w.Pull(&git.PullOptions{
			RemoteName: "origin",
			Auth: &http.BasicAuth{
				Username: "git",
				Password: githubToken,
			},
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return fmt.Errorf("failed to pull updates: %w", err)
		}
	}

	return nil
}

func syncDifferences() error {
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

	// Get current repository
	repo, err := git.PlainOpen(".")
	if err != nil {
		return fmt.Errorf("failed to open current repository: %w", err)
	}

	// Ensure the template remote exists
	remotes, err := repo.Remotes()
	if err != nil {
		return fmt.Errorf("failed to get remotes: %w", err)
	}

	// Check if template remote exists
	templateRemoteExists := false
	for _, remote := range remotes {
		if remote.Config().Name == "template" {
			templateRemoteExists = true
			break
		}
	}

	// If template remote doesn't exist, create it
	if !templateRemoteExists {
		utils.InfoMessage("Adding template remote...")
		_, err = repo.CreateRemote(&config.RemoteConfig{
			Name: "template",
			URLs: []string{fmt.Sprintf("https://github.com/%s.git", grappleTemplate)},
		})
		if err != nil {
			return fmt.Errorf("failed to create template remote: %w", err)
		}
	}

	// Fetch latest from template
	utils.InfoMessage("Fetching updates from template...")
	err = repo.Fetch(&git.FetchOptions{
		RemoteName: "template",
		Auth: &http.BasicAuth{
			Username: "git",
			Password: githubToken,
		},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("failed to fetch template: %w", err)
	}

	// Get template branch reference (try main first, then master)
	templateRef, err := repo.Reference("refs/remotes/template/main", true)
	if err != nil {
		// Try template/master if main not found
		templateRef, err = repo.Reference("refs/remotes/template/master", true)
		if err != nil {
			return fmt.Errorf("failed to get template reference (tried main and master): %w", err)
		}
	}

	// Get template commit and tree
	templateCommit, err := repo.CommitObject(templateRef.Hash())
	if err != nil {
		return fmt.Errorf("failed to get template commit: %w", err)
	}

	templateTree, err := templateCommit.Tree()
	if err != nil {
		return fmt.Errorf("failed to get template tree: %w", err)
	}

	// Get all template files matching our patterns
	templateFiles := make(map[string]string) // path -> content
	err = templateTree.Files().ForEach(func(f *object.File) error {
		for _, pattern := range patterns {
			if matched, _ := filepath.Match(pattern, f.Name); matched {
				content, err := f.Contents()
				if err != nil {
					return fmt.Errorf("failed to read template file %s: %w", f.Name, err)
				}
				templateFiles[f.Name] = content
				break
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to enumerate template files: %w", err)
	}

	// Compare with local files
	var diffFiles []string
	for path, templateContent := range templateFiles {
		// Read local file
		localContent, err := os.ReadFile(path)
		if err != nil {
			// File doesn't exist locally or can't be read
			diffFiles = append(diffFiles, path)
			continue
		}

		// Normalize line endings to avoid false positives
		normalizedTemplate := strings.ReplaceAll(templateContent, "\r\n", "\n")
		normalizedLocal := strings.ReplaceAll(string(localContent), "\r\n", "\n")

		// Compare content (ignoring whitespace for better results)
		if normalizedTemplate != normalizedLocal {
			diffFiles = append(diffFiles, path)
		}
	}

	// Sort files alphabetically for consistency
	sort.Strings(diffFiles)

	if len(diffFiles) == 0 {
		utils.InfoMessage("No differences found between the current branch and grapple-template")
		return nil
	}

	// Let user choose files to update
	choices := append([]string{"Exit", "Apply All"}, diffFiles...)
	selected, err := utils.PromptSelect("Select a file to view and apply the differences", choices)
	if err != nil {
		return err
	}

	switch selected {
	case "Exit":
		utils.InfoMessage("Exiting without applying further changes")
		return nil
	case "Apply All":
		for _, file := range diffFiles {
			utils.InfoMessage(fmt.Sprintf("Applying differences for %s...", file))
			if err := applyFileChanges(file, templateFiles[file]); err != nil {
				return fmt.Errorf("failed to apply changes to %s: %w", file, err)
			}
		}
		utils.SuccessMessage("All differences applied")
	default:
		utils.InfoMessage(fmt.Sprintf("Applying differences for %s...", selected))
		if err := applyFileChanges(selected, templateFiles[selected]); err != nil {
			return fmt.Errorf("failed to apply changes to %s: %w", selected, err)
		}
		utils.SuccessMessage(fmt.Sprintf("%s updated", selected))
	}

	return nil
}

func applyFileChanges(filePath string, templateContent string) error {
	// Read local file content if it exists
	localContent, err := os.ReadFile(filePath)
	var localContentStr string
	if err == nil {
		localContentStr = string(localContent)
	}

	// Normalize line endings
	normalizedTemplate := strings.ReplaceAll(templateContent, "\r\n", "\n")
	normalizedLocal := strings.ReplaceAll(localContentStr, "\r\n", "\n")

	// Check if already identical
	if normalizedTemplate == normalizedLocal {
		utils.InfoMessage(fmt.Sprintf("File %s already matches the template version", filePath))
		return nil
	}

	// Show diff
	utils.InfoMessage(fmt.Sprintf("Changes for %s:", filePath))
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(normalizedLocal, normalizedTemplate, false)

	// Only show diff if local file exists
	if len(localContentStr) > 0 {
		fmt.Println(dmp.DiffPrettyText(diffs))
	} else {
		// For new files, just show content
		fmt.Println(templateContent)
	}

	// Ask for confirmation
	confirm, err := utils.PromptConfirm("Would you like to apply these changes?")
	if err != nil {
		return fmt.Errorf("failed to get confirmation: %w", err)
	}

	if !confirm {
		utils.InfoMessage("Changes not applied")
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	// Write template content to file
	if err := os.WriteFile(filePath, []byte(templateContent), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Get repository to stage the file
	repo, err := git.PlainOpen(".")
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Get working tree
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Stage the file
	_, err = wt.Add(filePath)
	if err != nil {
		return fmt.Errorf("failed to stage file: %w", err)
	}

	utils.InfoMessage(fmt.Sprintf("Applied changes to %s", filePath))

	// Remember that we applied this change to avoid showing it again
	return nil
}
