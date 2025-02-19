/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cli/go-gh"
	"github.com/grapple-solution/grapple_cli/utils"
	"github.com/spf13/cobra"
)

// InitCmd represents the init command
var InitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Grapple application",
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
	for {
		// Check if directory exists locally
		if _, err := os.Stat(projectName); os.IsNotExist(err) {
			// Check if repo exists on GitHub
			client, err := gh.RESTClient(nil)
			if err != nil {
				return fmt.Errorf("failed to create GitHub client: %w", err)
			}

			// Get authenticated user
			var user struct {
				Login string `json:"login"`
			}
			err = client.Get("user", &user)
			if err != nil {
				return fmt.Errorf("failed to get GitHub user: %w", err)
			}

			utils.InfoMessage(fmt.Sprintf("Checking if repository %s exists on GitHub", projectName))
			response := struct{ Name string }{}
			err = client.Get(fmt.Sprintf("repos/%s/%s", user.Login, projectName), &response)
			if err != nil { // Repo doesn't exist
				break
			}
		}

		utils.InfoMessage(fmt.Sprintf("Directory or repository %s already exists", projectName))
		if !autoConfirm {
			confirm, err := utils.PromptConfirm("Would you like to rename the project with an increment?")
			if err != nil || !confirm {
				return fmt.Errorf("operation cancelled by user")
			}
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
	}
	return nil
}
func authenticateGitHub() error {
	_, err := gh.RESTClient(nil)
	if err != nil {
		if githubToken == "" {
			return fmt.Errorf("GitHub token required for authentication")
		}
		os.Setenv("GITHUB_TOKEN", githubToken)
	}
	return nil
}

func createOrCloneRepository() error {
	client, err := gh.RESTClient(nil)
	if err != nil {
		return fmt.Errorf("failed to create GitHub client: %w", err)
	}

	// Check if repo exists
	response := struct{ Name string }{}
	err = client.Get(fmt.Sprintf("repos/%s", projectName), &response)
	repoExists := err == nil

	if repoExists {
		utils.InfoMessage("Cloning existing repository")
		_, _, err := gh.Exec("repo", "clone", fmt.Sprintf("https://github.com/%s.git", grappleTemplate), projectName)
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
		utils.InfoMessage(fmt.Sprintf("Creating repository %s from template %s", projectName, grappleTemplate))
		_, _, err := gh.Exec("repo", "create", projectName, "--template", grappleTemplate, "--public", "--clone")
		if err != nil {
			return fmt.Errorf("failed to create repository: %w", err)
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
