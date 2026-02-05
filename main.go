package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	yaml "gopkg.in/yaml.v2"
)

func runNonInteractive(config Config, format string) error {
	model := InitialModel(config)
	go model.fetcher()

	var allCommits []*commitInfo
	for commit := range model.processedCommitsChan {
		if len(allCommits) > 0 {
			lastCommit := allCommits[len(allCommits)-1]
			commit.CumulativeFiles = lastCommit.CumulativeFiles + commit.Files
			commit.CumulativeAdditions = lastCommit.CumulativeAdditions + commit.Additions
			commit.CumulativeDeletions = lastCommit.CumulativeDeletions + commit.Deletions
		} else {
			commit.CumulativeFiles = commit.Files
			commit.CumulativeAdditions = commit.Additions
			commit.CumulativeDeletions = commit.Deletions
		}
		allCommits = append(allCommits, commit)
	}

	var outputData []byte
	var err error

	switch format {
	case "json":
		outputData, err = json.MarshalIndent(allCommits, "", "  ")
	case "yaml":
		outputData, err = yaml.Marshal(allCommits)
	default:
		return fmt.Errorf("unsupported output format: %s. supported formats are: json, yaml", format)
	}

	if err != nil {
		return fmt.Errorf("failed to marshal output: %v", err)
	}

	fmt.Println(string(outputData))
	return nil
}

// Config holds the configurable options for the application
type Config struct {
	CommitLimit        int    `yaml:"commitLimit"`
	RepoPath           string `yaml:"repoPath"`
	AutoProgress       bool   `yaml:"autoProgress"`
	ProgressIntervalMs int    `yaml:"progressIntervalMs"`
	ReportMode         bool   `yaml:"reportMode"`
	ReportWorkers      int    `yaml:"reportWorkers"`
	ReportPreload      bool   `yaml:"reportPreload"`
	ReportPreloadExit  bool   `yaml:"reportPreloadExit"`
	ReportSamplePct    int    `yaml:"reportSamplePct"`
	ReportFilePath     string `yaml:"reportFile"`
}

func loadConfig() (Config, error) {
	config := Config{
		CommitLimit:        -1,
		RepoPath:           ".",
		AutoProgress:       true,
		ProgressIntervalMs: 50, // milliseconds
		ReportMode:         false,
		ReportWorkers:      0, // 0 means auto
		ReportPreload:      false,
		ReportPreloadExit:  false,
		ReportSamplePct:    0, // 0 means full run
		ReportFilePath:     "",
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
	outputFlag := flag.String("output", "", "Output format for non-interactive mode (json or yaml)")
	reportFlag := flag.Bool("report", config.ReportMode, "Load all data first, then show a final report view")
	reportWorkersFlag := flag.Int("workers", config.ReportWorkers, "Workers for report mode (0 = auto, >0 = exact)")
	reportPreloadFlag := flag.Bool("report-preload", config.ReportPreload, "Preload report data before starting the TUI")
	reportPreloadExitFlag := flag.Bool("report-preload-exit", config.ReportPreloadExit, "Exit after preloading the report (skip TUI)")
	reportSamplePctFlag := flag.Int("report-sample", config.ReportSamplePct, "Report sample percent (0 = full, 1-100)")
	reportFileFlag := flag.String("report-file", config.ReportFilePath, "Report file path for resume/save")
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
	config.ReportMode = *reportFlag
	config.ReportWorkers = *reportWorkersFlag
	config.ReportPreload = *reportPreloadFlag
	config.ReportPreloadExit = *reportPreloadExitFlag
	config.ReportSamplePct = *reportSamplePctFlag
	config.ReportFilePath = *reportFileFlag

	// If a positional argument is provided, it overrides repoPathFlag
	if flag.NArg() > 0 {
		config.RepoPath = flag.Arg(0)
	}

	if *outputFlag != "" {
		if err := runNonInteractive(config, *outputFlag); err != nil {
			log.Fatalf("Error in non-interactive mode: %v", err)
		}
		return
	}

	if config.ReportMode && config.ReportPreload {
		start := time.Now()
		progress := func(processed, total, workers int, engine string) {
			if total > 0 {
				percent := (float64(processed) / float64(total)) * 100
				fmt.Printf("\rPreloading report... %d/%d (%.1f%%) using %d workers (%s)", processed, total, percent, workers, engine)
			} else {
				fmt.Printf("\rPreloading report... using %d workers (%s)", workers, engine)
			}
		}

		engine := "git-par"
		progressGitPar := func(processed, total, workers int) {
			progress(processed, total, workers, engine)
		}
		repo, commits, maxAdditions, maxDeletions, total, workers, err := loadAllCommitsGitParallel(config, progressGitPar)
		if err != nil {
			log.Printf("Error preloading report: %v", err)
			return
		}
		elapsed := time.Since(start).Round(100 * time.Millisecond)
		fmt.Printf("\nPreload complete in %s using %s\n", elapsed, engine)

		if config.ReportPreloadExit {
			return
		}

		model := InitialModel(config)
		model.repo = repo
		model.commits = commits
		model.maxAdditions = maxAdditions
		model.maxDeletions = maxDeletions
		model.loadingComplete = true
		model.autoProgress = false
		model.reportTotal = total
		model.reportProcessed = total
		model.reportWorkers = workers
		model.reportEngine = engine
		if len(model.commits) > 0 {
			model.currentCommitIndex = len(model.commits) - 1
		}

		m := &model
		p := tea.NewProgram(m, tea.WithAltScreen())
		m.SetProgram(p)
		if _, err := p.Run(); err != nil {
			log.Printf("Error running program: %v", err)
		}
		return
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
