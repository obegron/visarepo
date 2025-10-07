package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"

	tea "github.com/charmbracelet/bubbletea"
	yaml "gopkg.in/yaml.v2"
)

// Config holds the configurable options for the application
type Config struct {
	CommitLimit        int    `yaml:"commitLimit"`
	RepoPath           string `yaml:"repoPath"`
	AutoProgress       bool   `yaml:"autoProgress"`
	ProgressIntervalMs int    `yaml:"progressIntervalMs"`
}

func loadConfig() (Config, error) {
	config := Config{
		CommitLimit:        -1,
		RepoPath:           ".",
		AutoProgress:       true,
		ProgressIntervalMs: 25, // milliseconds
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
