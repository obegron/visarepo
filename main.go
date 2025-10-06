package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	yaml "gopkg.in/yaml.v2"
)

// Config holds the configurable options for the application
type Config struct {
	SortBy             string `yaml:"sortBy"`
	CommitLimit        int    `yaml:"commitLimit"`
	RepoPath           string `yaml:"repoPath"`
	AutoProgress       bool   `yaml:"autoProgress"`
	ProgressIntervalMs int    `yaml:"progressIntervalMs"`
}

func loadConfig() (Config, error) {
	config := Config{
		SortBy:             "churn",
		CommitLimit:        -1,
		RepoPath:           ".",
		AutoProgress:       false,
		ProgressIntervalMs: 500, // milliseconds
	}

	configFile, err := os.ReadFile(".visagit.yml")
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil // No config file, return default config
		}
		return config, fmt.Errorf("failed to read config file: %v", err)
	}

	err = yaml.Unmarshal(configFile, &config)
	if err != nil {
		return config, fmt.Errorf("failed to unmarshal config file: %v", err)
	}

	return config, nil
}

func main() {
	// Load configuration from file
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	// --- Flags ---
	sortByFlag := flag.String("sort", config.SortBy, "Column to sort by: files, additions, deletions, churn")
	commitLimitFlag := flag.Int("limit", config.CommitLimit, "Number of commits to display")
	repoPathFlag := flag.String("repo", config.RepoPath, "Path to the Git repository")
	autoProgressFlag := flag.Bool("auto", config.AutoProgress, "Enable automatic progression")
	progressIntervalFlag := flag.Int("interval", config.ProgressIntervalMs, "Interval for automatic progression in milliseconds")
	profile := flag.Bool("profile", false, "profile cpu")
	flag.Parse()

	if *profile {
		f, err := os.Create("cpu.prof")
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	// Override config with command-line flags if provided
	config.SortBy = *sortByFlag
	config.CommitLimit = *commitLimitFlag
	config.RepoPath = *repoPathFlag
	config.AutoProgress = *autoProgressFlag
	config.ProgressIntervalMs = *progressIntervalFlag

	// If a positional argument is provided, it overrides repoPathFlag
	if flag.NArg() > 0 {
		config.RepoPath = flag.Arg(0)
	}

	// Create a new Bubble Tea model
	model := InitialModel(config)
	m := &model

	// Interactive mode with full terminal UI
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.SetProgram(p) // Pass the program reference to the model

	// Run the program
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running program: %v", err)
	}
}

func processCmds(m *Model, cmd tea.Cmd) {
	cmds := []tea.Cmd{cmd}
	maxIterations := 10000 // Prevent infinite loops
	iterations := 0

	for len(cmds) > 0 && iterations < maxIterations {
		iterations++
		cmd = cmds[0]
		cmds = cmds[1:]

		if cmd == nil {
			continue
		}

		msg := cmd()
		if batchMsg, ok := msg.(tea.BatchMsg); ok {
			// Unpack batch messages
			for _, batchCmd := range batchMsg {
				if batchCmd != nil {
					cmds = append(cmds, batchCmd)
				}
			}
		} else {
			if _, isTick := msg.(progressTickMsg); isTick {
				// In stream mode, we need to simulate the delay
				time.Sleep(m.progressInterval)
			}
		}
		newModel, newCmd := m.Update(msg)
		*m = *newModel.(*Model)
		if newCmd != nil {
			cmds = append(cmds, newCmd)
		}
	}
}

func parseFrames(s string) []int {
	var frames []int
	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			// Handle range like "0-10"
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) == 2 {
				start, err1 := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
				end, err2 := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
				if err1 == nil && err2 == nil {
					for i := start; i <= end; i++ {
						frames = append(frames, i)
					}
				}
			}
		} else {
			// Single frame
			frame, err := strconv.Atoi(part)
			if err == nil {
				frames = append(frames, frame)
			}
		}
	}
	return frames
}

