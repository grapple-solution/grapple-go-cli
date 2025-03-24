/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v54/github"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// InitCmd represents the init command
var InitCmd = &cobra.Command{
	Use:     "init",
	Aliases: []string{"i"},
	Short:   "Initialize a new Grapple application",
	Long: `Initialize a new Grapple application from a template.
This command creates a new project directory and sets up the initial project structure.`,
	RunE: initializeApplication,
}

func init() {
	InitCmd.Flags().StringVarP(&projectName, "project-name", "", "", "Name of the project")
	InitCmd.Flags().BoolVarP(&autoConfirm, "auto-confirm", "", false, "Automatically confirm all prompts")
	InitCmd.Flags().StringVarP(&githubToken, "github-token", "", "", "GitHub token for authentication")
	InitCmd.Flags().StringVarP(&grappleType, "grapple-type", "", "", "Project type (svelte or react)")
	InitCmd.Flags().StringVarP(&grappleTemplate, "grapple-template", "", "", "Template repository to use")
}

func initializeApplication(cmd *cobra.Command, args []string) error {
	logFile, _, logOnCliAndFileStart := utils.GetLogWriters("/tmp/grpl_app_init.log")
	defer func() {
		logFile.Sync()
		logFile.Close()
	}()

	logOnCliAndFileStart()

	// Set default grapple type
	if err := setDefaultGrappleType(); err != nil {
		return err
	}

	// Set template based on type
	setGrappleTemplate()

	// get GitHub token
	if err := getGitHubToken(); err != nil {
		return err
	}

	// Validate and get project name
	if err := validateAndSetProjectName(); err != nil {
		return err
	}

	// Handle directory naming conflicts
	if err := handleDirectoryConflicts(); err != nil {
		return err
	}

	// Authenticate GitHub
	if err := authenticateGitHub(); err != nil {
		return err
	}

	// Create or clone repository
	if err := createOrCloneRepository(); err != nil {
		return err
	}

	// Update README
	if err := updateReadme(); err != nil {
		return err
	}

	utils.SuccessMessage(fmt.Sprintf("Project %s initialized successfully!", projectName))
	printNextSteps()

	return nil
}

func setDefaultGrappleType() error {
	if grappleType == "" {
		if autoConfirm {
			grappleType = "svelte"
		} else {
			result, err := utils.PromptSelect("Select project type", []string{"svelte", "react"})
			if err != nil {
				return fmt.Errorf("failed to get project type: %w", err)
			}
			grappleType = result
		}
	}
	utils.InfoMessage(fmt.Sprintf("Using project type: %s", grappleType))
	return nil
}

func setGrappleTemplate() {
	if grappleTemplate == "" {
		if grappleType == "svelte" {
			grappleTemplate = "grapple-solution/grapple-svelte-template"
		} else if grappleType == "react" {
			grappleTemplate = "grapple-solution/grapple-react-template"
		}
	}
}

func validateAndSetProjectName() error {
	if projectName == "" {
		result, err := utils.PromptInput("Enter project name", utils.DefaultValue, `^[a-zA-Z0-9_-]+$`)
		if err != nil {
			return fmt.Errorf("invalid project name: %w", err)
		}
		projectName = result
	}
	return nil
}

func handleDirectoryConflicts() error {
	// Check if directory exists locally
	if _, err := os.Stat(projectName); !os.IsNotExist(err) {
		utils.InfoMessage(fmt.Sprintf("Directory %s already exists locally", projectName))
		askedOnce := false
		for {
			if !autoConfirm && !askedOnce {
				confirm, err := utils.PromptConfirm("Would you like to rename the project with an increment?")
				if err != nil || !confirm {
					return fmt.Errorf("operation cancelled by user")
				}
				askedOnce = true
			}

			parts := strings.Split(projectName, "-")
			lastPart := parts[len(parts)-1]
			if num, err := strconv.Atoi(lastPart); err == nil {
				parts[len(parts)-1] = strconv.Itoa(num + 1)
			} else {
				parts = append(parts, "1")
			}
			projectName = strings.Join(parts, "-")
			utils.InfoMessage(fmt.Sprintf("Trying new project name: %s", projectName))

			if _, err := os.Stat(projectName); os.IsNotExist(err) {
				break
			}
		}
		return nil
	}

	// Create GitHub client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	githubClient := github.NewClient(tc)

	// Get authenticated user
	user, _, err := githubClient.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get GitHub user: %w", err)
	}

	username := user.GetLogin()

	utils.InfoMessage(fmt.Sprintf("Checking if repository %s exists on GitHub", projectName))
	_, _, err = githubClient.Repositories.Get(ctx, username, projectName)
	if err == nil { // Repo exists
		if !autoConfirm {
			result, err := utils.PromptSelect(
				"Project with this name already exists on GitHub. What would you like to do?",
				[]string{"clone existing", "create new"},
			)
			if err != nil {
				return fmt.Errorf("prompt failed: %w", err)
			}

			if result == "clone existing" {
				return nil // Will be handled by createOrCloneRepository()
			}
		}

		// Find non-conflicting name for new project
		askedOnce := false
		for {
			if !autoConfirm && !askedOnce {
				utils.InfoMessage(fmt.Sprintf("Will try to find a non-conflicting name for %s", projectName))
				askedOnce = true
			}

			parts := strings.Split(projectName, "-")
			lastPart := parts[len(parts)-1]
			if num, err := strconv.Atoi(lastPart); err == nil {
				parts[len(parts)-1] = strconv.Itoa(num + 1)
			} else {
				parts = append(parts, "1")
			}
			projectName = strings.Join(parts, "-")
			utils.InfoMessage(fmt.Sprintf("Trying new project name: %s", projectName))

			// Check if new name exists
			_, _, err = githubClient.Repositories.Get(ctx, username, projectName)
			if err != nil { // Name is available
				break
			}
		}
	}

	return nil
}

func authenticateGitHub() error {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	githubClient := github.NewClient(tc)

	_, _, err := githubClient.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to authenticate with GitHub: %w", err)
	}
	return nil
}

func createOrCloneRepository() error {
	// Create a GitHub client using the go-github library
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	githubClient := github.NewClient(tc)

	// Get authenticated user
	user, _, err := githubClient.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get GitHub user: %w", err)
	}

	username := user.GetLogin()

	// Check if repo exists
	utils.InfoMessage(fmt.Sprintf("Checking if repository %s exists on GitHub", projectName))
	_, _, err = githubClient.Repositories.Get(ctx, username, projectName)
	repoExists := err == nil

	if repoExists {
		utils.InfoMessage("Cloning existing repository")

		// Clone using go-git
		repoURL := fmt.Sprintf("https://github.com/%s/%s.git", username, projectName)

		_, err := git.PlainClone(projectName, false, &git.CloneOptions{
			URL: repoURL,
			Auth: &http.BasicAuth{
				Username: "git", // This can be anything except empty string
				Password: githubToken,
			},
			Progress: os.Stdout,
		})

		if err != nil {
			return fmt.Errorf("failed to clone repository: %w", err)
		}
	} else {
		if !autoConfirm {
			confirm, err := utils.PromptConfirm("Repository doesn't exist. Create it?")
			if err != nil || !confirm {
				return fmt.Errorf("operation cancelled by user")
			}
		}

		// Parse template owner/repo
		parts := strings.Split(grappleTemplate, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid template format: %s, expected owner/repo", grappleTemplate)
		}
		templateOwner, templateRepo := parts[0], parts[1]

		utils.InfoMessage(fmt.Sprintf("Creating repository %s from template %s", projectName, grappleTemplate))

		// Create a repository from a template using go-github
		templateRepoRequest := &github.TemplateRepoRequest{
			Name:        github.String(projectName),
			Owner:       github.String(username),
			Description: github.String(fmt.Sprintf("Project created from template %s", grappleTemplate)),
			Private:     github.Bool(false),
		}

		_, _, err = githubClient.Repositories.CreateFromTemplate(
			ctx,
			templateOwner,
			templateRepo,
			templateRepoRequest,
		)

		if err != nil {
			return fmt.Errorf("failed to create repository from template: %w", err)
		}

		// Wait a moment for GitHub to fully set up the new repository
		time.Sleep(2 * time.Second)

		// Clone the newly created repository using go-git
		repoURL := fmt.Sprintf("https://github.com/%s/%s.git", username, projectName)

		_, err = git.PlainClone(projectName, false, &git.CloneOptions{
			URL: repoURL,
			Auth: &http.BasicAuth{
				Username: "git", // This can be anything except empty string
				Password: githubToken,
			},
			Progress: os.Stdout,
		})

		if err != nil {
			return fmt.Errorf("failed to clone new repository: %w", err)
		}
	}

	return nil
}

func updateReadme() error {
	readmePath := filepath.Join(projectName, "README.md")
	content, err := os.ReadFile(readmePath)
	if err != nil {
		return fmt.Errorf("error reading README: %w", err)
	}

	newContent := strings.ReplaceAll(string(content), "grapple-template", projectName)
	if err := os.WriteFile(readmePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("error updating README: %w", err)
	}
	return nil
}

func printNextSteps() {
	utils.InfoMessage("What's Next?")
	utils.InfoMessage(fmt.Sprintf("1. cd %s", projectName))
	utils.InfoMessage("2. Run 'grpl dev help' to see available commands")
	utils.InfoMessage("3. Run 'grpl dev ns <namespace>' to set up your namespace")
	utils.InfoMessage("4. Run 'grpl dev' to start the project")
}

func getGitHubToken() error {
	if githubToken == "" {
		// First try getting from environment
		githubToken = os.Getenv("GITHUB_TOKEN")
		// If still empty, prompt user
		if githubToken == "" {
			result, err := utils.PromptInput("Enter GitHub token", utils.DefaultValue, utils.AlphaNumericWithHyphenUnderscoreRegex)
			if err != nil {
				return fmt.Errorf("invalid GitHub token: %w", err)
			}
			githubToken = result
		}
	}
	os.Setenv("GITHUB_TOKEN", githubToken)

	return nil
}
