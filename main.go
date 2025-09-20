package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "proji",
		Usage: "Fuzzy find and open project directory in with tmux sessions",
		Action: func(c *cli.Context) error {
			var selected string

			// Check if directory was provided as argument
			if c.NArg() > 0 {
				selected = c.Args().Get(0)
			} else {
				// Get directories and let user select
				dirs, err := getDirectories()
				if err != nil {
					return fmt.Errorf("failed to get directories: %w", err)
				}

				if len(dirs) == 0 {
					return fmt.Errorf("no directories found in specified paths")
				}

				selectedDir, err := selectDirectory(dirs)
				if err != nil {
					return fmt.Errorf("failed to select directory: %w", err)
				}

				if selectedDir == "" {
					return nil // User cancelled
				}
				selected = selectedDir
			}

			// Get session name from directory basename
			selectedName := strings.ReplaceAll(filepath.Base(selected), ".", "_")

			// Check tmux state
			tmuxRunning := isTmuxRunning()
			inTmux := os.Getenv("TMUX") != ""

			// If not in tmux and tmux is not running, create and attach directly
			if !inTmux && !tmuxRunning {
				return createTmuxSession(selectedName, selected, false)
			}

			// Create session if it doesn't exist
			if !tmuxHasSession(selectedName) {
				if err := createTmuxSession(selectedName, selected, true); err != nil {
					return err
				}
			}

			// Attach or switch to session
			tmuxPath, err := exec.LookPath("tmux")
			if err != nil {
				return fmt.Errorf("tmux not found in PATH: %w", err)
			}

			if !inTmux {
				// Not in tmux, attach to session
				return syscall.Exec(tmuxPath, []string{"tmux", "attach-session", "-t", selectedName}, os.Environ())
			} else {
				// In tmux, switch to session
				return syscall.Exec(tmuxPath, []string{"tmux", "switch-client", "-t", selectedName}, os.Environ())
			}
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func getDirectories() ([]string, error) {
	projectDir := os.Getenv("PROJECT_DIR")
	workDir := os.Getenv("WORK_DIR")
	assetDir := os.Getenv("ASSET_DIR")

	searchDirs := []string{}
	for _, dir := range []string{projectDir, workDir, assetDir} {
		if dir != "" {
			searchDirs = append(searchDirs, dir)
		}
	}

	if len(searchDirs) == 0 {
		return []string{}, nil
	}

	var dirs []string

	// Try fd first
	if commandExists("fd") {
		for _, searchDir := range searchDirs {
			if _, err := os.Stat(searchDir); err == nil {
				cmd := exec.Command("fd", ".", "--type", "d", "--max-depth", "1", searchDir)
				output, err := cmd.Output()
				if err == nil {
					for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
						if strings.TrimSpace(line) != "" {
							dirs = append(dirs, line)
						}
					}
				}
			}
		}
	}

	// Use find as fallback
	if len(dirs) == 0 {
		findArgs := []string{"-mindepth", "1", "-maxdepth", "1", "-type", "d"}
		findArgs = append(searchDirs, findArgs...)
		cmd := exec.Command("find", findArgs...)
		output, err := cmd.Output()
		if err == nil {
			for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
				if strings.TrimSpace(line) != "" {
					dirs = append(dirs, line)
				}
			}
		}
	}

	return dirs, nil
}

func selectDirectory(dirs []string) (string, error) {
	if !commandExists("fzf") {
		return "", fmt.Errorf("fzf is not installed. Please install fzf first")
	}

	cmd := exec.Command("fzf")
	cmd.Stdin = strings.NewReader(strings.Join(dirs, "\n"))

	output, err := cmd.Output()
	if err != nil {
		// fzf returns exit code 1 when user cancels, which is normal
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return "", nil // User cancelled
		}
		return "", fmt.Errorf("failed to run fzf: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func isTmuxRunning() bool {
	cmd := exec.Command("pgrep", "tmux")
	return cmd.Run() == nil
}

func tmuxHasSession(sessionName string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	return cmd.Run() == nil
}

func createTmuxSession(sessionName, directory string, detached bool) error {
	// Create new session
	args := []string{"new-session"}
	if detached {
		args = append(args, "-d")
	}
	args = append(args, "-s", sessionName, "-n", "code", "-c", directory)

	cmd := exec.Command("tmux", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tmux session '%s': %w", sessionName, err)
	}

	// Create second window (unnamed)
	cmd = exec.Command("tmux", "new-window", "-t", sessionName, "-c", directory)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create second window: %w", err)
	}

	// Send cd commands to ensure proper directory and prompt updates
	for _, window := range []string{"code", "1"} {
		target := fmt.Sprintf("%s:%s", sessionName, window)
		cdCmd := fmt.Sprintf("cd '%s'", directory)

		cmd = exec.Command("tmux", "send-keys", "-t", target, cdCmd, "Enter")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to send cd command to window %s: %w", window, err)
		}

		// Clear the screen after cd
		cmd = exec.Command("tmux", "send-keys", "-t", target, "clear", "Enter")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to send clear command to window %s: %w", window, err)
		}
	}

	return nil
}
